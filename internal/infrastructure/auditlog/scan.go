package auditlog

import (
	"bufio"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// maxAuditLineBytes bounds a single scanned line. Audit payloads are event metadata (hashes,
// addresses, keys), never file bodies, so 1 MiB is comfortably generous.
const maxAuditLineBytes = 1 << 20

// initialScanBuffer is the starting size of the line scanner's buffer; it grows up to
// maxAuditLineBytes as needed.
const initialScanBuffer = 64 * 1024

// AnomalyKind classifies a scan anomaly by how a caller should react.
type AnomalyKind int

const (
	// AnomalyTamper is corruption or tampering — malformed JSON, a missing/zero required field, a
	// failed signature, or a broken hash-chain link. A startup check refuses to run on these.
	AnomalyTamper AnomalyKind = iota
	// AnomalyClock is a backward-moving occurred_at — usually a host clock regression, not
	// tampering (an attacker cannot forge a signature, so a re-timestamped line fails as tamper).
	// A startup check warns but continues.
	AnomalyClock
)

// Anomaly is a single problem found while scanning an audit log.
type Anomaly struct {
	Line int
	Kind AnomalyKind
	Msg  string
}

// String renders the anomaly as "line N: <msg>".
func (a Anomaly) String() string { return fmt.Sprintf("line %d: %s", a.Line, a.Msg) }

// ScanResult summarizes an audit-log scan.
type ScanResult struct {
	Count        int
	FirstTime    time.Time
	LastTime     time.Time
	Anomalies    []Anomaly
	ChainChecked bool
}

// HasTamper reports whether any anomaly is a tamper/corruption anomaly (as opposed to a benign
// clock regression). Callers gate a hard failure on this.
func (r ScanResult) HasTamper() bool {
	for _, a := range r.Anomalies {
		if a.Kind == AnomalyTamper {
			return true
		}
	}
	return false
}

// Scan reads a JSONL audit log and checks each line's structure, signature (when pubKey is
// non-nil), hash-chain linkage, and timestamp monotonicity. It never stops early — every line is
// checked and all anomalies are collected. pubKey may be nil to skip signature verification. This
// is the single source of truth for audit-log verification; both `coordd audit verify` and the
// startup self-check call it.
func Scan(r io.Reader, pubKey ed25519.PublicKey) (ScanResult, error) {
	var res ScanResult
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, initialScanBuffer), maxAuditLineBytes)
	var lineNum int
	var prevTime time.Time
	var prevLineBytes []byte // raw JSON bytes of the previous valid line (no newline)

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry ports.AuditEvent
		if err := json.Unmarshal(line, &entry); err != nil {
			res.Anomalies = append(res.Anomalies, Anomaly{lineNum, AnomalyTamper, fmt.Sprintf("invalid JSON: %v", err)})
			continue
		}
		if missing := missingAuditFields(entry); len(missing) > 0 {
			res.Anomalies = append(res.Anomalies, Anomaly{lineNum, AnomalyTamper, fmt.Sprintf("missing required fields: %v", missing)})
			continue
		}

		if !prevTime.IsZero() && entry.OccurredAt.Before(prevTime) {
			res.Anomalies = append(res.Anomalies, Anomaly{lineNum, AnomalyClock, fmt.Sprintf(
				"timestamp %s is before previous entry %s",
				entry.OccurredAt.Format(time.RFC3339), prevTime.Format(time.RFC3339))})
		}
		if msg := checkSignature(entry, pubKey); msg != "" {
			res.Anomalies = append(res.Anomalies, Anomaly{lineNum, AnomalyTamper, msg})
		}
		if msg, checked := checkPrevHashLink(entry, prevLineBytes); msg != "" {
			res.Anomalies = append(res.Anomalies, Anomaly{lineNum, AnomalyTamper, msg})
		} else if checked {
			res.ChainChecked = true
		}

		if res.Count == 0 {
			res.FirstTime = entry.OccurredAt
		}
		res.LastTime = entry.OccurredAt
		prevTime = entry.OccurredAt
		prevLineBytes = append([]byte(nil), line...) // copy — scanner reuses the buffer
		res.Count++
	}
	return res, scanner.Err()
}

// missingAuditFields returns the names of required fields absent from entry. A zero occurred_at
// counts as missing — it is the exact defect the write-time stamping funnel guards against.
func missingAuditFields(entry ports.AuditEvent) []string {
	var missing []string
	if entry.LaunchID == "" {
		missing = append(missing, "launch_id")
	}
	if entry.EventName == "" {
		missing = append(missing, "event_name")
	}
	if entry.OccurredAt.IsZero() {
		missing = append(missing, "occurred_at")
	}
	if len(entry.Payload) == 0 {
		missing = append(missing, "payload")
	}
	return missing
}

// checkSignature verifies the entry's Ed25519 signature. Returns "" when valid (or when there is
// nothing to check — no pubkey, or an unsigned entry), else a description of the failure.
func checkSignature(entry ports.AuditEvent, pubKey ed25519.PublicKey) string {
	if pubKey == nil || entry.Signature == "" {
		return ""
	}
	sigBytes, err := base64.StdEncoding.DecodeString(entry.Signature)
	if err != nil {
		return fmt.Sprintf("invalid signature encoding: %v", err)
	}
	// Reproduce the signed bytes: the entry with only Signature zeroed, so PrevHash (and any
	// future field) is included automatically — the same shape the writer signs.
	noSig := entry
	noSig.Signature = ""
	msg, err := canonicaljson.MarshalForSigning(noSig)
	if err != nil {
		return fmt.Sprintf("re-marshaling for sig verify: %v", err)
	}
	if !ed25519.Verify(pubKey, msg, sigBytes) {
		return "signature verification FAILED"
	}
	return ""
}

// checkPrevHashLink verifies entry.PrevHash matches the SHA-256 of the previous line. Entries with
// an empty prev_hash are skipped (first entry, restart boundary, or a pre-chaining log): returns
// ("", false). On a verified link returns ("", true); on a mismatch returns a message.
func checkPrevHashLink(entry ports.AuditEvent, prevLineBytes []byte) (msg string, checked bool) {
	if entry.PrevHash == "" {
		return "", false
	}
	if len(prevLineBytes) == 0 {
		return "prev_hash set but no previous line exists", false
	}
	if want := sha256hex(prevLineBytes); entry.PrevHash != want {
		return fmt.Sprintf("prev_hash mismatch (want %s, got %s)", want, entry.PrevHash), false
	}
	return "", true
}

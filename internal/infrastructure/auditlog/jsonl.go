// Package auditlog implements ports.AuditLogWriter as an append-only JSONL file.
// Each line is a JSON-encoded AuditEvent. The file is never truncated or modified.
package auditlog

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"
	"github.com/rs/zerolog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// JSONLWriter writes audit events to an append-only JSONL file.
// A mutex serializes concurrent writes so lines are never interleaved.
type JSONLWriter struct {
	mu             sync.Mutex
	file           *os.File
	privKey        ed25519.PrivateKey    // nil disables signing
	prevHash       string                // SHA-256 hex of the last written line's JSON bytes
	prevHashStore  ports.AuditChainStore // nil = no cross-restart persistence
	logger         zerolog.Logger        // operational warnings (e.g. clock regression); defaults to Nop
	lastOccurredAt time.Time             // occurred_at of the last appended entry, for the monotonicity warning
}

// Open opens (or creates) the JSONL audit log at path.
// The file is opened with O_APPEND so the OS guarantees atomicity of small writes.
// privKey is an Ed25519 private key used to sign each entry. Pass nil to disable
// signing (entries will have an empty Signature field).
func Open(path string, privKey ed25519.PrivateKey) (*JSONLWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit log open %q: %w", path, err)
	}
	return &JSONLWriter{file: f, privKey: privKey, logger: zerolog.Nop()}, nil
}

// WithLogger sets the logger used for operational warnings — currently the append-time clock
// regression warning and the startup-scan results. Defaults to a no-op logger.
func (w *JSONLWriter) WithLogger(l zerolog.Logger) *JSONLWriter {
	w.logger = l
	return w
}

// PubKey returns the Ed25519 public key corresponding to the signing key.
// Returns nil if the writer was opened without a signing key.
func (w *JSONLWriter) PubKey() ed25519.PublicKey {
	if w.privKey == nil {
		return nil
	}
	return w.privKey.Public().(ed25519.PublicKey)
}

// WithPrevHashStore attaches a persistent store for the chain tip. On each call
// it loads the stored hash and verifies it matches the last line currently in the
// log file. A mismatch means lines were deleted (or added) between the last
// shutdown and now — the server refuses to start. Call this once after Open().
func (w *JSONLWriter) WithPrevHashStore(ctx context.Context, store ports.AuditChainStore) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	storedHash, err := store.LoadPrevHash(ctx)
	if err != nil {
		return fmt.Errorf("audit log load chain tip: %w", err)
	}

	if storedHash != "" {
		if err := verifyLastLineHash(w.file.Name(), storedHash); err != nil {
			return err
		}
	}

	w.prevHashStore = store
	w.prevHash = storedHash
	return nil
}

// verifyLastLineHash opens the log file read-only, reads the last non-empty line,
// and confirms its SHA-256 matches storedHash. Returns a descriptive error if not.
func verifyLastLineHash(path, storedHash string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("audit log: open for chain verification: %w", err)
	}
	defer f.Close()

	last, err := readLastLine(f)
	if err != nil {
		return fmt.Errorf("audit log: reading last line for chain verification: %w", err)
	}
	if len(last) == 0 {
		return fmt.Errorf(
			"audit log: chain tip mismatch — stored hash %s but log file is empty; "+
				"possible tampering or data loss — investigate before restarting", storedHash)
	}
	if got := sha256hex(last); got != storedHash {
		return fmt.Errorf(
			"audit log: chain tip mismatch — stored hash %s but last log line hashes to %s; "+
				"possible tampering or data loss — investigate before restarting", storedHash, got)
	}
	return nil
}

// readLastLine scans up to the last 64 KB of f and returns the last non-empty line
// (without its trailing newline). Returns nil if the file is empty.
func readLastLine(f *os.File) ([]byte, error) {
	const maxScan = 64 * 1024
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		return nil, nil
	}
	offset := info.Size() - maxScan
	if offset < 0 {
		offset = 0
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	var last []byte
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if b := scanner.Bytes(); len(b) > 0 {
			last = append([]byte(nil), b...) // copy — scanner reuses the buffer
		}
	}
	return last, scanner.Err()
}

// VerifyOnStart runs the boot-time integrity check. It always seeds the in-memory last-timestamp
// from the log's final entry, so the append-time clock-regression warning survives restarts. When
// full is true it also scans the entire log: any tamper/corruption anomaly (failed signature,
// broken hash-chain link, malformed or zero-field entry) refuses startup by returning an error,
// while a backward-timestamp anomaly only logs a warning. Call once, after WithPrevHashStore.
func (w *JSONLWriter) VerifyOnStart(full bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	last, err := lastEntryOccurredAt(w.file.Name())
	if err != nil {
		return fmt.Errorf("audit log: reading last entry timestamp: %w", err)
	}
	w.lastOccurredAt = last

	if !full {
		return nil
	}

	f, err := os.Open(w.file.Name())
	if err != nil {
		return fmt.Errorf("audit log: open for startup scan: %w", err)
	}
	defer f.Close()

	res, err := Scan(f, w.PubKey())
	if err != nil {
		return fmt.Errorf("audit log: startup scan: %w", err)
	}

	// Split anomalies by class: warn on clock regressions (benign host-clock history), refuse to
	// start on anything else (tamper or corruption).
	var tamper []string
	for _, a := range res.Anomalies {
		if a.Kind == AnomalyClock {
			w.logger.Warn().Str("anomaly", a.String()).
				Msg("audit log startup scan: backward timestamp — check host clock history")
		} else {
			tamper = append(tamper, a.String())
		}
	}
	if len(tamper) > 0 {
		return fmt.Errorf("audit log: startup scan found %d tamper/corruption anomaly(ies) —"+
			" refusing to start; investigate before restarting: %s", len(tamper), strings.Join(tamper, "; "))
	}
	w.logger.Info().Int("entries", res.Count).Msg("audit log startup scan: OK")
	return nil
}

// lastEntryOccurredAt returns the occurred_at of the log's final entry, or the zero time if the log
// is empty.
func lastEntryOccurredAt(path string) (time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	last, err := readLastLine(f)
	if err != nil {
		return time.Time{}, err
	}
	if len(last) == 0 {
		return time.Time{}, nil
	}
	var ev ports.AuditEvent
	if err := json.Unmarshal(last, &ev); err != nil {
		return time.Time{}, fmt.Errorf("parsing last audit entry: %w", err)
	}
	return ev.OccurredAt, nil
}

// Append serializes ev to JSON and writes a single newline-terminated line.
// If the writer was opened with a signing key, the Signature field is set to a
// base64 Ed25519 signature over the canonical JSON of the event (excluding the
// signature field itself). PrevHash is set to the SHA-256 hex of the previous
// line's JSON bytes and is covered by the signature, making deletions detectable.
// If a PrevHashStore is attached, the new tip is persisted so the chain survives
// server restarts.
func (w *JSONLWriter) Append(ctx context.Context, ev ports.AuditEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	ev.PrevHash = w.prevHash

	// Monotonicity is not enforced — the raw timestamp is preserved for forensics — but a backward
	// step signals a host clock regression, so surface it live. `coordd audit verify` flags it too.
	if !w.lastOccurredAt.IsZero() && ev.OccurredAt.Before(w.lastOccurredAt) {
		w.logger.Warn().
			Time("occurred_at", ev.OccurredAt).
			Time("previous", w.lastOccurredAt).
			Str("event", ev.EventName).
			Msg("audit clock regression: entry occurred_at precedes the previous entry — check host clock")
	}

	if w.privKey != nil {
		msg, err := canonicaljson.MarshalForSigning(ev)
		if err != nil {
			return fmt.Errorf("audit log sign: canonical json: %w", err)
		}
		ev.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(w.privKey, msg))
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("audit log marshal: %w", err)
	}

	if _, err := w.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("audit log write: %w", err)
	}
	w.prevHash = sha256hex(line)
	w.lastOccurredAt = ev.OccurredAt

	if w.prevHashStore != nil {
		if err := w.prevHashStore.SavePrevHash(ctx, w.prevHash); err != nil {
			return fmt.Errorf("audit log persist chain tip: %w", err)
		}
	}
	return nil
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ReadForLaunch opens the log file read-only and returns all entries whose
// LaunchID matches. Implements ports.AuditLogReader.
func (w *JSONLWriter) ReadForLaunch(_ context.Context, launchID string) ([]ports.AuditEvent, error) {
	f, err := os.Open(w.file.Name())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // no events yet — not an error
		}
		return nil, fmt.Errorf("audit log read: %w", err)
	}
	defer f.Close()

	var out []ports.AuditEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev ports.AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue // skip malformed lines
		}
		if ev.LaunchID == launchID {
			out = append(out, ev)
		}
	}
	return out, scanner.Err()
}

// Close flushes and closes the underlying file.
func (w *JSONLWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

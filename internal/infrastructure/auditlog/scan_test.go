package auditlog

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// writeChainedEntries appends named entries through a real writer (prev_hash chained), one minute
// apart, so the log is well-formed and hash-linked.
func writeChainedEntries(t *testing.T, path string, names []string) {
	t.Helper()
	w, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, name := range names {
		e := ports.AuditEvent{
			LaunchID:   "L1",
			EventName:  name,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Payload:    json.RawMessage(`{}`),
		}
		if err := w.Append(context.Background(), e); err != nil {
			t.Fatalf("Append %q: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func anomaliesContain(anoms []Anomaly, substr string) bool {
	for _, a := range anoms {
		if strings.Contains(a.Msg, substr) {
			return true
		}
	}
	return false
}

func hasKind(anoms []Anomaly, kind AnomalyKind) bool {
	for _, a := range anoms {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

func TestScan_BackwardsCompatible_NoPrevHash(t *testing.T) {
	// Entries written without prev_hash (old-format log): no chain anomalies expected.
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	f, _ := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, name := range []string{"A", "B", "C"} {
		entry := ports.AuditEvent{
			LaunchID:   "L1",
			EventName:  name,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Payload:    json.RawMessage(`{}`),
		}
		line, _ := json.Marshal(entry)
		_, _ = f.Write(append(line, '\n'))
	}
	_ = f.Close()

	rf, _ := os.Open(path)
	defer rf.Close()
	res, err := Scan(rf, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Count != 3 {
		t.Errorf("Count = %d, want 3", res.Count)
	}
	if len(res.Anomalies) != 0 {
		t.Errorf("Anomalies = %v, want none", res.Anomalies)
	}
	if res.ChainChecked {
		t.Errorf("ChainChecked = true, want false for entries without prev_hash")
	}
}

func TestScan_ValidChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	writeChainedEntries(t, path, []string{"A", "B", "C"})

	f, _ := os.Open(path)
	defer f.Close()
	res, err := Scan(f, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Count != 3 {
		t.Errorf("Count = %d, want 3", res.Count)
	}
	if len(res.Anomalies) != 0 {
		t.Errorf("Anomalies = %v, want none", res.Anomalies)
	}
	if !res.ChainChecked {
		t.Error("ChainChecked = false, want true for entries with prev_hash")
	}
}

func TestScan_DetectsDeletedEntry_Tamper(t *testing.T) {
	// Write 3 chained entries, delete line 2 ("B"); "C" no longer chains to its predecessor, so
	// expect a prev_hash mismatch — classified as tamper.
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	writeChainedEntries(t, path, []string{"A", "B", "C"})

	f, _ := os.Open(path)
	sc := bufio.NewScanner(f)
	var allLines []string
	for sc.Scan() {
		allLines = append(allLines, sc.Text())
	}
	_ = f.Close()

	f2, _ := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	for i, line := range allLines {
		if i == 1 { // drop "B"
			continue
		}
		_, _ = f2.WriteString(line + "\n")
	}
	_ = f2.Close()

	rf, _ := os.Open(path)
	defer rf.Close()
	res, err := Scan(rf, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !anomaliesContain(res.Anomalies, "prev_hash") {
		t.Errorf("expected a prev_hash anomaly, got: %v", res.Anomalies)
	}
	if !res.HasTamper() {
		t.Error("a prev_hash mismatch must classify as tamper")
	}
}

// A backward-moving timestamp is a clock anomaly, not tamper — a startup check warns, not refuses.
func TestScan_BackwardTimestamp_ClassifiedAsClock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	f, _ := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, ts := range []time.Time{base.Add(time.Minute), base} { // second precedes first
		entry := ports.AuditEvent{LaunchID: "L1", EventName: "E", OccurredAt: ts, Payload: json.RawMessage(`{}`)}
		line, _ := json.Marshal(entry)
		_, _ = f.Write(append(line, '\n'))
	}
	_ = f.Close()

	rf, _ := os.Open(path)
	defer rf.Close()
	res, err := Scan(rf, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !hasKind(res.Anomalies, AnomalyClock) {
		t.Errorf("expected a clock anomaly, got: %v", res.Anomalies)
	}
	if res.HasTamper() {
		t.Error("a backward timestamp must NOT classify as tamper")
	}
}

// A tampered payload/field breaks the Ed25519 signature — classified as tamper.
func TestScan_TamperedField_FailsSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, _ := Open(path, priv)
	e := ports.AuditEvent{
		LaunchID:   "L1",
		EventName:  "AAA",
		OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload:    json.RawMessage(`{}`),
	}
	_ = w.Append(context.Background(), e)
	_ = w.Close()

	// Flip the event_name on disk; the stored signature no longer matches.
	raw, _ := os.ReadFile(path)
	_ = os.WriteFile(path, bytes.Replace(raw, []byte(`"AAA"`), []byte(`"BBB"`), 1), 0o600)

	rf, _ := os.Open(path)
	defer rf.Close()
	res, err := Scan(rf, pub)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !anomaliesContain(res.Anomalies, "signature") {
		t.Errorf("expected a signature anomaly, got: %v", res.Anomalies)
	}
	if !res.HasTamper() {
		t.Error("a failed signature must classify as tamper")
	}
}

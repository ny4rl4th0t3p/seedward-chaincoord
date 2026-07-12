package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/auditlog"
)

func writeAuditEntries(t *testing.T, path string, names []string) {
	t.Helper()
	w, err := auditlog.Open(path, nil)
	if err != nil {
		t.Fatalf("auditlog.Open: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, name := range names {
		e := ports.AuditEvent{
			LaunchID:   "L1",
			EventName:  name,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Payload:    []byte(`{}`),
		}
		if err := w.Append(context.Background(), e); err != nil {
			t.Fatalf("Append %q: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScanAuditLog_BackwardsCompatible(t *testing.T) {
	// Entries written without prev_hash (old-format log): no chain anomalies expected.
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	f, _ := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, name := range []string{"A", "B", "C"} {
		entry := rawAuditEntry{
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
	res, err := scanAuditLog(rf, nil)
	if err != nil {
		t.Fatalf("scanAuditLog: %v", err)
	}
	if res.count != 3 {
		t.Errorf("count = %d, want 3", res.count)
	}
	if len(res.anomalies) != 0 {
		t.Errorf("anomalies = %v, want none", res.anomalies)
	}
	if res.chainChecked {
		t.Errorf("chainChecked = true, want false for entries without prev_hash")
	}
}

func TestScanAuditLog_ValidChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	writeAuditEntries(t, path, []string{"A", "B", "C"})

	f, _ := os.Open(path)
	defer f.Close()
	res, err := scanAuditLog(f, nil)
	if err != nil {
		t.Fatalf("scanAuditLog: %v", err)
	}
	if res.count != 3 {
		t.Errorf("count = %d, want 3", res.count)
	}
	if len(res.anomalies) != 0 {
		t.Errorf("anomalies = %v, want none", res.anomalies)
	}
	if !res.chainChecked {
		t.Error("chainChecked = false, want true for entries with prev_hash")
	}
}

func TestScanAuditLog_DetectsDeletedEntry(t *testing.T) {
	// Write 3 chained entries, delete line 2 ("B"); the next entry ("C", now line 2) no longer
	// chains to its predecessor, so expect a prev_hash mismatch reported on line 2.
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	writeAuditEntries(t, path, []string{"A", "B", "C"})

	// Read all lines.
	f, _ := os.Open(path)
	sc := bufio.NewScanner(f)
	var allLines []string
	for sc.Scan() {
		allLines = append(allLines, sc.Text())
	}
	_ = f.Close()

	// Rewrite file without line 2 (index 1 = "B").
	f2, _ := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	for i, line := range allLines {
		if i == 1 {
			continue
		}
		_, _ = f2.WriteString(line + "\n")
	}
	_ = f2.Close()

	rf, _ := os.Open(path)
	defer rf.Close()
	res, err := scanAuditLog(rf, nil)
	if err != nil {
		t.Fatalf("scanAuditLog: %v", err)
	}
	if len(res.anomalies) == 0 {
		t.Fatal("expected prev_hash mismatch anomaly, got none")
	}
	found := false
	for _, a := range res.anomalies {
		if strings.Contains(a, "prev_hash") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected prev_hash anomaly, got: %v", res.anomalies)
	}
}

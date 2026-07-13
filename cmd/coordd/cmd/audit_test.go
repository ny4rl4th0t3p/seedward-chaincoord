package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/auditlog"
)

// The scan logic itself is tested in the auditlog package (scan_test.go). These tests exercise the
// thin CLI wiring: runAuditVerify → auditlog.Scan → success/failure.

func TestAuditVerifyCmd_CleanLog_OK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := auditlog.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, name := range []string{"A", "B", "C"} {
		if err := w.Append(context.Background(), ports.AuditEvent{
			LaunchID:   "L1",
			EventName:  name,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Payload:    []byte(`{}`),
		}); err != nil {
			t.Fatalf("Append %q: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := newAuditVerifyCmd()
	cmd.SetArgs([]string{"--file", path})
	if err := cmd.Execute(); err != nil {
		t.Errorf("verify on a clean log should succeed, got: %v", err)
	}
}

func TestAuditVerifyCmd_MalformedLog_Fails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte("{ not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newAuditVerifyCmd()
	cmd.SetArgs([]string{"--file", path})
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err == nil {
		t.Error("verify on a malformed log must return a non-nil error")
	}
}

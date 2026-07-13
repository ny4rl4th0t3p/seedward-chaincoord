package auditlog

import (
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

	"github.com/rs/zerolog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

func mustAppend(t *testing.T, w *JSONLWriter, name string, ts time.Time) {
	t.Helper()
	if err := w.Append(context.Background(), ports.AuditEvent{
		LaunchID:   "L1",
		EventName:  name,
		OccurredAt: ts,
		Payload:    json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("Append %q: %v", name, err)
	}
}

// A backward-stamped entry warns live but the true timestamp is still written (no clamping/masking).
func TestAppend_WarnsOnClockRegression_ButWritesTrueTime(t *testing.T) {
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.WithLogger(zerolog.New(&buf))

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustAppend(t, w, "A", base.Add(time.Minute))
	mustAppend(t, w, "B", base) // earlier than A
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "clock regression") {
		t.Errorf("expected a clock-regression warning, log was: %s", buf.String())
	}
	last, err := lastEntryOccurredAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if !last.Equal(base) {
		t.Errorf("last occurred_at = %s, want the true backward time %s — must not be clamped", last, base)
	}
}

func TestAppend_NoWarnWhenMonotonic(t *testing.T) {
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.WithLogger(zerolog.New(&buf))

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustAppend(t, w, "A", base)
	mustAppend(t, w, "B", base.Add(time.Minute))
	_ = w.Close()

	if strings.Contains(buf.String(), "clock regression") {
		t.Errorf("monotonic timestamps must not warn, log was: %s", buf.String())
	}
}

// Full startup verify refuses to start on tamper (a flipped field fails the signature).
func TestVerifyOnStart_Full_RefusesOnTamper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, priv)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, w, "AAA", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	_ = w.Close()

	raw, _ := os.ReadFile(path)
	_ = os.WriteFile(path, bytes.Replace(raw, []byte(`"AAA"`), []byte(`"ZZZ"`), 1), 0o600)

	w2, err := Open(path, priv) // same key so PubKey() matches; like a restart
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	w2.WithLogger(zerolog.New(&bytes.Buffer{}))
	if err := w2.VerifyOnStart(true); err == nil {
		t.Error("full startup verify must refuse to start on tamper")
	}
	_ = pub
}

// A backward timestamp is a clock anomaly — full startup verify warns but does NOT refuse.
func TestVerifyOnStart_Full_WarnsOnClock_DoesNotRefuse(t *testing.T) {
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustAppend(t, w, "A", base.Add(time.Minute))
	mustAppend(t, w, "B", base) // backward
	_ = w.Close()

	w2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	w2.WithLogger(zerolog.New(&buf))
	if err := w2.VerifyOnStart(true); err != nil {
		t.Errorf("a backward timestamp must not refuse startup, got: %v", err)
	}
	if !strings.Contains(buf.String(), "backward timestamp") {
		t.Errorf("expected a clock warning from the startup scan, log was: %s", buf.String())
	}
}

// VerifyOnStart seeds lastOccurredAt (even in tail mode) so a later backward append warns.
func TestVerifyOnStart_SeedsLastOccurredAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, w, "A", base.Add(time.Hour))
	_ = w.Close()

	var buf bytes.Buffer
	w2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	w2.WithLogger(zerolog.New(&buf))
	if err := w2.VerifyOnStart(false); err != nil { // tail mode still seeds
		t.Fatal(err)
	}
	mustAppend(t, w2, "B", base) // earlier than the seeded last entry
	if !strings.Contains(buf.String(), "clock regression") {
		t.Errorf("VerifyOnStart must seed lastOccurredAt so a later backward append warns; log: %s", buf.String())
	}
}

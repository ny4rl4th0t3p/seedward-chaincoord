package auditlog

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/pkg/canonicaljson"
)

func openTmp(t *testing.T) (w *JSONLWriter, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, path
}

func ev(name, launchID string) ports.AuditEvent {
	return ports.AuditEvent{
		LaunchID:   launchID,
		EventName:  name,
		OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload:    []byte(`{}`),
	}
}

func TestOpen_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestOpen_BadPath(t *testing.T) {
	_, err := Open("/nonexistent/dir/audit.jsonl", nil)
	if err == nil {
		t.Fatal("expected error for bad path")
	}
}

func TestAppend_WritesValidJSONL(t *testing.T) {
	w, path := openTmp(t)
	if err := w.Append(context.Background(), ev("TestEvent", "launch-1")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	_ = w.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}
	if m["event_name"] != "TestEvent" {
		t.Errorf("event_name: got %v", m["event_name"])
	}
}

func TestAppend_MultipleEvents_OrderPreserved(t *testing.T) {
	w, path := openTmp(t)
	for _, name := range []string{"first", "second", "third"} {
		if err := w.Append(context.Background(), ev(name, "launch-1")); err != nil {
			t.Fatalf("Append %q: %v", name, err)
		}
	}
	_ = w.Close()

	f, _ := os.Open(path)
	defer f.Close()
	var names []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e ports.AuditEvent
		_ = json.Unmarshal(scanner.Bytes(), &e)
		names = append(names, e.EventName)
	}
	want := []string{"first", "second", "third"}
	for i, n := range want {
		if i >= len(names) || names[i] != n {
			t.Errorf("line %d: got %q, want %q", i, names[i], n)
		}
	}
}

func TestAppend_Concurrent_NoInterleaving(t *testing.T) {
	w, path := openTmp(t)

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = w.Append(context.Background(), ev("evt", "launch-1"))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}

	f, _ := os.Open(path)
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
		if err := json.Unmarshal(scanner.Bytes(), &map[string]any{}); err != nil {
			t.Errorf("line %d is not valid JSON: %v", count, err)
		}
	}
	if count != n {
		t.Errorf("expected %d lines, got %d", n, count)
	}
}

func TestReadForLaunch_FiltersByLaunchID(t *testing.T) {
	w, _ := openTmp(t)
	ctx := context.Background()
	_ = w.Append(ctx, ev("A", "launch-1"))
	_ = w.Append(ctx, ev("B", "launch-2"))
	_ = w.Append(ctx, ev("C", "launch-1"))

	got, err := w.ReadForLaunch(ctx, "launch-1")
	if err != nil {
		t.Fatalf("ReadForLaunch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].EventName != "A" || got[1].EventName != "C" {
		t.Errorf("unexpected events: %v, %v", got[0].EventName, got[1].EventName)
	}
}

func TestReadForLaunch_EmptyResult(t *testing.T) {
	w, _ := openTmp(t)
	_ = w.Append(context.Background(), ev("X", "launch-99"))

	got, err := w.ReadForLaunch(context.Background(), "launch-nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 events, got %d", len(got))
	}
}

func TestReadForLaunch_SkipsMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	// Write one malformed line then one valid line directly.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("not json at all\n")
	valid, _ := json.Marshal(ev("Good", "launch-1"))
	_, _ = f.Write(append(valid, '\n'))
	_ = f.Close()

	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	got, err := w.ReadForLaunch(context.Background(), "launch-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].EventName != "Good" {
		t.Errorf("expected 1 good event, got %v", got)
	}
}

func TestAppend_SignsEntryWhenKeyProvided(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, priv)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	if err := w.Append(context.Background(), ev("TestEvent", "launch-1")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	_ = w.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Scan()
	line := scanner.Bytes()

	var entry ports.AuditEvent
	if err := json.Unmarshal(line, &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Signature == "" {
		t.Fatal("expected Signature to be set")
	}

	sigBytes, err := base64.StdEncoding.DecodeString(entry.Signature)
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}

	// Strip signature field and canonicalize to reproduce the signed bytes.
	entry.Signature = ""
	msg, err := canonicaljson.MarshalForSigning(entry)
	if err != nil {
		t.Fatalf("MarshalForSigning: %v", err)
	}
	if !ed25519.Verify(pub, msg, sigBytes) {
		t.Error("signature verification failed")
	}
}

func TestAppend_NoSignatureWhenNoKey(t *testing.T) {
	w, path := openTmp(t)
	if err := w.Append(context.Background(), ev("TestEvent", "launch-1")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	_ = w.Close()

	f, _ := os.Open(path)
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Scan()
	var entry ports.AuditEvent
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Signature != "" {
		t.Errorf("expected empty signature without key, got %q", entry.Signature)
	}
}

// mockChainStore is an in-memory ports.AuditChainStore for tests.
type mockChainStore struct {
	mu   sync.Mutex
	hash string
}

func (m *mockChainStore) LoadPrevHash(_ context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hash, nil
}

func (m *mockChainStore) SavePrevHash(_ context.Context, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hash = hash
	return nil
}

func TestAppend_ChainsPrevHash(t *testing.T) {
	w, path := openTmp(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, name := range []string{"first", "second", "third"} {
		e := ports.AuditEvent{
			LaunchID:   "L1",
			EventName:  name,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Payload:    []byte(`{}`),
		}
		if err := w.Append(ctx, e); err != nil {
			t.Fatalf("Append %q: %v", name, err)
		}
	}
	_ = w.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var lines [][]byte
	var entries []ports.AuditEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		lines = append(lines, raw)
		var e ports.AuditEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("unmarshal line %d: %v", len(lines), err)
		}
		entries = append(entries, e)
	}

	if entries[0].PrevHash != "" {
		t.Errorf("entry 0: want empty prev_hash, got %q", entries[0].PrevHash)
	}
	if got, want := entries[1].PrevHash, sha256hex(lines[0]); got != want {
		t.Errorf("entry 1: prev_hash = %q, want %q", got, want)
	}
	if got, want := entries[2].PrevHash, sha256hex(lines[1]); got != want {
		t.Errorf("entry 2: prev_hash = %q, want %q", got, want)
	}
}

func TestWithPrevHashStore_RestoresChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ctx := context.Background()
	store := &mockChainStore{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Session 1: write 2 entries.
	w1, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w1.WithPrevHashStore(ctx, store); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"first", "second"} {
		_ = w1.Append(ctx, ports.AuditEvent{
			LaunchID: "L1", EventName: name,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Payload:    []byte(`{}`),
		})
	}
	_ = w1.Close()

	storedHash := store.hash // sha256(line2)

	// Session 2: new writer, same store — chain should continue.
	w2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if err := w2.WithPrevHashStore(ctx, store); err != nil {
		t.Fatalf("WithPrevHashStore on restart: %v", err)
	}
	_ = w2.Append(ctx, ports.AuditEvent{
		LaunchID: "L1", EventName: "third",
		OccurredAt: base.Add(2 * time.Minute),
		Payload:    []byte(`{}`),
	})
	_ = w2.Close()

	// The third entry must chain from the hash stored after session 1.
	f, _ := os.Open(path)
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var allEntries []ports.AuditEvent
	for scanner.Scan() {
		var e ports.AuditEvent
		_ = json.Unmarshal(scanner.Bytes(), &e)
		allEntries = append(allEntries, e)
	}

	if got := allEntries[2].PrevHash; got != storedHash {
		t.Errorf("entry 2 prev_hash = %q, want stored hash %q", got, storedHash)
	}
}

func TestWithPrevHashStore_DetectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ctx := context.Background()
	store := &mockChainStore{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Write 2 entries normally.
	w1, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w1.WithPrevHashStore(ctx, store); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"first", "second"} {
		_ = w1.Append(ctx, ports.AuditEvent{
			LaunchID: "L1", EventName: name,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Payload:    []byte(`{}`),
		})
	}
	_ = w1.Close()

	storedHash := store.hash // sha256(line2)

	// Tamper: rewrite the file keeping only line 1 (delete line 2).
	f, _ := os.Open(path)
	sc := bufio.NewScanner(f)
	sc.Scan()
	line1 := sc.Text()
	_ = f.Close()

	f2, _ := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	_, _ = f2.WriteString(line1 + "\n")
	_ = f2.Close()

	// Open new writer with the original stored hash — must fail.
	w2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if err := w2.WithPrevHashStore(ctx, &mockChainStore{hash: storedHash}); err == nil {
		t.Fatal("expected tampering error, got nil")
	}
}

func TestWithPrevHashStore_EmptyFileStoredHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// Store claims there was a previous entry but the file is empty.
	if err := w.WithPrevHashStore(context.Background(), &mockChainStore{hash: "abc123"}); err == nil {
		t.Fatal("expected error for stored hash with empty file, got nil")
	}
}

func TestClose_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second close returns an error (file already closed) — we just ensure it doesn't panic.
	_ = w.Close()
}

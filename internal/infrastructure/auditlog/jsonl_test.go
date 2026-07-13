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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

func openTmp(t *testing.T) (w *JSONLWriter, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	require.NoError(t, err, "Open")
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
	require.NoError(t, err, "Open")
	defer w.Close()
	_, err = os.Stat(path)
	assert.NoError(t, err, "file not created")
}

func TestOpen_BadPath(t *testing.T) {
	_, err := Open("/nonexistent/dir/audit.jsonl", nil)
	require.Error(t, err, "expected error for bad path")
	assert.ErrorIs(t, err, os.ErrNotExist, "a missing parent dir must surface as a not-exist error")
}

func TestAppend_WritesValidJSONL(t *testing.T) {
	w, path := openTmp(t)
	require.NoError(t, w.Append(context.Background(), ev("TestEvent", "launch-1")), "Append")
	_ = w.Close()

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	require.Len(t, lines, 1)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &m), "line is not valid JSON")
	assert.Equal(t, "TestEvent", m["event_name"])
}

func TestAppend_MultipleEvents_OrderPreserved(t *testing.T) {
	w, path := openTmp(t)
	for _, name := range []string{"first", "second", "third"} {
		require.NoError(t, w.Append(context.Background(), ev(name, "launch-1")), "Append %q", name)
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
	assert.Equal(t, []string{"first", "second", "third"}, names)
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
		require.NoError(t, err, "goroutine %d", i)
	}

	f, _ := os.Open(path)
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
		var m map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &m), "line %d is not valid JSON", count)
	}
	assert.Equal(t, n, count)
}

func TestReadForLaunch_FiltersByLaunchID(t *testing.T) {
	w, _ := openTmp(t)
	ctx := context.Background()
	_ = w.Append(ctx, ev("A", "launch-1"))
	_ = w.Append(ctx, ev("B", "launch-2"))
	_ = w.Append(ctx, ev("C", "launch-1"))

	got, err := w.ReadForLaunch(ctx, "launch-1")
	require.NoError(t, err, "ReadForLaunch")
	require.Len(t, got, 2)
	assert.Equal(t, "A", got[0].EventName)
	assert.Equal(t, "C", got[1].EventName)
}

func TestReadForLaunch_EmptyResult(t *testing.T) {
	w, _ := openTmp(t)
	_ = w.Append(context.Background(), ev("X", "launch-99"))

	got, err := w.ReadForLaunch(context.Background(), "launch-nope")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestReadForLaunch_SkipsMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	// Write one malformed line then one valid line directly.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, _ = f.WriteString("not json at all\n")
	valid, _ := json.Marshal(ev("Good", "launch-1"))
	_, _ = f.Write(append(valid, '\n'))
	_ = f.Close()

	w, err := Open(path, nil)
	require.NoError(t, err)
	defer w.Close()

	got, err := w.ReadForLaunch(context.Background(), "launch-1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Good", got[0].EventName)
}

func TestAppend_SignsEntryWhenKeyProvided(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err, "generating key")

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, priv)
	require.NoError(t, err, "Open")
	defer w.Close()

	require.NoError(t, w.Append(context.Background(), ev("TestEvent", "launch-1")), "Append")
	_ = w.Close()

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Scan()
	line := scanner.Bytes()

	var entry ports.AuditEvent
	require.NoError(t, json.Unmarshal(line, &entry), "unmarshal")
	require.NotEmpty(t, entry.Signature, "expected Signature to be set")

	sigBytes, err := base64.StdEncoding.DecodeString(entry.Signature)
	require.NoError(t, err, "decoding signature")

	// Strip signature field and canonicalize to reproduce the signed bytes.
	entry.Signature = ""
	msg, err := canonicaljson.MarshalForSigning(entry)
	require.NoError(t, err, "MarshalForSigning")
	assert.True(t, ed25519.Verify(pub, msg, sigBytes), "signature verification failed")
}

func TestAppend_SignatureCoversPrevHashAndPayload(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err, "generating key")

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, priv)
	require.NoError(t, err, "Open")
	require.NoError(t, w.Append(context.Background(), ev("TestEvent", "launch-1")), "Append")
	_ = w.Close()

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Scan()
	var entry ports.AuditEvent
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &entry), "unmarshal")
	sigBytes, err := base64.StdEncoding.DecodeString(entry.Signature)
	require.NoError(t, err)
	entry.Signature = ""

	// Baseline: the untampered entry verifies.
	msg, err := canonicaljson.MarshalForSigning(entry)
	require.NoError(t, err)
	require.True(t, ed25519.Verify(pub, msg, sigBytes), "baseline signature must verify")

	// Tampering prev_hash — the re-chaining attack — must invalidate the signature. This is the
	// property that stops an attacker from deleting an entry and recomputing the chain: prev_hash
	// is covered by the signature, so a rewritten chain can't be re-signed without the audit key.
	tampered := entry
	tampered.PrevHash = "0000000000000000000000000000000000000000000000000000000000000000"
	msg, err = canonicaljson.MarshalForSigning(tampered)
	require.NoError(t, err)
	assert.False(t, ed25519.Verify(pub, msg, sigBytes), "mutating prev_hash must break the signature")

	// Tampering the payload likewise breaks it.
	tampered = entry
	tampered.EventName = "ForgedEvent"
	msg, err = canonicaljson.MarshalForSigning(tampered)
	require.NoError(t, err)
	assert.False(t, ed25519.Verify(pub, msg, sigBytes), "mutating the payload must break the signature")
}

func TestAppend_NoSignatureWhenNoKey(t *testing.T) {
	w, path := openTmp(t)
	require.NoError(t, w.Append(context.Background(), ev("TestEvent", "launch-1")), "Append")
	_ = w.Close()

	f, _ := os.Open(path)
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Scan()
	var entry ports.AuditEvent
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &entry), "unmarshal")
	assert.Empty(t, entry.Signature, "expected empty signature without key")
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
		require.NoError(t, w.Append(ctx, e), "Append %q", name)
	}
	_ = w.Close()

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var lines [][]byte
	var entries []ports.AuditEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		lines = append(lines, raw)
		var e ports.AuditEvent
		require.NoError(t, json.Unmarshal(raw, &e), "unmarshal line %d", len(lines))
		entries = append(entries, e)
	}

	assert.Empty(t, entries[0].PrevHash, "entry 0: want empty prev_hash")
	assert.Equal(t, sha256hex(lines[0]), entries[1].PrevHash, "entry 1 prev_hash")
	assert.Equal(t, sha256hex(lines[1]), entries[2].PrevHash, "entry 2 prev_hash")
}

func TestWithPrevHashStore_RestoresChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ctx := context.Background()
	store := &mockChainStore{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Session 1: write 2 entries.
	w1, err := Open(path, nil)
	require.NoError(t, err)
	require.NoError(t, w1.WithPrevHashStore(ctx, store))
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
	require.NoError(t, err)
	defer w2.Close()
	require.NoError(t, w2.WithPrevHashStore(ctx, store), "WithPrevHashStore on restart")
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

	assert.Equal(t, storedHash, allEntries[2].PrevHash, "entry 2 must chain from the stored hash")
}

func TestWithPrevHashStore_DetectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ctx := context.Background()
	store := &mockChainStore{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Write 2 entries normally.
	w1, err := Open(path, nil)
	require.NoError(t, err)
	require.NoError(t, w1.WithPrevHashStore(ctx, store))
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
	require.NoError(t, err)
	defer w2.Close()
	err = w2.WithPrevHashStore(ctx, &mockChainStore{hash: storedHash})
	assert.Error(t, err, "expected tampering error")
}

func TestWithPrevHashStore_EmptyFileStoredHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	require.NoError(t, err)
	defer w.Close()
	// Store claims there was a previous entry but the file is empty.
	err = w.WithPrevHashStore(context.Background(), &mockChainStore{hash: "abc123"})
	assert.Error(t, err, "expected error for stored hash with empty file")
}

func TestClose_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := Open(path, nil)
	require.NoError(t, err)
	require.NoError(t, w.Close(), "first Close")
	// Second close returns an error (file already closed) — we just ensure it doesn't panic.
	_ = w.Close()
}

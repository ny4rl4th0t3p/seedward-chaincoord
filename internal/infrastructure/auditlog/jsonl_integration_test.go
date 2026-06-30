//go:build integration

package auditlog

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

func TestIntegration_AuditLog_FileCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	w, err := Open(path, nil)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = w.Close() })

	_, err = os.Stat(path)
	assert.NoError(t, err, "expected audit log file to exist after Open")
}

func TestIntegration_AuditLog_AppendAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	w, err := Open(path, nil)
	require.NoError(t, err, "Open")

	ev := ports.AuditEvent{
		EventName:  "ValidatorApproved",
		OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload:    []byte(`{"launch_id":"abc"}`),
	}
	require.NoError(t, w.Append(context.Background(), ev), "Append")
	require.NoError(t, w.Close(), "Close")

	f, err := os.Open(path)
	require.NoError(t, err, "open for read")
	defer f.Close()

	var lines []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var m map[string]any
		if !assert.NoError(t, json.Unmarshal(scanner.Bytes(), &m), "line is not valid JSON: %s", scanner.Bytes()) {
			continue
		}
		lines = append(lines, m)
	}
	require.NoError(t, scanner.Err(), "scan")

	require.Len(t, lines, 1)
	assert.Equal(t, "ValidatorApproved", lines[0]["event_name"])
}

func TestIntegration_AuditLog_ConcurrentAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	w, err := Open(path, nil)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = w.Close() })

	const goroutines = 50
	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = w.Append(context.Background(), ports.AuditEvent{
				EventName:  "TestEvent",
				OccurredAt: time.Now().UTC(),
				Payload:    []byte(`{}`),
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d", i)
	}

	// Verify all lines were written and are valid JSON (no interleaving).
	f, err := os.Open(path)
	require.NoError(t, err, "open for read")
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
		var m map[string]any
		assert.NoError(t, json.Unmarshal(scanner.Bytes(), &m), "line %d is not valid JSON", count)
	}
	require.NoError(t, scanner.Err(), "scan")
	assert.Equal(t, goroutines, count)
}

func TestIntegration_AuditLog_AppendOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	// First writer appends one event.
	w1, err := Open(path, nil)
	require.NoError(t, err, "Open w1")
	require.NoError(t, w1.Append(context.Background(), ports.AuditEvent{EventName: "first", OccurredAt: time.Now().UTC(), Payload: []byte(`{}`)}), "Append w1")
	require.NoError(t, w1.Close(), "Close w1")

	// Second writer opens the same file and appends another event.
	w2, err := Open(path, nil)
	require.NoError(t, err, "Open w2")
	require.NoError(t, w2.Append(context.Background(), ports.AuditEvent{EventName: "second", OccurredAt: time.Now().UTC(), Payload: []byte(`{}`)}), "Append w2")
	require.NoError(t, w2.Close(), "Close w2")

	// Both events must be present.
	f, err := os.Open(path)
	require.NoError(t, err, "open for read")
	defer f.Close()

	var names []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev ports.AuditEvent
		if !assert.NoError(t, json.Unmarshal(scanner.Bytes(), &ev), "invalid JSON") {
			continue
		}
		names = append(names, ev.EventName)
	}
	assert.Equal(t, []string{"first", "second"}, names)
}

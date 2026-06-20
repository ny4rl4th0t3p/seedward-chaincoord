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

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

func TestIntegration_AuditLog_FileCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	w, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected audit log file to exist after Open: %v", err)
	}
}

func TestIntegration_AuditLog_AppendAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	w, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ev := ports.AuditEvent{
		EventName:  "ValidatorApproved",
		OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload:    []byte(`{"launch_id":"abc"}`),
	}
	if err := w.Append(context.Background(), ev); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open for read: %v", err)
	}
	defer f.Close()

	var lines []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Errorf("line is not valid JSON: %v — %s", err, scanner.Bytes())
			continue
		}
		lines = append(lines, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["event_name"] != "ValidatorApproved" {
		t.Errorf("unexpected event_name: %v", lines[0]["event_name"])
	}
}

func TestIntegration_AuditLog_ConcurrentAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	w, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
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
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}

	// Verify all lines were written and are valid JSON (no interleaving).
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open for read: %v", err)
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
		if err := json.Unmarshal(scanner.Bytes(), &map[string]any{}); err != nil {
			t.Errorf("line %d is not valid JSON: %v", count, err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != goroutines {
		t.Errorf("expected %d lines, got %d", goroutines, count)
	}
}

func TestIntegration_AuditLog_AppendOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	// First writer appends one event.
	w1, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open w1: %v", err)
	}
	if err := w1.Append(context.Background(), ports.AuditEvent{EventName: "first", OccurredAt: time.Now().UTC(), Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("Append w1: %v", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("Close w1: %v", err)
	}

	// Second writer opens the same file and appends another event.
	w2, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open w2: %v", err)
	}
	if err := w2.Append(context.Background(), ports.AuditEvent{EventName: "second", OccurredAt: time.Now().UTC(), Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("Append w2: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("Close w2: %v", err)
	}

	// Both events must be present.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open for read: %v", err)
	}
	defer f.Close()

	var names []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev ports.AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Errorf("invalid JSON: %v", err)
			continue
		}
		names = append(names, ev.EventName)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 events, got %d", len(names))
	}
	if names[0] != "first" || names[1] != "second" {
		t.Errorf("unexpected event names: %v", names)
	}
}

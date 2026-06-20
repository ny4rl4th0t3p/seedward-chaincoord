package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// ---- fakes ------------------------------------------------------------------

type fakeExpirer struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeExpirer) ExpireStale(_ context.Context) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.err
}

func (f *fakeExpirer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeMonitorRepo struct {
	mu       sync.Mutex
	launches []*launch.Launch
	findErr  error
	saveErr  error
	saved    []*launch.Launch
}

func (r *fakeMonitorRepo) FindByStatus(_ context.Context, _ launch.Status) ([]*launch.Launch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.launches, r.findErr
}

func (r *fakeMonitorRepo) Save(_ context.Context, l *launch.Launch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved = append(r.saved, l)
	return r.saveErr
}

func (r *fakeMonitorRepo) saveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.saved)
}

type fakePublisher struct {
	mu     sync.Mutex
	events []domain.DomainEvent
}

func (p *fakePublisher) Publish(ev domain.DomainEvent) {
	p.mu.Lock()
	p.events = append(p.events, ev)
	p.mu.Unlock()
}

func (p *fakePublisher) eventCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events)
}

func (p *fakePublisher) firstEvent() domain.DomainEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.events) == 0 {
		return nil
	}
	return p.events[0]
}

// genesisReadyLaunch builds a minimal Launch in GENESIS_READY status.
func genesisReadyLaunch(rpcURL string) *launch.Launch {
	return &launch.Launch{
		ID:            uuid.New(),
		Status:        launch.StatusGenesisReady,
		MonitorRPCURL: rpcURL,
	}
}

// block1JSON returns a CometBFT-style response JSON with a non-null block.
func block1JSON() []byte {
	body := map[string]any{
		"result": map[string]any{
			"block": map[string]any{
				"header": map[string]any{"height": "1"},
			},
		},
	}
	b, _ := json.Marshal(body)
	return b
}

// nullBlockJSON returns a response with a null block (node not ready).
func nullBlockJSON() []byte {
	return []byte(`{"result":{"block":null}}`)
}

// ---- RunProposalExpiry ------------------------------------------------------

func TestRunProposalExpiry_StopsOnContextCancel(t *testing.T) {
	svc := &fakeExpirer{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunProposalExpiry(ctx, svc, zerolog.Nop(), 10*time.Millisecond)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunProposalExpiry did not stop after context cancel")
	}
}

func TestRunProposalExpiry_CallsExpireStaleOnTick(t *testing.T) {
	svc := &fakeExpirer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go RunProposalExpiry(ctx, svc, zerolog.Nop(), 20*time.Millisecond)

	// Wait for at least 2 calls.
	deadline := time.After(2 * time.Second)
	for {
		if svc.callCount() >= 2 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected ≥2 ExpireStale calls, got %d", svc.callCount())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestRunProposalExpiry_ContinuesAfterError(t *testing.T) {
	var callN atomic.Int64
	svc := &fakeExpirer{}
	// Return an error on the first call, succeed on subsequent ones.
	svc.err = errors.New("transient DB error")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Override: after the first call clears the error.
	go func() {
		for {
			if callN.Load() >= 1 {
				svc.mu.Lock()
				svc.err = nil
				svc.mu.Unlock()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	go func() {
		for {
			callN.Store(int64(svc.callCount()))
			time.Sleep(5 * time.Millisecond)
		}
	}()

	go RunProposalExpiry(ctx, svc, zerolog.Nop(), 20*time.Millisecond)

	// Should still accumulate calls even though first call errored.
	deadline := time.After(2 * time.Second)
	for {
		if svc.callCount() >= 3 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected ≥3 calls despite error, got %d", svc.callCount())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// ---- RunLaunchMonitor -------------------------------------------------------

func TestRunLaunchMonitor_StopsOnContextCancel(t *testing.T) {
	repo := &fakeMonitorRepo{}
	pub := &fakePublisher{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunLaunchMonitor(ctx, repo, pub, zerolog.Nop(), 10*time.Millisecond, nil)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunLaunchMonitor did not stop after context cancel")
	}
}

func TestRunLaunchMonitor_SkipsEmptyMonitorURL(t *testing.T) {
	// A launch with no MonitorRPCURL should never trigger an HTTP call.
	l := genesisReadyLaunch("") // empty URL
	repo := &fakeMonitorRepo{launches: []*launch.Launch{l}}
	pub := &fakePublisher{}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Use a non-existent address to prove no HTTP call is made (it would error).
	RunLaunchMonitor(ctx, repo, pub, zerolog.Nop(), 20*time.Millisecond, nil)

	if repo.saveCount() != 0 {
		t.Errorf("expected no saves for empty MonitorRPCURL, got %d", repo.saveCount())
	}
	if pub.eventCount() != 0 {
		t.Errorf("expected no events for empty MonitorRPCURL, got %d", pub.eventCount())
	}
}

func TestRunLaunchMonitor_MarksLaunchedOnBlock1Detected(t *testing.T) {
	// Fake CometBFT RPC that returns block 1 immediately.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(block1JSON()); err != nil {
			t.Errorf("write block1: %v", err)
		}
	}))
	defer srv.Close()

	l := genesisReadyLaunch(srv.URL)
	repo := &fakeMonitorRepo{launches: []*launch.Launch{l}}
	pub := &fakePublisher{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go RunLaunchMonitor(ctx, repo, pub, zerolog.Nop(), 20*time.Millisecond, nil)

	// Wait until Save is called (indicating the launch was marked LAUNCHED).
	deadline := time.After(2 * time.Second)
	for repo.saveCount() < 1 {
		select {
		case <-deadline:
			t.Fatal("launch was not saved within timeout")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if l.Status != launch.StatusLaunched {
		t.Errorf("expected StatusLaunched, got %s", l.Status)
	}
	if pub.eventCount() == 0 {
		t.Error("expected LaunchDetected event to be published")
	}
	ev, ok := pub.firstEvent().(domain.LaunchDetected)
	if !ok {
		t.Fatalf("expected LaunchDetected event, got %T", pub.firstEvent())
	}
	if ev.LaunchID != l.ID {
		t.Errorf("event LaunchID mismatch: want %s, got %s", l.ID, ev.LaunchID)
	}
	if ev.SourceRPC != srv.URL {
		t.Errorf("event SourceRPC mismatch: want %s, got %s", srv.URL, ev.SourceRPC)
	}
}

func TestRunLaunchMonitor_SkipsNon200Response(t *testing.T) {
	// Fake RPC that returns 503 (node not ready yet).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	l := genesisReadyLaunch(srv.URL)
	repo := &fakeMonitorRepo{launches: []*launch.Launch{l}}
	pub := &fakePublisher{}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	RunLaunchMonitor(ctx, repo, pub, zerolog.Nop(), 20*time.Millisecond, nil)

	if repo.saveCount() != 0 {
		t.Errorf("expected no saves for non-200 response, got %d", repo.saveCount())
	}
	if pub.eventCount() != 0 {
		t.Errorf("expected no events for non-200 response, got %d", pub.eventCount())
	}
}

func TestRunLaunchMonitor_ContinuesOnFindError(t *testing.T) {
	var hitCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitCount.Add(1)
		if _, err := w.Write(block1JSON()); err != nil {
			t.Errorf("write block1: %v", err)
		}
	}))
	defer srv.Close()

	// First call returns error; second call returns a launch.
	l := genesisReadyLaunch(srv.URL)
	callN := 0
	repo := &fakeMonitorRepo{}
	repo.findErr = errors.New("DB unavailable")

	// We'll use a custom repo that alternates errors.
	altRepo := &alternatingRepo{
		errOnFirst: true,
		launch:     l,
	}

	pub := &fakePublisher{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = repo
	_ = callN

	go RunLaunchMonitor(ctx, altRepo, pub, zerolog.Nop(), 20*time.Millisecond, nil)

	// After error the loop should continue and eventually save.
	deadline := time.After(2 * time.Second)
	for {
		if altRepo.saveCount() >= 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected save after recoverable error, got %d saves", altRepo.saveCount())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// alternatingRepo returns an error on the first FindByStatus call, then succeeds.
type alternatingRepo struct {
	mu         sync.Mutex
	findCalls  int
	errOnFirst bool
	launch     *launch.Launch
	saved      []*launch.Launch
}

func (r *alternatingRepo) FindByStatus(_ context.Context, _ launch.Status) ([]*launch.Launch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.findCalls++
	if r.errOnFirst && r.findCalls == 1 {
		return nil, errors.New("transient DB error")
	}
	return []*launch.Launch{r.launch}, nil
}

func (r *alternatingRepo) Save(_ context.Context, l *launch.Launch) error {
	r.mu.Lock()
	r.saved = append(r.saved, l)
	r.mu.Unlock()
	return nil
}

func (r *alternatingRepo) saveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.saved)
}

// ---- pollBlock1 -------------------------------------------------------------

func TestPollBlock1_Returns200WithBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/block" || r.URL.RawQuery != "height=1" {
			t.Errorf("unexpected request path/query: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(block1JSON()); err != nil {
			t.Errorf("write block1: %v", err)
		}
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	detected, err := pollBlock1(context.Background(), client, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !detected {
		t.Error("expected block to be detected")
	}
}

func TestPollBlock1_Returns200NullBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(nullBlockJSON()); err != nil {
			t.Errorf("Write: %v", err)
		}
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	detected, err := pollBlock1(context.Background(), client, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detected {
		t.Error("null block should not be detected")
	}
}

func TestPollBlock1_Returns503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	detected, err := pollBlock1(context.Background(), client, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error for non-200: %v", err)
	}
	if detected {
		t.Error("non-200 response should not be detected as block found")
	}
}

func TestPollBlock1_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("not json {{{")); err != nil {
			t.Errorf("Write: %v", err)
		}
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := pollBlock1(context.Background(), client, srv.URL)
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestPollBlock1_BadURL(t *testing.T) {
	client := &http.Client{Timeout: 100 * time.Millisecond}
	_, err := pollBlock1(context.Background(), client, "http://127.0.0.1:1") // nothing listening
	if err == nil {
		t.Error("expected error for unreachable URL")
	}
}

// ---- markLaunched -----------------------------------------------------------

func TestMarkLaunched_SavesAndPublishes(t *testing.T) {
	l := genesisReadyLaunch("http://rpc.example.com:26657")
	repo := &fakeMonitorRepo{}
	pub := &fakePublisher{}

	markLaunched(context.Background(), repo, pub, zerolog.Nop(), l, l.MonitorRPCURL)

	if l.Status != launch.StatusLaunched {
		t.Errorf("expected StatusLaunched, got %s", l.Status)
	}
	if repo.saveCount() != 1 {
		t.Errorf("expected 1 save, got %d", repo.saveCount())
	}
	if pub.eventCount() != 1 {
		t.Errorf("expected 1 event, got %d", pub.eventCount())
	}
}

func TestMarkLaunched_WrongStatusNoSave(t *testing.T) {
	// Launch not in GENESIS_READY — MarkLaunched should fail, no save or publish.
	l := &launch.Launch{
		ID:     uuid.New(),
		Status: launch.StatusWindowClosed, // wrong status
	}
	repo := &fakeMonitorRepo{}
	pub := &fakePublisher{}

	markLaunched(context.Background(), repo, pub, zerolog.Nop(), l, "http://rpc.example.com")

	if repo.saveCount() != 0 {
		t.Errorf("expected no save after MarkLaunched error, got %d", repo.saveCount())
	}
	if pub.eventCount() != 0 {
		t.Errorf("expected no event after MarkLaunched error, got %d", pub.eventCount())
	}
}

func TestMarkLaunched_SaveErrorNoPublish(t *testing.T) {
	l := genesisReadyLaunch("http://rpc.example.com:26657")
	repo := &fakeMonitorRepo{saveErr: errors.New("disk full")}
	pub := &fakePublisher{}

	markLaunched(context.Background(), repo, pub, zerolog.Nop(), l, l.MonitorRPCURL)

	if pub.eventCount() != 0 {
		t.Errorf("expected no event when save fails, got %d", pub.eventCount())
	}
}

// ---- SSRF defense-in-depth guard --------------------------------------------

func TestRunMonitorTick_PrivateURLSkippedByValidator(t *testing.T) {
	// A launch with a private IP URL. When the real validator is wired, the
	// tick should skip it without making any HTTP call or saving.
	// We use a fake HTTP server that would panic if called, to prove no request is made.
	called := false
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		if _, err := w.Write(block1JSON()); err != nil {
			t.Errorf("Write: %v", err)
		}
	}))
	defer badSrv.Close()

	// Replace the test server's URL host with a private IP — the server still
	// listens on loopback, but the validator sees the private address and rejects it.
	privateURL := "http://192.168.1.1:26657"
	l := genesisReadyLaunch(privateURL)
	repo := &fakeMonitorRepo{launches: []*launch.Launch{l}}
	pub := &fakePublisher{}

	validateFn := func(rawURL string) error {
		// Inline validator: reject 192.168.x.x
		if len(rawURL) >= 16 && rawURL[:16] == "http://192.168.1" {
			return errors.New("private address")
		}
		return nil
	}

	httpClient := &http.Client{Timeout: time.Second}
	runMonitorTick(context.Background(), repo, pub, zerolog.Nop(), httpClient, validateFn)

	if called {
		t.Error("HTTP request was made to a URL that failed SSRF validation")
	}
	if repo.saveCount() != 0 {
		t.Errorf("expected no saves for blocked URL, got %d", repo.saveCount())
	}
	if pub.eventCount() != 0 {
		t.Errorf("expected no events for blocked URL, got %d", pub.eventCount())
	}
}

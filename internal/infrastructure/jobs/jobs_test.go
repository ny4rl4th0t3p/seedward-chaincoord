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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
const testChainID = "testchain-1"

func genesisReadyLaunch(rpcURL string) *launch.Launch {
	return &launch.Launch{
		ID:            uuid.New(),
		Status:        launch.StatusGenesisReady,
		MonitorRPCURL: rpcURL,
		Record:        launch.ChainRecord{ChainID: testChainID},
	}
}

// block1JSON returns a CometBFT-style response JSON with a non-null block.
func block1JSON() []byte {
	body := map[string]any{
		"result": map[string]any{
			"block": map[string]any{
				"header": map[string]any{"chain_id": testChainID, "height": "1"},
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

// craftedBlockServer returns a test RPC server that answers /block?height=1 with a block
// carrying the given header chain_id and height — used to exercise the monitor's identity check.
func craftedBlockServer(t *testing.T, chainID, height string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b, _ := json.Marshal(map[string]any{
			"result": map[string]any{
				"block": map[string]any{
					"header": map[string]any{"chain_id": chainID, "height": height},
				},
			},
		})
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunMonitorTick_VerifiesChainIDAndHeight(t *testing.T) {
	tests := []struct {
		name         string
		chainID      string
		height       string
		wantLaunched bool
	}{
		{"matching block launches", testChainID, "1", true},
		{"wrong chain_id does not launch", "otherchain-1", "1", false},
		{"wrong height does not launch", testChainID, "2", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := craftedBlockServer(t, tc.chainID, tc.height)
			l := genesisReadyLaunch(srv.URL) // launch ChainID = testChainID
			repo := &fakeMonitorRepo{launches: []*launch.Launch{l}}
			pub := &fakePublisher{}

			runMonitorTick(context.Background(), repo, pub, zerolog.Nop(), &http.Client{Timeout: time.Second}, nil)

			if tc.wantLaunched {
				assert.Equal(t, launch.StatusLaunched, l.Status)
				assert.Equal(t, 1, repo.saveCount())
				assert.Equal(t, 1, pub.eventCount())
			} else {
				assert.Equal(t, launch.StatusGenesisReady, l.Status, "must not flip to LAUNCHED")
				assert.Zero(t, repo.saveCount())
				assert.Zero(t, pub.eventCount())
			}
		})
	}
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
		require.Fail(t, "RunProposalExpiry did not stop after context cancel")
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
			require.Failf(t, "too few ExpireStale calls", "expected ≥2, got %d", svc.callCount())
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
			require.Failf(t, "too few calls despite error", "expected ≥3, got %d", svc.callCount())
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
		require.Fail(t, "RunLaunchMonitor did not stop after context cancel")
	}
}

func TestRunLaunchMonitor_SkipsEmptyMonitorURL(t *testing.T) {
	// A launch with no MonitorRPCURL should never trigger an HTTP call.
	l := genesisReadyLaunch("") // empty URL
	repo := &fakeMonitorRepo{launches: []*launch.Launch{l}}
	pub := &fakePublisher{}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Empty MonitorRPCURL is skipped by the tick before any HTTP call — no address is dialed.
	RunLaunchMonitor(ctx, repo, pub, zerolog.Nop(), 20*time.Millisecond, nil)

	assert.Zero(t, repo.saveCount(), "expected no saves for empty MonitorRPCURL")
	assert.Zero(t, pub.eventCount(), "expected no events for empty MonitorRPCURL")
}

func TestRunLaunchMonitor_MarksLaunchedOnBlock1Detected(t *testing.T) {
	// Fake CometBFT RPC that returns block 1 immediately.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write(block1JSON())
		assert.NoError(t, err, "write block1")
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
			require.Fail(t, "launch was not saved within timeout")
		case <-time.After(5 * time.Millisecond):
		}
	}

	assert.Equal(t, launch.StatusLaunched, l.Status)
	require.NotZero(t, pub.eventCount(), "expected LaunchDetected event to be published")
	ev, ok := pub.firstEvent().(domain.LaunchDetected)
	require.True(t, ok, "expected LaunchDetected event, got %T", pub.firstEvent())
	assert.Equal(t, l.ID, ev.LaunchID, "event LaunchID mismatch")
	assert.Equal(t, srv.URL, ev.SourceRPC, "event SourceRPC mismatch")
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

	assert.Zero(t, repo.saveCount(), "expected no saves for non-200 response")
	assert.Zero(t, pub.eventCount(), "expected no events for non-200 response")
}

func TestRunLaunchMonitor_ContinuesOnFindError(t *testing.T) {
	var hitCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitCount.Add(1)
		_, err := w.Write(block1JSON())
		assert.NoError(t, err, "write block1")
	}))
	defer srv.Close()

	// altRepo errors on the first FindByStatus call, then returns a launch — exercising the
	// monitor's continue-on-error path.
	l := genesisReadyLaunch(srv.URL)
	altRepo := &alternatingRepo{
		errOnFirst: true,
		launch:     l,
	}

	pub := &fakePublisher{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go RunLaunchMonitor(ctx, altRepo, pub, zerolog.Nop(), 20*time.Millisecond, nil)

	// After error the loop should continue and eventually save.
	deadline := time.After(2 * time.Second)
	for {
		if altRepo.saveCount() >= 1 {
			return
		}
		select {
		case <-deadline:
			require.Failf(t, "no save after recoverable error", "got %d saves", altRepo.saveCount())
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
		assert.Equal(t, "/block", r.URL.Path, "unexpected request path")
		assert.Equal(t, "height=1", r.URL.RawQuery, "unexpected request query")
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write(block1JSON())
		assert.NoError(t, err, "write block1")
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	detected, err := pollBlock1(context.Background(), client, srv.URL, testChainID)
	require.NoError(t, err)
	assert.True(t, detected, "expected block to be detected")
}

func TestPollBlock1_Returns200NullBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write(nullBlockJSON())
		assert.NoError(t, err, "write null block")
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	detected, err := pollBlock1(context.Background(), client, srv.URL, testChainID)
	require.NoError(t, err)
	assert.False(t, detected, "null block should not be detected")
}

func TestPollBlock1_Returns503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	detected, err := pollBlock1(context.Background(), client, srv.URL, testChainID)
	require.NoError(t, err, "unexpected error for non-200")
	assert.False(t, detected, "non-200 response should not be detected as block found")
}

func TestPollBlock1_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("not json {{{"))
		assert.NoError(t, err, "write invalid json")
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := pollBlock1(context.Background(), client, srv.URL, testChainID)
	assert.Error(t, err, "expected error for invalid JSON response")
}

func TestPollBlock1_BadURL(t *testing.T) {
	client := &http.Client{Timeout: 100 * time.Millisecond}
	_, err := pollBlock1(context.Background(), client, "http://127.0.0.1:1", testChainID) // nothing listening
	assert.Error(t, err, "expected error for unreachable URL")
}

// ---- markLaunched -----------------------------------------------------------

func TestMarkLaunched_SavesAndPublishes(t *testing.T) {
	l := genesisReadyLaunch("http://rpc.example.com:26657")
	repo := &fakeMonitorRepo{}
	pub := &fakePublisher{}

	markLaunched(context.Background(), repo, pub, zerolog.Nop(), l, l.MonitorRPCURL)

	assert.Equal(t, launch.StatusLaunched, l.Status)
	assert.Equal(t, 1, repo.saveCount(), "expected 1 save")
	assert.Equal(t, 1, pub.eventCount(), "expected 1 event")
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

	assert.Zero(t, repo.saveCount(), "expected no save after MarkLaunched error")
	assert.Zero(t, pub.eventCount(), "expected no event after MarkLaunched error")
}

func TestMarkLaunched_SaveErrorNoPublish(t *testing.T) {
	l := genesisReadyLaunch("http://rpc.example.com:26657")
	repo := &fakeMonitorRepo{saveErr: errors.New("disk full")}
	pub := &fakePublisher{}

	markLaunched(context.Background(), repo, pub, zerolog.Nop(), l, l.MonitorRPCURL)

	assert.Zero(t, pub.eventCount(), "expected no event when save fails")
}

// ---- SSRF defense-in-depth guard --------------------------------------------

func TestRunMonitorTick_PrivateURLSkippedByValidator(t *testing.T) {
	// The launch points AT badSrv, but the SSRF validator rejects that URL — so the monitor must
	// skip the dial entirely. badSrv flips `called` if it is ever hit; the whole point is that it
	// never is, which proves validation gates the dial itself (not merely the save/publish that
	// follow). Pointing at an unrelated hardcoded IP would make `called` un-flippable and inert.
	called := false
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, err := w.Write(block1JSON())
		assert.NoError(t, err, "write block1")
	}))
	defer badSrv.Close()

	l := genesisReadyLaunch(badSrv.URL)
	repo := &fakeMonitorRepo{launches: []*launch.Launch{l}}
	pub := &fakePublisher{}

	// Reject the launch's URL, as the real SSRF validator would for a private/reserved target.
	validateFn := func(rawURL string) error {
		if rawURL == badSrv.URL {
			return errors.New("blocked by SSRF policy")
		}
		return nil
	}

	httpClient := &http.Client{Timeout: time.Second}
	runMonitorTick(context.Background(), repo, pub, zerolog.Nop(), httpClient, validateFn)

	assert.False(t, called, "HTTP request was made to a URL that failed SSRF validation")
	assert.Zero(t, repo.saveCount(), "expected no saves for blocked URL")
	assert.Zero(t, pub.eventCount(), "expected no events for blocked URL")
}

package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/sse"
)

// The api handler tests are package api_test (black-box); this file is package api so it can reach the
// unexported sseHeartbeatInterval / Server internals / operatorAddrKey. So it defines its own minimal
// fixtures rather than sharing the api_test harness.

const heartbeatMember = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"

// stubLaunchRepo is a minimal ports.LaunchRepository — only FindByID is exercised (by GetLaunch).
type stubLaunchRepo struct{ l *launch.Launch }

func (r *stubLaunchRepo) FindByID(_ context.Context, id uuid.UUID) (*launch.Launch, error) {
	if r.l != nil && r.l.ID == id {
		return r.l, nil
	}
	return nil, ports.ErrNotFound
}
func (*stubLaunchRepo) Save(context.Context, *launch.Launch) error { return nil }
func (*stubLaunchRepo) FindAll(context.Context, string, int, int) ([]*launch.Launch, int, error) {
	return nil, 0, nil
}
func (*stubLaunchRepo) FindByChainID(context.Context, string) (*launch.Launch, error) {
	return nil, ports.ErrNotFound
}
func (*stubLaunchRepo) FindByStatus(context.Context, launch.Status) ([]*launch.Launch, error) {
	return nil, nil
}

func heartbeatLaunch(t *testing.T) *launch.Launch {
	t.Helper()
	rec := launch.ChainRecord{
		ChainID: "testchain-1", ChainName: "Test Chain", Bech32Prefix: "cosmos",
		BinaryName: "testchaind", BinaryVersion: "v1.0.0", Denom: "utest",
		GentxDeadline: time.Now().Add(24 * time.Hour), MinValidatorCount: 4,
	}
	addr := launch.MustNewAccountID(heartbeatMember)
	committee := launch.Committee{
		ID: uuid.New(), ThresholdM: 1, TotalN: 1, LeadAddress: addr,
		Members:   []launch.CommitteeMember{{Address: addr, Moniker: "lead"}},
		CreatedAt: time.Now(),
	}
	l, err := launch.New(uuid.New(), rec, launch.LaunchTypeTestnet, committee)
	require.NoError(t, err)
	return l
}

// flushRecorder is a thread-safe http.ResponseWriter + Flusher for streaming-handler tests: the
// handler writes from a goroutine while the test polls the buffer.
type flushRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
	hdr http.Header
}

func (f *flushRecorder) Header() http.Header {
	if f.hdr == nil {
		f.hdr = http.Header{}
	}
	return f.hdr
}

func (f *flushRecorder) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.Write(p)
}

func (*flushRecorder) WriteHeader(int) {}
func (*flushRecorder) Flush()          {}

func (f *flushRecorder) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.String()
}

func TestHandleEvents_Heartbeat(t *testing.T) {
	// An idle stream (no domain events published) must still emit a ':' heartbeat comment frame, so
	// proxies don't reap the connection and a dead subscriber is detected (a write to a closed socket
	// errors → the handler returns → Unsubscribe frees the slot).
	prev := sseHeartbeatInterval
	sseHeartbeatInterval = 15 * time.Millisecond
	defer func() { sseHeartbeatInterval = prev }()

	l := heartbeatLaunch(t) // heartbeatMember is the committee lead → visible to it
	// GetLaunch only reads launchRepo + the launch's own visibility, so the other deps can be nil.
	launchSvc := services.NewLaunchService(&stubLaunchRepo{l: l}, nil, nil, nil, nil, nil, nil, nil, nil)
	s := &Server{launches: launchSvc, sseBroker: sse.New()}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", l.ID.String())
	base := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	base = context.WithValue(base, operatorAddrKey, heartbeatMember)
	ctx, cancel := context.WithCancel(base)
	defer cancel()

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/launch/"+l.ID.String()+"/events", http.NoBody)
	w := &flushRecorder{}

	done := make(chan struct{})
	go func() {
		s.handleEvents(w, r)
		close(done)
	}()

	require.Eventually(t, func() bool { return strings.Contains(w.body(), ": ping") },
		time.Second, 5*time.Millisecond, "an idle stream must emit a heartbeat comment")

	cancel()
	<-done
}

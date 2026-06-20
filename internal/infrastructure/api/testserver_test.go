package api_test

// Test infrastructure for API handler tests.
// Builds a real Server wired with in-memory fake port implementations so that
// HTTP-layer concerns (parsing, auth middleware, status-code mapping) can be
// tested without any I/O. Business logic is already covered by the service tests.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/ny4rl4th0t3p/seedward-libs/gentxvalidate"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/config"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/api"
)

// ---- test constants ---------------------------------------------------------

const (
	testAddr1 = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"
	testAddr2 = "cosmos1yy3zxfp9ycnjs2f29vkz6t30xqcnyve5j4ep6w"
	testAddr3 = "cosmos1g9pyx3z9ger5sj22fdxy6nj02pg4y5657yq8y0"
	testSig   = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
)

func mustAddr(s string) launch.OperatorAddress { return launch.MustNewOperatorAddress(s) }
func mustSig() launch.Signature {
	s, err := launch.NewSignature(testSig)
	if err != nil {
		panic(err)
	}
	return s
}

// testLaunch returns a PUBLIC DRAFT launch with a 2-of-3 committee.
func testLaunch() *launch.Launch {
	maxComm, _ := launch.NewCommissionRate("0.20")
	maxCommChange, _ := launch.NewCommissionRate("0.01")
	rec := launch.ChainRecord{
		ChainID:                 "testchain-1",
		ChainName:               "Test Chain",
		Bech32Prefix:            "cosmos",
		BinaryName:              "testchaind",
		BinaryVersion:           "v1.0.0",
		BinarySHA256:            "abc123",
		Denom:                   "utest",
		MinSelfDelegation:       "1000000",
		MaxCommissionRate:       maxComm,
		MaxCommissionChangeRate: maxCommChange,
		GentxDeadline:           time.Now().Add(24 * time.Hour).UTC(),
		ApplicationWindowOpen:   time.Now().UTC(),
		MinValidatorCount:       1,
	}
	committee := launch.Committee{
		ID: uuid.New(),
		Members: []launch.CommitteeMember{
			{Address: mustAddr(testAddr1), Moniker: "coord-1", PubKeyB64: "AAAA"},
			{Address: mustAddr(testAddr2), Moniker: "coord-2", PubKeyB64: "AAAA"},
			{Address: mustAddr(testAddr3), Moniker: "coord-3", PubKeyB64: "AAAA"},
		},
		ThresholdM:        2,
		TotalN:            3,
		LeadAddress:       mustAddr(testAddr1),
		CreationSignature: mustSig(),
		CreatedAt:         time.Now().UTC(),
	}
	l, err := launch.New(uuid.New(), rec, launch.LaunchTypeTestnet, launch.VisibilityPublic, committee)
	if err != nil {
		panic(err)
	}
	return l
}

// ---- harness ----------------------------------------------------------------

// harness holds the test server and the fake stores so tests can pre-seed data.
type harness struct {
	server     *api.Server
	sessions   *thinSessionStore
	challenges *thinChallengeStore
	launches   *thinLaunchRepo
	joinReqs   *thinJoinRequestRepo
	proposals  *thinProposalRepo
	readiness  *thinReadinessRepo
	genesis    *thinGenesisStore
	auditLog   *thinAuditLogReader
	allowlist  *thinCoordinatorAllowlist
}

// thinGentxValidator is an all-passing ports.GentxValidator for HTTP-layer
// tests. It echoes the gentx's embedded consensus pubkey so the consensus-pubkey
// uniqueness check still distinguishes submissions; real invariant coverage is
// in the service and gentxvalidate tests.
type thinGentxValidator struct{}

func (thinGentxValidator) Validate(gentxJSON []byte, _ gentxvalidate.Params) ports.GentxValidationOutcome {
	return ports.GentxValidationOutcome{
		Results:            []gentxvalidate.Result{{Invariant: gentxvalidate.InvWellFormed, OK: true}},
		ConsensusPubKeyB64: gentxConsensusPubKeyForTest(gentxJSON),
	}
}

// gentxConsensusPubKeyForTest reads body.messages[0].pubkey.key (already base64,
// matching the stored format) from a gentx JSON.
func gentxConsensusPubKeyForTest(gentxJSON []byte) string {
	var doc struct {
		Body struct {
			Messages []struct {
				PubKey struct {
					Key string `json:"key"`
				} `json:"pubkey"`
			} `json:"messages"`
		} `json:"body"`
	}
	if err := json.Unmarshal(gentxJSON, &doc); err != nil || len(doc.Body.Messages) == 0 {
		return ""
	}
	return doc.Body.Messages[0].PubKey.Key
}

// newHarness builds a Server wired with all-fake port implementations.
func newHarness(t *testing.T) *harness {
	t.Helper()

	sessions := &thinSessionStore{data: make(map[string]string)}
	challenges := &thinChallengeStore{data: make(map[string]string)}
	nonces := &thinNonceStore{seen: make(map[string]struct{})}
	verifier := &thinVerifier{}
	launchRepo := &thinLaunchRepo{data: make(map[uuid.UUID]*launch.Launch)}
	genesisStore := newThinGenesisStore(t)
	auditLogReader := &thinAuditLogReader{}
	auditLogWriter := &thinAuditLogWriter{}
	events := &thinEventPublisher{}
	tx := &thinTransactor{}
	jrRepo := &thinJoinRequestRepo{data: make(map[uuid.UUID]*joinrequest.JoinRequest)}
	propRepo := &thinProposalRepo{data: make(map[uuid.UUID]*proposal.Proposal)}
	readinessRepo := &thinReadinessRepo{data: make(map[uuid.UUID]*launch.ReadinessConfirmation)}

	authSvc := services.NewAuthService(challenges, sessions, nonces, verifier)
	launchSvc := services.NewLaunchService(launchRepo, jrRepo, readinessRepo, genesisStore, events, auditLogWriter)
	jrSvc := services.NewJoinRequestService(launchRepo, jrRepo, nonces, verifier, thinGentxValidator{})
	propSvc := services.NewProposalService(launchRepo, jrRepo, propRepo, readinessRepo, nonces, verifier, events, auditLogWriter, tx)
	readinessSvc := services.NewReadinessService(launchRepo, jrRepo, readinessRepo, nonces, verifier)

	allowlistRepo := &thinCoordinatorAllowlist{data: make(map[string]*ports.CoordinatorAllowlistEntry)}
	srv := api.NewServer(zerolog.Nop(), "", nil, authSvc, launchSvc, jrSvc, propSvc, readinessSvc,
		sessions, &thinSSEBroker{}, genesisStore, auditLogReader, nil, allowlistRepo,
		config.LaunchPolicyOpen, false, 32<<20, false)

	return &harness{
		server:     srv,
		sessions:   sessions,
		challenges: challenges,
		launches:   launchRepo,
		joinReqs:   jrRepo,
		proposals:  propRepo,
		readiness:  readinessRepo,
		genesis:    genesisStore,
		auditLog:   auditLogReader,
		allowlist:  allowlistRepo,
	}
}

// newHarnessWithAdmins is like newHarness but registers the given addresses as admins.
func newHarnessWithAdmins(t *testing.T, adminAddrs ...string) *harness {
	t.Helper()
	return newHarnessConfig(t, adminAddrs, config.LaunchPolicyOpen)
}

// newHarnessWithPolicy builds a harness with a specific launch policy and optional admins.
func newHarnessWithPolicy(t *testing.T, policy string, adminAddrs ...string) *harness {
	t.Helper()
	return newHarnessConfig(t, adminAddrs, policy)
}

func newHarnessConfig(t *testing.T, adminAddrs []string, launchPolicy string) *harness {
	t.Helper()

	sessions := &thinSessionStore{data: make(map[string]string)}
	challenges := &thinChallengeStore{data: make(map[string]string)}
	nonces := &thinNonceStore{seen: make(map[string]struct{})}
	verifier := &thinVerifier{}
	launchRepo := &thinLaunchRepo{data: make(map[uuid.UUID]*launch.Launch)}
	genesisStore := newThinGenesisStore(t)
	auditLogReader := &thinAuditLogReader{}
	auditLogWriter := &thinAuditLogWriter{}
	events := &thinEventPublisher{}
	tx := &thinTransactor{}
	jrRepo := &thinJoinRequestRepo{data: make(map[uuid.UUID]*joinrequest.JoinRequest)}
	propRepo := &thinProposalRepo{data: make(map[uuid.UUID]*proposal.Proposal)}
	readinessRepo := &thinReadinessRepo{data: make(map[uuid.UUID]*launch.ReadinessConfirmation)}

	authSvc := services.NewAuthService(challenges, sessions, nonces, verifier)
	launchSvc := services.NewLaunchService(launchRepo, jrRepo, readinessRepo, genesisStore, events, auditLogWriter)
	jrSvc := services.NewJoinRequestService(launchRepo, jrRepo, nonces, verifier, thinGentxValidator{})
	propSvc := services.NewProposalService(launchRepo, jrRepo, propRepo, readinessRepo, nonces, verifier, events, auditLogWriter, tx)
	readinessSvc := services.NewReadinessService(launchRepo, jrRepo, readinessRepo, nonces, verifier)

	allowlistRepo := &thinCoordinatorAllowlist{data: make(map[string]*ports.CoordinatorAllowlistEntry)}
	srv := api.NewServer(zerolog.Nop(), "", adminAddrs, authSvc, launchSvc, jrSvc, propSvc, readinessSvc,
		sessions, &thinSSEBroker{}, genesisStore, auditLogReader, nil, allowlistRepo, launchPolicy, false, 32<<20, false)

	return &harness{
		server:     srv,
		sessions:   sessions,
		challenges: challenges,
		launches:   launchRepo,
		joinReqs:   jrRepo,
		proposals:  propRepo,
		readiness:  readinessRepo,
		genesis:    genesisStore,
		auditLog:   auditLogReader,
		allowlist:  allowlistRepo,
	}
}

// newHarnessRateLimitDisabled builds a harness with all per-IP rate limiters disabled.
func newHarnessRateLimitDisabled(t *testing.T) *harness {
	t.Helper()

	sessions := &thinSessionStore{data: make(map[string]string)}
	challenges := &thinChallengeStore{data: make(map[string]string)}
	nonces := &thinNonceStore{seen: make(map[string]struct{})}
	verifier := &thinVerifier{}
	launchRepo := &thinLaunchRepo{data: make(map[uuid.UUID]*launch.Launch)}
	genesisStore := newThinGenesisStore(t)
	auditLogReader := &thinAuditLogReader{}
	auditLogWriter := &thinAuditLogWriter{}
	events := &thinEventPublisher{}
	tx := &thinTransactor{}
	jrRepo := &thinJoinRequestRepo{data: make(map[uuid.UUID]*joinrequest.JoinRequest)}
	propRepo := &thinProposalRepo{data: make(map[uuid.UUID]*proposal.Proposal)}
	readinessRepo := &thinReadinessRepo{data: make(map[uuid.UUID]*launch.ReadinessConfirmation)}

	authSvc := services.NewAuthService(challenges, sessions, nonces, verifier)
	launchSvc := services.NewLaunchService(launchRepo, jrRepo, readinessRepo, genesisStore, events, auditLogWriter)
	jrSvc := services.NewJoinRequestService(launchRepo, jrRepo, nonces, verifier, thinGentxValidator{})
	propSvc := services.NewProposalService(launchRepo, jrRepo, propRepo, readinessRepo, nonces, verifier, events, auditLogWriter, tx)
	readinessSvc := services.NewReadinessService(launchRepo, jrRepo, readinessRepo, nonces, verifier)

	allowlistRepo := &thinCoordinatorAllowlist{data: make(map[string]*ports.CoordinatorAllowlistEntry)}
	srv := api.NewServer(zerolog.Nop(), "", nil, authSvc, launchSvc, jrSvc, propSvc, readinessSvc,
		sessions, &thinSSEBroker{}, genesisStore, auditLogReader, nil, allowlistRepo,
		config.LaunchPolicyOpen, false, 32<<20, true)

	return &harness{
		server:     srv,
		sessions:   sessions,
		challenges: challenges,
		launches:   launchRepo,
		joinReqs:   jrRepo,
		proposals:  propRepo,
		readiness:  readinessRepo,
		genesis:    genesisStore,
		auditLog:   auditLogReader,
		allowlist:  allowlistRepo,
	}
}

// newHarnessHostMode builds a harness with genesis host mode enabled and the given max bytes.
func newHarnessHostMode(t *testing.T, maxBytes int64) *harness {
	t.Helper()

	sessions := &thinSessionStore{data: make(map[string]string)}
	challenges := &thinChallengeStore{data: make(map[string]string)}
	nonces := &thinNonceStore{seen: make(map[string]struct{})}
	verifier := &thinVerifier{}
	launchRepo := &thinLaunchRepo{data: make(map[uuid.UUID]*launch.Launch)}
	genesisStore := newThinGenesisStore(t)
	auditLogReader := &thinAuditLogReader{}
	auditLogWriter := &thinAuditLogWriter{}
	events := &thinEventPublisher{}
	tx := &thinTransactor{}
	jrRepo := &thinJoinRequestRepo{data: make(map[uuid.UUID]*joinrequest.JoinRequest)}
	propRepo := &thinProposalRepo{data: make(map[uuid.UUID]*proposal.Proposal)}
	readinessRepo := &thinReadinessRepo{data: make(map[uuid.UUID]*launch.ReadinessConfirmation)}

	authSvc := services.NewAuthService(challenges, sessions, nonces, verifier)
	launchSvc := services.NewLaunchService(launchRepo, jrRepo, readinessRepo, genesisStore, events, auditLogWriter)
	jrSvc := services.NewJoinRequestService(launchRepo, jrRepo, nonces, verifier, thinGentxValidator{})
	propSvc := services.NewProposalService(launchRepo, jrRepo, propRepo, readinessRepo, nonces, verifier, events, auditLogWriter, tx)
	readinessSvc := services.NewReadinessService(launchRepo, jrRepo, readinessRepo, nonces, verifier)

	allowlistRepo := &thinCoordinatorAllowlist{data: make(map[string]*ports.CoordinatorAllowlistEntry)}
	srv := api.NewServer(zerolog.Nop(), "", nil, authSvc, launchSvc, jrSvc, propSvc, readinessSvc,
		sessions, &thinSSEBroker{}, genesisStore, auditLogReader, nil, allowlistRepo,
		config.LaunchPolicyOpen, true, maxBytes, false)

	return &harness{
		server:     srv,
		sessions:   sessions,
		challenges: challenges,
		launches:   launchRepo,
		joinReqs:   jrRepo,
		proposals:  propRepo,
		readiness:  readinessRepo,
		genesis:    genesisStore,
		auditLog:   auditLogReader,
		allowlist:  allowlistRepo,
	}
}

// do sends a request to the test server and returns the recorded response.
func (h *harness) do(method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	var b *bytes.Reader
	if body != nil {
		b = bytes.NewReader(body)
	} else {
		b = bytes.NewReader(nil)
	}
	r := httptest.NewRequestWithContext(context.Background(), method, path, b)
	r.RemoteAddr = "127.0.0.1:9999"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(w, r)
	return w
}

// doJSON is a convenience wrapper for requests with JSON bodies.
func (h *harness) doJSON(method, path string, body []byte) *httptest.ResponseRecorder {
	return h.do(method, path, body, map[string]string{"Content-Type": "application/json"})
}

// doAuthJSON sends an authenticated JSON request.
func (h *harness) doAuthJSON(method, path string, body []byte, token string) *httptest.ResponseRecorder {
	return h.do(method, path, body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + token,
	})
}

// seedSession creates a session token for the given address.
func (h *harness) seedSession(addr string) string {
	token := "tok-" + addr
	h.sessions.data[token] = addr
	return token
}

// assertStatus fails the test if the response status code differs from want.
func assertStatus(t *testing.T, got *httptest.ResponseRecorder, want int) {
	t.Helper()
	if got.Code != want {
		t.Errorf("status: want %d, got %d (body: %s)", want, got.Code, got.Body.String())
	}
}

// ---- thin fakes -------------------------------------------------------------

// thinSessionStore maps token → operator address.
type thinSessionStore struct {
	data        map[string]string
	issueErr    error
	validateErr error
}

func (s *thinSessionStore) Issue(_ context.Context, addr string) (string, error) {
	if s.issueErr != nil {
		return "", s.issueErr
	}
	tok := "tok-" + addr
	s.data[tok] = addr
	return tok, nil
}

func (s *thinSessionStore) Validate(_ context.Context, token string) (string, error) {
	if s.validateErr != nil {
		return "", s.validateErr
	}
	addr, ok := s.data[token]
	if !ok {
		return "", ports.ErrUnauthorized
	}
	return addr, nil
}

func (s *thinSessionStore) Revoke(_ context.Context, token string) error {
	delete(s.data, token)
	return nil
}

func (s *thinSessionStore) RevokeAllForOperator(_ context.Context, addr string) error {
	for tok, a := range s.data {
		if a == addr {
			delete(s.data, tok)
		}
	}
	return nil
}

func (s *thinSessionStore) ParseClaims(token string) (string, time.Time, error) {
	addr, ok := s.data[token]
	if !ok {
		return "", time.Time{}, ports.ErrUnauthorized
	}
	return addr, time.Now().Add(time.Hour), nil
}

// thinChallengeStore is an in-memory challenge store.
type thinChallengeStore struct {
	data     map[string]string
	issueErr error
}

func (c *thinChallengeStore) Issue(_ context.Context, addr string) (string, error) {
	if c.issueErr != nil {
		return "", c.issueErr
	}
	ch := "challenge-for-" + addr
	c.data[addr] = ch
	return ch, nil
}

func (c *thinChallengeStore) Consume(_ context.Context, addr string) (string, error) {
	ch, ok := c.data[addr]
	if !ok {
		return "", ports.ErrNotFound
	}
	delete(c.data, addr)
	return ch, nil
}

// thinNonceStore accepts all nonces once.
type thinNonceStore struct {
	seen map[string]struct{}
}

func (n *thinNonceStore) Consume(_ context.Context, addr, nonce string) error {
	key := addr + ":" + nonce
	if _, ok := n.seen[key]; ok {
		return ports.ErrConflict
	}
	n.seen[key] = struct{}{}
	return nil
}

// thinVerifier always succeeds.
type thinVerifier struct{ err error }

func (v *thinVerifier) Verify(_, _ string, _, _ []byte) error { return v.err }

// thinLaunchRepo is an in-memory launch repository.
type thinLaunchRepo struct {
	data    map[uuid.UUID]*launch.Launch
	saveErr error
	findErr error
}

func (r *thinLaunchRepo) Save(_ context.Context, l *launch.Launch) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.data[l.ID] = l
	return nil
}

func (r *thinLaunchRepo) FindByID(_ context.Context, id uuid.UUID) (*launch.Launch, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	l, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return l, nil
}

func (r *thinLaunchRepo) FindAll(_ context.Context, _ string, _, _ int) ([]*launch.Launch, int, error) {
	var out []*launch.Launch
	for _, l := range r.data {
		out = append(out, l)
	}
	return out, len(out), nil
}

func (r *thinLaunchRepo) FindByChainID(_ context.Context, chainID string) (*launch.Launch, error) {
	for _, l := range r.data {
		if l.Record.ChainID == chainID {
			return l, nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *thinLaunchRepo) FindByStatus(_ context.Context, status launch.Status) ([]*launch.Launch, error) {
	var out []*launch.Launch
	for _, l := range r.data {
		if l.Status == status {
			out = append(out, l)
		}
	}
	return out, nil
}

// thinJoinRequestRepo is an in-memory join request repository.
type thinJoinRequestRepo struct {
	data map[uuid.UUID]*joinrequest.JoinRequest
}

func (r *thinJoinRequestRepo) Save(_ context.Context, jr *joinrequest.JoinRequest) error {
	r.data[jr.ID] = jr
	return nil
}

func (r *thinJoinRequestRepo) FindByID(_ context.Context, id uuid.UUID) (*joinrequest.JoinRequest, error) {
	jr, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return jr, nil
}

func (r *thinJoinRequestRepo) FindByLaunch(_ context.Context, launchID uuid.UUID, status *joinrequest.Status, _, _ int) ([]*joinrequest.JoinRequest, int, error) {
	var out []*joinrequest.JoinRequest
	for _, jr := range r.data {
		if jr.LaunchID == launchID && (status == nil || jr.Status == *status) {
			out = append(out, jr)
		}
	}
	return out, len(out), nil
}

func (r *thinJoinRequestRepo) FindByOperator(_ context.Context, launchID uuid.UUID, addr string) (*joinrequest.JoinRequest, error) {
	for _, jr := range r.data {
		if jr.LaunchID == launchID && jr.OperatorAddress.String() == addr {
			return jr, nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *thinJoinRequestRepo) FindApprovedByLaunch(_ context.Context, launchID uuid.UUID) ([]*joinrequest.JoinRequest, error) {
	var out []*joinrequest.JoinRequest
	for _, jr := range r.data {
		if jr.LaunchID == launchID && jr.Status == joinrequest.StatusApproved {
			out = append(out, jr)
		}
	}
	return out, nil
}

func (*thinJoinRequestRepo) CountByOperator(_ context.Context, _ uuid.UUID, _ string) (int, error) {
	return 0, nil
}

func (*thinJoinRequestRepo) CountByConsensusPubKey(_ context.Context, _ uuid.UUID, _ string) (int, error) {
	return 0, nil
}

// thinProposalRepo is an in-memory proposal repository.
type thinProposalRepo struct {
	data map[uuid.UUID]*proposal.Proposal
}

func (r *thinProposalRepo) Save(_ context.Context, p *proposal.Proposal) error {
	r.data[p.ID] = p
	return nil
}

func (r *thinProposalRepo) FindByID(_ context.Context, id uuid.UUID) (*proposal.Proposal, error) {
	p, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return p, nil
}

func (r *thinProposalRepo) FindByLaunch(_ context.Context, launchID uuid.UUID, _, _ int) ([]*proposal.Proposal, int, error) {
	var out []*proposal.Proposal
	for _, p := range r.data {
		if p.LaunchID == launchID {
			out = append(out, p)
		}
	}
	return out, len(out), nil
}

func (r *thinProposalRepo) FindPending(_ context.Context) ([]*proposal.Proposal, error) {
	var out []*proposal.Proposal
	for _, p := range r.data {
		if p.Status == proposal.StatusPendingSignatures {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *thinProposalRepo) ExpireAllPending(_ context.Context, launchID uuid.UUID) error {
	for _, p := range r.data {
		if p.LaunchID == launchID && p.Status == proposal.StatusPendingSignatures {
			p.Status = proposal.StatusExpired
		}
	}
	return nil
}

// thinReadinessRepo is an in-memory readiness repository.
type thinReadinessRepo struct {
	data map[uuid.UUID]*launch.ReadinessConfirmation
}

func (r *thinReadinessRepo) Save(_ context.Context, rc *launch.ReadinessConfirmation) error {
	r.data[rc.ID] = rc
	return nil
}

func (r *thinReadinessRepo) FindByLaunch(_ context.Context, launchID uuid.UUID) ([]*launch.ReadinessConfirmation, error) {
	var out []*launch.ReadinessConfirmation
	for _, rc := range r.data {
		if rc.LaunchID == launchID {
			out = append(out, rc)
		}
	}
	return out, nil
}

func (r *thinReadinessRepo) FindByOperator(_ context.Context, launchID uuid.UUID, addr string) (*launch.ReadinessConfirmation, error) {
	for _, rc := range r.data {
		if rc.LaunchID == launchID && rc.OperatorAddress.String() == addr {
			return rc, nil
		}
	}
	return nil, ports.ErrNotFound
}

func (*thinReadinessRepo) InvalidateByLaunch(_ context.Context, _ uuid.UUID) error { return nil }

// thinGenesisStore is an in-memory genesis store for handler tests.
// SaveInitial / SaveFinal write bytes to a temp dir so GetInitialRef / GetFinalRef
// can return a real LocalPath that the GET handler can open and stream.
// Tests can also inject explicit GenesisRefs (e.g. for Option A redirect testing)
// by setting initialRef / finalRef directly.
type thinGenesisStore struct {
	dir        string // temp dir owned by the test
	initial    map[string][]byte
	final      map[string][]byte
	initialRef map[string]*ports.GenesisRef // explicit override (Option A)
	finalRef   map[string]*ports.GenesisRef // explicit override (Option A)
}

func newThinGenesisStore(t *testing.T) *thinGenesisStore {
	t.Helper()
	return &thinGenesisStore{
		dir:        t.TempDir(),
		initial:    make(map[string][]byte),
		final:      make(map[string][]byte),
		initialRef: make(map[string]*ports.GenesisRef),
		finalRef:   make(map[string]*ports.GenesisRef),
	}
}

func (g *thinGenesisStore) SaveInitial(_ context.Context, id string, data []byte) error {
	g.initial[id] = data
	return nil
}

func (g *thinGenesisStore) SaveFinal(_ context.Context, id string, data []byte) error {
	g.final[id] = data
	return nil
}

func (g *thinGenesisStore) SaveInitialRef(_ context.Context, id, url, sha256 string) error {
	g.initialRef[id] = &ports.GenesisRef{ExternalURL: url, SHA256: sha256}
	return nil
}

func (g *thinGenesisStore) SaveFinalRef(_ context.Context, id, url, sha256 string) error {
	g.finalRef[id] = &ports.GenesisRef{ExternalURL: url, SHA256: sha256}
	return nil
}

func (g *thinGenesisStore) GetInitialRef(_ context.Context, id string) (*ports.GenesisRef, error) {
	if ref, ok := g.initialRef[id]; ok {
		return ref, nil
	}
	data, ok := g.initial[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	path := filepath.Join(g.dir, id+"-initial.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("thinGenesisStore: %w", err)
	}
	return &ports.GenesisRef{LocalPath: path}, nil
}

func (g *thinGenesisStore) GetFinalRef(_ context.Context, id string) (*ports.GenesisRef, error) {
	if ref, ok := g.finalRef[id]; ok {
		return ref, nil
	}
	data, ok := g.final[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	path := filepath.Join(g.dir, id+"-final.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("thinGenesisStore: %w", err)
	}
	return &ports.GenesisRef{LocalPath: path}, nil
}

// thinAuditLogWriter discards events.
type thinAuditLogWriter struct{}

func (*thinAuditLogWriter) Append(_ context.Context, _ ports.AuditEvent) error { return nil }

// thinAuditLogReader returns an empty list.
type thinAuditLogReader struct{}

func (*thinAuditLogReader) ReadForLaunch(_ context.Context, _ string) ([]ports.AuditEvent, error) {
	return nil, nil
}

// thinEventPublisher discards events.
type thinEventPublisher struct{}

func (*thinEventPublisher) Publish(_ domain.DomainEvent) {}

// thinTransactor runs fn directly (no real transaction).
type thinTransactor struct{}

func (*thinTransactor) InTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// thinSSEBroker satisfies the sseSubscriber interface used inside the api package.
type thinSSEBroker struct{}

func (*thinSSEBroker) Subscribe(_ string) chan domain.DomainEvent {
	ch := make(chan domain.DomainEvent, 1)
	close(ch) // immediately closed so SSE handler returns
	return ch
}

func (*thinSSEBroker) Unsubscribe(_ string, _ chan domain.DomainEvent) {}

// ---- assertion helpers ------------------------------------------------------

func assertStatusCode(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Errorf("want status %d, got %d (body: %s)", want, w.Code, w.Body.String())
	}
}

func assertContentTypeJSON(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("want Content-Type application/json, got %q", ct)
	}
}

// ---- response helpers -------------------------------------------------------

func jsonBody(s string) []byte { return []byte(s) }

func nowTS() string { return time.Now().UTC().Format(time.RFC3339) }

// Suppress "unused" linter if nowTS is only used in auth tests.
var _ = http.MethodGet

// thinCoordinatorAllowlist is an in-memory CoordinatorAllowlistRepository.
type thinCoordinatorAllowlist struct {
	data map[string]*ports.CoordinatorAllowlistEntry
}

func (r *thinCoordinatorAllowlist) Add(_ context.Context, address, addedBy string) error {
	if _, exists := r.data[address]; !exists {
		r.data[address] = &ports.CoordinatorAllowlistEntry{
			Address: address,
			AddedBy: addedBy,
			AddedAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
	return nil
}

func (r *thinCoordinatorAllowlist) Remove(_ context.Context, address string) error {
	if _, ok := r.data[address]; !ok {
		return ports.ErrNotFound
	}
	delete(r.data, address)
	return nil
}

func (r *thinCoordinatorAllowlist) Contains(_ context.Context, address string) (bool, error) {
	_, ok := r.data[address]
	return ok, nil
}

func (r *thinCoordinatorAllowlist) List(_ context.Context, page, perPage int) ([]*ports.CoordinatorAllowlistEntry, int, error) {
	all := make([]*ports.CoordinatorAllowlistEntry, 0, len(r.data))
	for _, e := range r.data {
		all = append(all, e)
	}
	total := len(all)
	start := (page - 1) * perPage
	if start >= total {
		return []*ports.CoordinatorAllowlistEntry{}, total, nil
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

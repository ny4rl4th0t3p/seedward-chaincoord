package services

// Fake implementations of all port interfaces for unit testing.
// These live exclusively in _test.go files and are never compiled into production code.

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

// ---- test constants -------------------------------------------------------

const (
	// Real checksummed bech32 cosmos addresses for testing.
	testAddr1 = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"
	testAddr2 = "cosmos1yy3zxfp9ycnjs2f29vkz6t30xqcnyve5j4ep6w"
	testAddr3 = "cosmos1g9pyx3z9ger5sj22fdxy6nj02pg4y5657yq8y0"

	// 64-byte all-zeros Ed25519 signature, base64-encoded (88 chars).
	testSig = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
)

// nowTS returns the current UTC time as an RFC3339 string — valid for validateTimestamp.
func nowTS() string { return time.Now().UTC().Format(time.RFC3339) }

// expiredTS returns a timestamp 10 minutes in the past — rejected by validateTimestamp.
func expiredTS() string { return time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339) }

func mustAddr(s string) launch.OperatorAddress { return launch.MustNewOperatorAddress(s) }

func mustSig() launch.Signature {
	s, err := launch.NewSignature(testSig)
	if err != nil {
		panic(err)
	}
	return s
}

// ---- domain fixtures -------------------------------------------------------

func testChainRecord() launch.ChainRecord {
	maxComm, _ := launch.NewCommissionRate("0.20")
	maxCommChange, _ := launch.NewCommissionRate("0.01")
	return launch.ChainRecord{
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
}

func testCommittee(threshold, total int) launch.Committee {
	allAddrs := []string{testAddr1, testAddr2, testAddr3}
	members := make([]launch.CommitteeMember, total)
	for i := range total {
		members[i] = launch.CommitteeMember{
			Address:   mustAddr(allAddrs[i]),
			Moniker:   "coord",
			PubKeyB64: "AAAA",
		}
	}
	return launch.Committee{
		ID:                uuid.New(),
		Members:           members,
		ThresholdM:        threshold,
		TotalN:            total,
		LeadAddress:       mustAddr(testAddr1),
		CreationSignature: mustSig(),
		CreatedAt:         time.Now().UTC(),
	}
}

// testLaunch returns a DRAFT launch with a 2-of-3 committee.
func testLaunch() *launch.Launch {
	l, err := launch.New(uuid.New(), testChainRecord(), launch.LaunchTypeTestnet, testCommittee(2, 3))
	if err != nil {
		panic(err)
	}
	return l
}

// test1of1Launch returns a DRAFT launch with a 1-of-1 committee and MinValidatorCount=0.
func test1of1Launch() *launch.Launch {
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
		},
		ThresholdM:        1,
		TotalN:            1,
		LeadAddress:       mustAddr(testAddr1),
		CreationSignature: mustSig(),
		CreatedAt:         time.Now().UTC(),
	}
	l, err := launch.New(uuid.New(), rec, launch.LaunchTypeTestnet, committee)
	if err != nil {
		panic(err)
	}
	return l
}

// test1of3Launch returns a DRAFT launch with a 1-of-3 committee (threshold 1 → auto-execute).
func test1of3Launch() *launch.Launch {
	l, err := launch.New(uuid.New(), testChainRecord(), launch.LaunchTypeTestnet, testCommittee(1, 3))
	if err != nil {
		panic(err)
	}
	return l
}

// testProposal returns a PENDING proposal with a CLOSE_WINDOW action.
func testProposal(launchID uuid.UUID) *proposal.Proposal {
	payload := []byte(`{}`)
	p, err := proposal.New(
		uuid.New(), launchID,
		proposal.ActionCloseApplicationWindow, payload,
		mustAddr(testAddr1), mustSig(),
		48*time.Hour, time.Now(),
	)
	if err != nil {
		panic(err)
	}
	return p
}

// ---- fakeChallengeStore ---------------------------------------------------

type fakeChallengeStore struct {
	data       map[string]string
	issueErr   error
	consumeErr error
}

func newFakeChallengeStore() *fakeChallengeStore {
	return &fakeChallengeStore{data: make(map[string]string)}
}

func (f *fakeChallengeStore) Issue(_ context.Context, addr string) (string, error) {
	if f.issueErr != nil {
		return "", f.issueErr
	}
	ch := "challenge-" + addr
	f.data[addr] = ch
	return ch, nil
}

func (f *fakeChallengeStore) Consume(_ context.Context, addr string) (string, error) {
	if f.consumeErr != nil {
		return "", f.consumeErr
	}
	ch, ok := f.data[addr]
	if !ok {
		return "", ports.ErrNotFound
	}
	delete(f.data, addr)
	return ch, nil
}

// ---- fakeSessionStore -----------------------------------------------------

type fakeSessionStore struct {
	data        map[string]string // token → addr
	issueErr    error
	validateErr error
	revokeErr   error
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{data: make(map[string]string)}
}

func (f *fakeSessionStore) Issue(_ context.Context, addr string) (string, error) {
	if f.issueErr != nil {
		return "", f.issueErr
	}
	token := "token-" + addr
	f.data[token] = addr
	return token, nil
}

func (f *fakeSessionStore) Validate(_ context.Context, token string) (string, error) {
	if f.validateErr != nil {
		return "", f.validateErr
	}
	addr, ok := f.data[token]
	if !ok {
		return "", ports.ErrUnauthorized
	}
	return addr, nil
}

func (f *fakeSessionStore) Revoke(_ context.Context, token string) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	delete(f.data, token)
	return nil
}

func (f *fakeSessionStore) RevokeAllForOperator(_ context.Context, addr string) error {
	for tok, a := range f.data {
		if a == addr {
			delete(f.data, tok)
		}
	}
	return nil
}

func (f *fakeSessionStore) ParseClaims(token string) (string, time.Time, error) {
	addr, ok := f.data[token]
	if !ok {
		return "", time.Time{}, ports.ErrUnauthorized
	}
	return addr, time.Now().Add(time.Hour), nil
}

// ---- fakeNonceStore -------------------------------------------------------

type fakeNonceStore struct {
	seen       map[string]struct{}
	consumeErr error
}

func newFakeNonceStore() *fakeNonceStore {
	return &fakeNonceStore{seen: make(map[string]struct{})}
}

func (f *fakeNonceStore) Consume(_ context.Context, addr, nonce string) error {
	if f.consumeErr != nil {
		return f.consumeErr
	}
	key := addr + ":" + nonce
	if _, ok := f.seen[key]; ok {
		return ports.ErrConflict
	}
	f.seen[key] = struct{}{}
	return nil
}

// ---- fakeVerifier ---------------------------------------------------------

type fakeVerifier struct{ err error }

func (f *fakeVerifier) Verify(_, _ string, _, _ []byte) error { return f.err }

// ---- fakeLaunchRepo -------------------------------------------------------

type fakeLaunchRepo struct {
	data    map[uuid.UUID]*launch.Launch
	saveErr error
	findErr error
}

func newFakeLaunchRepo(launches ...*launch.Launch) *fakeLaunchRepo {
	f := &fakeLaunchRepo{data: make(map[uuid.UUID]*launch.Launch)}
	for _, l := range launches {
		f.data[l.ID] = l
	}
	return f
}

func (f *fakeLaunchRepo) Save(_ context.Context, l *launch.Launch) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.data[l.ID] = l
	return nil
}

func (f *fakeLaunchRepo) FindByID(_ context.Context, id uuid.UUID) (*launch.Launch, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	l, ok := f.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return l, nil
}

func (f *fakeLaunchRepo) FindAll(_ context.Context, _ string, _, _ int) ([]*launch.Launch, int, error) {
	var out []*launch.Launch
	for _, l := range f.data {
		out = append(out, l)
	}
	return out, len(out), nil
}

func (f *fakeLaunchRepo) FindByChainID(_ context.Context, chainID string) (*launch.Launch, error) {
	for _, l := range f.data {
		if l.Record.ChainID == chainID {
			return l, nil
		}
	}
	return nil, ports.ErrNotFound
}

func (f *fakeLaunchRepo) FindByStatus(_ context.Context, status launch.Status) ([]*launch.Launch, error) {
	var out []*launch.Launch
	for _, l := range f.data {
		if l.Status == status {
			out = append(out, l)
		}
	}
	return out, nil
}

// ---- fakeJoinRequestRepo --------------------------------------------------

type fakeJoinRequestRepo struct {
	data        map[uuid.UUID]*joinrequest.JoinRequest
	countByOp   map[string]int // "launchID:addr" → count
	saveErr     error
	findErr     error
	findByOpErr error
}

func newFakeJoinRequestRepo(jrs ...*joinrequest.JoinRequest) *fakeJoinRequestRepo {
	f := &fakeJoinRequestRepo{
		data:      make(map[uuid.UUID]*joinrequest.JoinRequest),
		countByOp: make(map[string]int),
	}
	for _, jr := range jrs {
		f.data[jr.ID] = jr
	}
	return f
}

func (f *fakeJoinRequestRepo) setCount(launchID uuid.UUID, addr string, n int) {
	f.countByOp[launchID.String()+":"+addr] = n
}

func (f *fakeJoinRequestRepo) Save(_ context.Context, jr *joinrequest.JoinRequest) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.data[jr.ID] = jr
	return nil
}

func (f *fakeJoinRequestRepo) FindByID(_ context.Context, id uuid.UUID) (*joinrequest.JoinRequest, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	jr, ok := f.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return jr, nil
}

func (f *fakeJoinRequestRepo) FindByLaunch(_ context.Context, launchID uuid.UUID, status *joinrequest.Status, _, _ int) ([]*joinrequest.JoinRequest, int, error) {
	var out []*joinrequest.JoinRequest
	for _, jr := range f.data {
		if jr.LaunchID != launchID {
			continue
		}
		if status != nil && jr.Status != *status {
			continue
		}
		out = append(out, jr)
	}
	return out, len(out), nil
}

func (f *fakeJoinRequestRepo) FindByOperator(_ context.Context, launchID uuid.UUID, addr string) (*joinrequest.JoinRequest, error) {
	if f.findByOpErr != nil {
		return nil, f.findByOpErr
	}
	for _, jr := range f.data {
		if jr.LaunchID == launchID && jr.OperatorAddress.String() == addr {
			return jr, nil
		}
	}
	return nil, ports.ErrNotFound
}

func (f *fakeJoinRequestRepo) FindApprovedByLaunch(_ context.Context, launchID uuid.UUID) ([]*joinrequest.JoinRequest, error) {
	var out []*joinrequest.JoinRequest
	for _, jr := range f.data {
		if jr.LaunchID == launchID && jr.Status == joinrequest.StatusApproved {
			out = append(out, jr)
		}
	}
	return out, nil
}

func (f *fakeJoinRequestRepo) CountBySubmitter(_ context.Context, launchID uuid.UUID, addr string) (int, error) {
	return f.countByOp[launchID.String()+":"+addr], nil
}

func (f *fakeJoinRequestRepo) CountByConsensusPubKey(_ context.Context, launchID uuid.UUID, pubKey string) (int, error) {
	for _, jr := range f.data {
		if jr.LaunchID == launchID && jr.ConsensusPubKey == pubKey && isActiveStatus(jr.Status) {
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeJoinRequestRepo) FindActiveByValidator(_ context.Context, launchID uuid.UUID, validatorAddr string) (*joinrequest.JoinRequest, error) {
	for _, jr := range f.data {
		if jr.LaunchID == launchID && jr.OperatorAddress.String() == validatorAddr && isActiveStatus(jr.Status) {
			return jr, nil
		}
	}
	return nil, ports.ErrNotFound
}

// isActiveStatus mirrors the partial-index predicate: PENDING/APPROVED are active,
// REJECTED/EXPIRED are terminal (D4).
func isActiveStatus(s joinrequest.Status) bool {
	return s == joinrequest.StatusPending || s == joinrequest.StatusApproved
}

// ---- fakeProposalRepo -----------------------------------------------------

type fakeProposalRepo struct {
	data    map[uuid.UUID]*proposal.Proposal
	saveErr error
	findErr error
}

func newFakeProposalRepo(proposals ...*proposal.Proposal) *fakeProposalRepo {
	f := &fakeProposalRepo{data: make(map[uuid.UUID]*proposal.Proposal)}
	for _, p := range proposals {
		f.data[p.ID] = p
	}
	return f
}

func (f *fakeProposalRepo) Save(_ context.Context, p *proposal.Proposal) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.data[p.ID] = p
	return nil
}

func (f *fakeProposalRepo) FindByID(_ context.Context, id uuid.UUID) (*proposal.Proposal, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	p, ok := f.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return p, nil
}

func (f *fakeProposalRepo) FindByLaunch(_ context.Context, launchID uuid.UUID, _, _ int) ([]*proposal.Proposal, int, error) {
	var out []*proposal.Proposal
	for _, p := range f.data {
		if p.LaunchID == launchID {
			out = append(out, p)
		}
	}
	return out, len(out), nil
}

func (f *fakeProposalRepo) FindPending(_ context.Context) ([]*proposal.Proposal, error) {
	var out []*proposal.Proposal
	for _, p := range f.data {
		if p.Status == proposal.StatusPendingSignatures {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeProposalRepo) ExpireAllPending(_ context.Context, launchID uuid.UUID) error {
	for _, p := range f.data {
		if p.LaunchID == launchID && p.Status == proposal.StatusPendingSignatures {
			p.Status = proposal.StatusExpired
		}
	}
	return nil
}

// ---- fakeReadinessRepo ----------------------------------------------------

type fakeReadinessRepo struct {
	data          map[uuid.UUID]*launch.ReadinessConfirmation
	findByOpErr   error
	invalidateErr error
}

func newFakeReadinessRepo(rcs ...*launch.ReadinessConfirmation) *fakeReadinessRepo {
	f := &fakeReadinessRepo{data: make(map[uuid.UUID]*launch.ReadinessConfirmation)}
	for _, rc := range rcs {
		f.data[rc.ID] = rc
	}
	return f
}

func (f *fakeReadinessRepo) Save(_ context.Context, rc *launch.ReadinessConfirmation) error {
	f.data[rc.ID] = rc
	return nil
}

func (f *fakeReadinessRepo) FindByLaunch(_ context.Context, launchID uuid.UUID) ([]*launch.ReadinessConfirmation, error) {
	var out []*launch.ReadinessConfirmation
	for _, rc := range f.data {
		if rc.LaunchID == launchID {
			out = append(out, rc)
		}
	}
	return out, nil
}

func (f *fakeReadinessRepo) FindByOperator(_ context.Context, launchID uuid.UUID, addr string) (*launch.ReadinessConfirmation, error) {
	if f.findByOpErr != nil {
		return nil, f.findByOpErr
	}
	for _, rc := range f.data {
		if rc.LaunchID == launchID && rc.OperatorAddress.String() == addr {
			return rc, nil
		}
	}
	return nil, ports.ErrNotFound
}

func (f *fakeReadinessRepo) InvalidateByLaunch(_ context.Context, launchID uuid.UUID) error {
	if f.invalidateErr != nil {
		return f.invalidateErr
	}
	now := time.Now()
	for _, rc := range f.data {
		if rc.LaunchID == launchID {
			rc.Invalidate(now)
		}
	}
	return nil
}

// ---- fakeGenesisStore -----------------------------------------------------

type fakeGenesisStore struct {
	initial      map[string][]byte
	final        map[string][]byte
	initialRef   map[string]*ports.StoredFileRef
	finalRef     map[string]*ports.StoredFileRef
	saveInitErr  error
	saveFinalErr error
}

func newFakeGenesisStore() *fakeGenesisStore {
	return &fakeGenesisStore{
		initial:    make(map[string][]byte),
		final:      make(map[string][]byte),
		initialRef: make(map[string]*ports.StoredFileRef),
		finalRef:   make(map[string]*ports.StoredFileRef),
	}
}

func (f *fakeGenesisStore) SaveInitial(_ context.Context, launchID string, data []byte) error {
	if f.saveInitErr != nil {
		return f.saveInitErr
	}
	f.initial[launchID] = data
	return nil
}

func (f *fakeGenesisStore) SaveFinal(_ context.Context, launchID string, data []byte) error {
	if f.saveFinalErr != nil {
		return f.saveFinalErr
	}
	f.final[launchID] = data
	return nil
}

func (f *fakeGenesisStore) SaveInitialRef(_ context.Context, launchID, url, sha256 string) error {
	f.initialRef[launchID] = &ports.StoredFileRef{ExternalURL: url, SHA256: sha256}
	return nil
}

func (f *fakeGenesisStore) SaveFinalRef(_ context.Context, launchID, url, sha256 string) error {
	f.finalRef[launchID] = &ports.StoredFileRef{ExternalURL: url, SHA256: sha256}
	return nil
}

func (f *fakeGenesisStore) GetInitialRef(_ context.Context, launchID string) (*ports.StoredFileRef, error) {
	if ref, ok := f.initialRef[launchID]; ok {
		return ref, nil
	}
	if _, ok := f.initial[launchID]; ok {
		return &ports.StoredFileRef{LocalPath: "fake-initial-" + launchID}, nil
	}
	return nil, ports.ErrNotFound
}

func (f *fakeGenesisStore) GetFinalRef(_ context.Context, launchID string) (*ports.StoredFileRef, error) {
	if ref, ok := f.finalRef[launchID]; ok {
		return ref, nil
	}
	if _, ok := f.final[launchID]; ok {
		return &ports.StoredFileRef{LocalPath: "fake-final-" + launchID}, nil
	}
	return nil, ports.ErrNotFound
}

// ---- fakeAllocationStore --------------------------------------------------

type fakeAllocationStore struct {
	bytes   map[string][]byte
	refs    map[string]*ports.StoredFileRef
	saveErr error
}

func newFakeAllocationStore() *fakeAllocationStore {
	return &fakeAllocationStore{
		bytes: make(map[string][]byte),
		refs:  make(map[string]*ports.StoredFileRef),
	}
}

func (f *fakeAllocationStore) Save(_ context.Context, launchID, allocType string, data []byte) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.bytes[launchID+":"+allocType] = data
	return nil
}

func (f *fakeAllocationStore) SaveRef(_ context.Context, launchID, allocType, url, sha256 string) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.refs[launchID+":"+allocType] = &ports.StoredFileRef{ExternalURL: url, SHA256: sha256}
	return nil
}

func (f *fakeAllocationStore) GetRef(_ context.Context, launchID, allocType string) (*ports.StoredFileRef, error) {
	key := launchID + ":" + allocType
	if ref, ok := f.refs[key]; ok {
		return ref, nil
	}
	if _, ok := f.bytes[key]; ok {
		return &ports.StoredFileRef{LocalPath: "fake-alloc-" + key}, nil
	}
	return nil, ports.ErrNotFound
}

// ---- fakeAuditLogWriter ---------------------------------------------------

type fakeAuditLogWriter struct {
	events    []ports.AuditEvent
	appendErr error
}

func (f *fakeAuditLogWriter) Append(_ context.Context, ev ports.AuditEvent) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.events = append(f.events, ev)
	return nil
}

// ---- fakeEventPublisher ---------------------------------------------------

type fakeEventPublisher struct {
	events []domain.DomainEvent
}

func (f *fakeEventPublisher) Publish(ev domain.DomainEvent) {
	f.events = append(f.events, ev)
}

// ---- fakeTransactor -------------------------------------------------------

type fakeTransactor struct{}

func (*fakeTransactor) InTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

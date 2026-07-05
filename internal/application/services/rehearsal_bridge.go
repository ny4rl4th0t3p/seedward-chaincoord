package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// RehearsalLeasedError is returned by ClaimRehearsalRun when a different runner already holds an
// unexpired lease on the run. It maps to 409 and carries the current holder for the response body.
type RehearsalLeasedError struct {
	RunnerID       string
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
}

func (e *RehearsalLeasedError) Error() string {
	return fmt.Sprintf("rehearsal run already claimed by %q until %s",
		e.RunnerID, e.LeaseExpiresAt.Format(time.RFC3339))
}

func (*RehearsalLeasedError) Unwrap() error { return ports.ErrConflict }

// RehearsalInput is coordd's assembled rehearsal build input for a launch (bridge contract)
// plus its input_set_hash. Only APPROVED join requests and committee-approved
// allocation files appear. Wire serialization is the API layer's concern.
type RehearsalInput struct {
	LaunchID     uuid.UUID
	AttemptID    uuid.UUID // coordd-minted attempt for (launch, input_set_hash); anti-fabrication anchor
	GeneratedAt  time.Time
	Status       launch.Status
	Chain        RehearsalChain
	Gentxs       []RehearsalGentx      // sorted by operator address
	Allocations  []RehearsalAllocation // approved host-mode files, sorted by type
	InputSetHash string
}

// RehearsalChain mirrors the chain record fields the rehearsal service needs.
type RehearsalChain struct {
	ChainID                 string
	Bech32Prefix            string
	Denom                   string
	TotalSupply             string
	MinSelfDelegation       string
	MaxCommissionRate       string
	MaxCommissionChangeRate string
	MinValidatorCount       int
	GenesisTime             *time.Time
	BinaryName              string
	BinaryVersion           string
	BinarySHA256            string
	RepoURL                 string
	RepoCommit              string
}

// RehearsalGentx is one approved join request's gentx and extracted fields.
type RehearsalGentx struct {
	OperatorAddress string
	ConsensusPubKey string
	Moniker         string
	SelfDelegation  string
	GentxJSON       json.RawMessage
}

// RehearsalAllocation is the metadata for one committee-approved allocation file. The bytes
// are NOT inlined — the daemon streams them from a per-file URL (the API layer builds it),
// so airdrop-scale files never buffer in memory.
type RehearsalAllocation struct {
	Type               string
	SHA256             string
	ApprovedByProposal string
}

// PreviewRehearsalInput assembles the rehearsal input for a launch WITHOUT minting an attempt or
// acquiring a lease — a read-only "what would be rehearsed" view (GET rehearsal-input). Its
// AttemptID is empty; a runner must ClaimRehearsalRun to obtain one. Returns ErrNotFound if the
// launch does not exist.
func (s *LaunchService) PreviewRehearsalInput(ctx context.Context, launchID uuid.UUID) (*RehearsalInput, error) {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, err
	}
	return s.assembleRehearsalInput(ctx, l)
}

// ClaimRehearsalRun is the run entry point (POST rehearsal-claim): it assembles the input, mints
// (get-or-create) the attempt for its input_set_hash, and acquires the run lease for runnerID —
// returning the input + attempt_id. If a different runner already holds an unexpired lease on the
// same input set, it returns *RehearsalLeasedError (409). The lease auto-expires after the TTL, so a
// crashed runner self-heals; a coordinator can also ResetRehearsalAttempt for an immediate override.
func (s *LaunchService) ClaimRehearsalRun(ctx context.Context, launchID uuid.UUID, runnerID string) (*RehearsalInput, error) {
	const op = "claim rehearsal run"
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, err
	}
	in, err := s.assembleRehearsalInput(ctx, l)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	attempt, err := s.attempts.GetOrCreate(ctx, l.ID, in.InputSetHash, now)
	if err != nil {
		return nil, fmt.Errorf("%s: attempt: %w", op, err)
	}
	if err := attempt.Claim(runnerID, now, now.Add(s.rehearsalLeaseTTL)); err != nil {
		if errors.Is(err, launch.ErrAttemptLeased) {
			return nil, &RehearsalLeasedError{
				RunnerID:       attempt.RunnerID,
				ClaimedAt:      derefTime(attempt.ClaimedAt),
				LeaseExpiresAt: derefTime(attempt.LeaseExpiresAt),
			}
		}
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if err := s.attempts.Save(ctx, attempt); err != nil {
		return nil, fmt.Errorf("%s: save: %w", op, err)
	}
	in.AttemptID = attempt.ID
	return in, nil
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// currentInputSetHash computes the launch's current input_set_hash without minting an attempt —
// used to decide whether an incoming result is stale.
func (s *LaunchService) currentInputSetHash(ctx context.Context, l *launch.Launch) (string, error) {
	in, err := s.assembleRehearsalInput(ctx, l)
	if err != nil {
		return "", err
	}
	return in.InputSetHash, nil
}

// assembleRehearsalInput gathers the approved input set (chain + gentxs + allocation metadata) and
// computes input_set_hash. It is pure — no attempt minting, no lease — so both the serve path and
// the staleness check can call it.
func (s *LaunchService) assembleRehearsalInput(ctx context.Context, l *launch.Launch) (*RehearsalInput, error) {
	const op = "build rehearsal input"
	launchID := l.ID

	approved, err := s.joinRequests.FindApprovedByLaunch(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("%s: gentxs: %w", op, err)
	}
	gentxs := make([]RehearsalGentx, 0, len(approved))
	for _, jr := range approved {
		gentxs = append(gentxs, RehearsalGentx{
			OperatorAddress: jr.OperatorAddress.String(),
			ConsensusPubKey: jr.ConsensusPubKey,
			Moniker:         jr.Moniker(),
			SelfDelegation:  strconv.FormatInt(jr.SelfDelegationAmount(), 10),
			GentxJSON:       jr.GentxJSON,
		})
	}
	sort.Slice(gentxs, func(i, j int) bool { return gentxs[i].OperatorAddress < gentxs[j].OperatorAddress })

	// Only metadata — no store access, no attestor distinction. The daemon streams each file
	// from its URL (built by the API layer), and the stream endpoint handles host-vs-attestor.
	allocs := make([]RehearsalAllocation, 0, len(l.AllocationFiles))
	for _, af := range l.AllocationFiles {
		if af.Status != launch.AllocationApproved {
			continue
		}
		approvedBy := ""
		if af.ApprovedByProposal != nil {
			approvedBy = af.ApprovedByProposal.String()
		}
		allocs = append(allocs, RehearsalAllocation{
			Type:               string(af.Type),
			SHA256:             af.SHA256,
			ApprovedByProposal: approvedBy,
		})
	}
	sort.Slice(allocs, func(i, j int) bool { return allocs[i].Type < allocs[j].Type })

	chain := RehearsalChain{
		ChainID:                 l.Record.ChainID,
		Bech32Prefix:            l.Record.Bech32Prefix,
		Denom:                   l.Record.Denom,
		TotalSupply:             l.Record.TotalSupply,
		MinSelfDelegation:       l.Record.MinSelfDelegation,
		MaxCommissionRate:       l.Record.MaxCommissionRate.String(),
		MaxCommissionChangeRate: l.Record.MaxCommissionChangeRate.String(),
		MinValidatorCount:       l.Record.MinValidatorCount,
		GenesisTime:             l.Record.GenesisTime,
		BinaryName:              l.Record.BinaryName,
		BinaryVersion:           l.Record.BinaryVersion,
		BinarySHA256:            l.Record.BinarySHA256,
		RepoURL:                 l.Record.RepoURL,
		RepoCommit:              l.Record.RepoCommit,
	}

	hash, err := computeInputSetHash(chain, gentxs, allocs)
	if err != nil {
		return nil, fmt.Errorf("%s: hash: %w", op, err)
	}

	return &RehearsalInput{
		LaunchID:     l.ID,
		GeneratedAt:  time.Now().UTC(),
		Status:       l.Status,
		Chain:        chain,
		Gentxs:       gentxs,
		Allocations:  allocs,
		InputSetHash: hash,
	}, nil
}

// computeInputSetHash is the staleness key (bridge contract): SHA-256 over the canonical
// JSON of {chain (all fields), gentxs[operator+consensus+gentx_sha256] sorted, files[sha256|null]}.
// status and generated_at are deliberately excluded so a result stays current across lifecycle
// transitions while the inputs are unchanged.
func computeInputSetHash(chain RehearsalChain, gentxs []RehearsalGentx, allocs []RehearsalAllocation) (string, error) {
	type hashBinary struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		SHA256     string `json:"sha256"`
		RepoURL    string `json:"repo_url"`
		RepoCommit string `json:"repo_commit"`
	}
	type hashChain struct {
		ChainID                 string     `json:"chain_id"`
		Bech32Prefix            string     `json:"bech32_prefix"`
		Denom                   string     `json:"denom"`
		TotalSupply             string     `json:"total_supply"`
		MinSelfDelegation       string     `json:"min_self_delegation"`
		MaxCommissionRate       string     `json:"max_commission_rate"`
		MaxCommissionChangeRate string     `json:"max_commission_change_rate"`
		MinValidatorCount       int        `json:"min_validator_count"`
		GenesisTime             *string    `json:"genesis_time"`
		Binary                  hashBinary `json:"binary"`
	}
	type hashGentx struct {
		OperatorAddress string `json:"operator_address"`
		ConsensusPubkey string `json:"consensus_pubkey"`
		GentxSHA256     string `json:"gentx_sha256"`
	}
	type hashFiles struct {
		Accounts *string `json:"accounts_sha256"`
		Claims   *string `json:"claims_sha256"`
		Grants   *string `json:"grants_sha256"`
		Authz    *string `json:"authz_sha256"`
		Feegrant *string `json:"feegrant_sha256"`
	}
	type hashInput struct {
		Chain  hashChain   `json:"chain"`
		Gentxs []hashGentx `json:"gentxs"`
		Files  hashFiles   `json:"files"`
	}

	entries := make([]hashGentx, len(gentxs))
	for i, g := range gentxs {
		sum := sha256.Sum256(g.GentxJSON)
		entries[i] = hashGentx{
			OperatorAddress: g.OperatorAddress,
			ConsensusPubkey: g.ConsensusPubKey,
			GentxSHA256:     hex.EncodeToString(sum[:]),
		}
	}

	var files hashFiles
	for _, a := range allocs {
		sha := a.SHA256
		switch launch.AllocationType(a.Type) {
		case launch.AllocationAccounts:
			files.Accounts = &sha
		case launch.AllocationClaims:
			files.Claims = &sha
		case launch.AllocationGrants:
			files.Grants = &sha
		case launch.AllocationAuthz:
			files.Authz = &sha
		case launch.AllocationFeegrant:
			files.Feegrant = &sha
		}
	}

	hi := hashInput{
		Chain: hashChain{
			ChainID:                 chain.ChainID,
			Bech32Prefix:            chain.Bech32Prefix,
			Denom:                   chain.Denom,
			TotalSupply:             chain.TotalSupply,
			MinSelfDelegation:       chain.MinSelfDelegation,
			MaxCommissionRate:       chain.MaxCommissionRate,
			MaxCommissionChangeRate: chain.MaxCommissionChangeRate,
			MinValidatorCount:       chain.MinValidatorCount,
			GenesisTime:             rfc3339OrNil(chain.GenesisTime),
			Binary: hashBinary{
				Name:       chain.BinaryName,
				Version:    chain.BinaryVersion,
				SHA256:     chain.BinarySHA256,
				RepoURL:    chain.RepoURL,
				RepoCommit: chain.RepoCommit,
			},
		},
		Gentxs: entries,
		Files:  files,
	}

	msg, err := canonicaljson.MarshalForSigning(hi)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(msg)
	return hex.EncodeToString(sum[:]), nil
}

// rfc3339OrNil formats a nullable time as an RFC3339 string pointer (nil stays nil).
func rfc3339OrNil(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

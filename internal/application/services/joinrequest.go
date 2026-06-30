package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-libs/gentxvalidate"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// maxJoinRequestsPerSubmitter caps submissions per submitter per launch as an
// anti-spam backstop. It counts ALL statuses (see CountBySubmitter) — every
// rejected/expired submission consumes a slot and is never refunded, so a noisy
// submitter is bounded to this many attempts regardless of how often they retry.
// Set well above realistic fleet sizes since one submitter may bring many nodes.
const maxJoinRequestsPerSubmitter = 50

// JoinRequestService handles validator join request submission and retrieval.
type JoinRequestService struct {
	launches       ports.LaunchRepository
	joinRequests   ports.JoinRequestRepository
	nonces         ports.NonceStore
	verifier       ports.SignatureVerifier
	gentxValidator ports.GentxValidator
}

func NewJoinRequestService(
	launches ports.LaunchRepository,
	joinRequests ports.JoinRequestRepository,
	nonces ports.NonceStore,
	verifier ports.SignatureVerifier,
	gentxValidator ports.GentxValidator,
) *JoinRequestService {
	return &JoinRequestService{
		launches:       launches,
		joinRequests:   joinRequests,
		nonces:         nonces,
		verifier:       verifier,
		gentxValidator: gentxValidator,
	}
}

// requiresSelfDelegationFloor reports whether the launch type enforces the
// declared minimum self-delegation (mainnet-grade launches do; plain testnets
// do not). This is the launch-type-conditional gate the domain used to apply.
func requiresSelfDelegationFloor(lt launch.LaunchType) bool {
	switch lt {
	case launch.LaunchTypeMainnet, launch.LaunchTypeIncentivizedTestnet, launch.LaunchTypePermissioned:
		return true
	case launch.LaunchTypeTestnet:
		return false
	default:
		return false
	}
}

// validateGentx runs the shared invariant set over the gentx using params built
// from the launch (the self-delegation floor applies only to launch types that
// declare one) and returns the extracted consensus pubkey and the validator's
// operator (self-delegator) account address. A failing gentx yields a
// *ports.GentxInvalidError carrying the per-invariant detail.
func (s *JoinRequestService) validateGentx(l *launch.Launch, gentxJSON json.RawMessage) (consensusPubKey, validatorAddr string, err error) {
	params := gentxvalidate.Params{
		ChainID:                 l.Record.ChainID,
		BondDenom:               l.Record.Denom,
		Bech32Prefix:            l.Record.Bech32Prefix,
		MaxCommissionRate:       l.Record.MaxCommissionRate.String(),
		MaxCommissionChangeRate: l.Record.MaxCommissionChangeRate.String(),
	}
	if requiresSelfDelegationFloor(l.LaunchType) {
		params.MinSelfDelegation = l.Record.MinSelfDelegation
	}
	outcome := s.gentxValidator.Validate(gentxJSON, params)
	if !gentxvalidate.AllOK(outcome.Results) {
		return "", "", &ports.GentxInvalidError{Results: outcome.Results}
	}
	return outcome.ConsensusPubKeyB64, outcome.ValidatorAddress, nil
}

// SubmitInput is the deserialized join request payload from the validator.
type SubmitInput struct {
	ChainID         string `json:"chain_id"`
	OperatorAddress string `json:"operator_address"`
	// PubKeyB64 is the operator's secp256k1 compressed public key (base64, 33 bytes) used to verify the
	// request signature. Distinct from the consensus key, which is extracted from the gentx.
	PubKeyB64   string          `json:"pubkey_b64"`
	GentxJSON   json.RawMessage `json:"gentx" swaggertype:"object"`
	PeerAddress string          `json:"peer_address"`
	RPCEndpoint string          `json:"rpc_endpoint"`
	Memo        string          `json:"memo"`
	Timestamp   string          `json:"timestamp"`
	Nonce       string          `json:"nonce"`
	Signature   string          `json:"signature"`
}

// verifyRequestAuth enforces replay protection, timestamp freshness, and the
// request signature over the canonical payload. It must run before any launch
// state is touched.
func (s *JoinRequestService) verifyRequestAuth(ctx context.Context, input SubmitInput) error {
	if err := s.nonces.Consume(ctx, input.OperatorAddress, input.Nonce); err != nil {
		return fmt.Errorf("submit join request: nonce rejected: %w", err)
	}
	if err := validateTimestamp(input.Timestamp); err != nil {
		return fmt.Errorf("submit join request: %w", err)
	}
	message, err := canonicaljson.MarshalForSigning(input)
	if err != nil {
		return fmt.Errorf("submit join request: signing bytes: %w", err)
	}
	sigBytes, err := decodeBase64Sig(input.Signature)
	if err != nil {
		return fmt.Errorf("submit join request: signature encoding: %w", err)
	}
	if err := s.verifier.Verify(input.OperatorAddress, input.PubKeyB64, message, sigBytes); err != nil {
		// Invalid signature is an auth failure (401); the verifier returns a bare error.
		return fmt.Errorf("submit join request: signature invalid: %w: %w", err, ports.ErrUnauthorized)
	}
	return nil
}

// parseConnectionFields validates the peer address, the optional RPC endpoint,
// and the request signature value object carried on the join request.
func parseConnectionFields(input SubmitInput) (
	peerAddr launch.PeerAddress,
	rpcEndpoint launch.RPCEndpoint,
	sig launch.Signature,
	err error,
) {
	if peerAddr, err = launch.NewPeerAddress(input.PeerAddress); err != nil {
		return peerAddr, rpcEndpoint, sig, fmt.Errorf("submit join request: peer_address: %w: %w", err, ports.ErrBadRequest)
	}
	if input.RPCEndpoint != "" {
		if rpcEndpoint, err = launch.NewRPCEndpoint(input.RPCEndpoint); err != nil {
			return peerAddr, rpcEndpoint, sig, fmt.Errorf("submit join request: rpc_endpoint: %w: %w", err, ports.ErrBadRequest)
		}
	}
	if sig, err = launch.NewSignature(input.Signature); err != nil {
		return peerAddr, rpcEndpoint, sig, fmt.Errorf("submit join request: signature value: %w: %w", err, ports.ErrBadRequest)
	}
	return peerAddr, rpcEndpoint, sig, nil
}

// supersedePending applies D4 dedup keyed on the validator identity. If the validator already
// has an ACTIVE request: an APPROVED one locks the validator (ErrConflict — revoke first); a
// PENDING one is superseded — expired in place so the incoming submission replaces it (the new
// gentx is validator-signed, so its content is self-authorized regardless of submitter).
// REJECTED/EXPIRED requests are terminal and never reach here, so they never block. Must run
// before the consensus-pubkey check so a validator re-submitting its own key is not blocked by
// its own now-expired prior request.
func (s *JoinRequestService) supersedePending(ctx context.Context, launchID uuid.UUID, validatorAddr launch.OperatorAddress) error {
	existing, err := s.joinRequests.FindActiveByValidator(ctx, launchID, validatorAddr.String())
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("submit join request: active request check: %w", err)
	}
	switch existing.Status {
	case joinrequest.StatusApproved:
		return fmt.Errorf("submit join request: %w", ports.ErrValidatorAlreadyApproved)
	case joinrequest.StatusPending:
		if err := existing.Expire(); err != nil { // EXPIRED is the terminal "superseded" state (D4)
			return fmt.Errorf("submit join request: supersede pending: %w", err)
		}
		if err := s.joinRequests.Save(ctx, existing); err != nil {
			return fmt.Errorf("submit join request: supersede save: %w", err)
		}
		return nil
	case joinrequest.StatusRejected, joinrequest.StatusExpired:
		// FindActiveByValidator returns only PENDING/APPROVED, so a terminal status here is a bug.
		return fmt.Errorf("submit join request: unexpected terminal status %q from active lookup", existing.Status)
	}
	// Unreachable for the four known statuses; guards against a future enum value.
	return fmt.Errorf("submit join request: unknown join request status %q", existing.Status)
}

// Submit validates and stores a join request from a validator.
func (s *JoinRequestService) Submit(ctx context.Context, launchID uuid.UUID, input SubmitInput) (*joinrequest.JoinRequest, error) {
	if err := s.verifyRequestAuth(ctx, input); err != nil {
		return nil, err
	}

	// Load the launch and check it's open for applications.
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("submit join request: launch: %w", err)
	}

	submitterAddr, err := launch.NewOperatorAddress(input.OperatorAddress)
	if err != nil {
		return nil, fmt.Errorf("submit join request: submitter address: %w", err)
	}

	// Pre-acceptance gentx validation (shared invariant set, authoritative server-side).
	// Runs BEFORE the allowlist check: the gentx cryptographically proves the
	// validator's operator address, and that — not the submitter — is what the
	// allowlist gates. Returns the extracted consensus pubkey + the validator
	// operator address, or a per-invariant error.
	consensusPubKey, validatorAddrStr, err := s.validateGentx(l, input.GentxJSON)
	if err != nil {
		return nil, err
	}
	validatorAddr, err := launch.NewOperatorAddress(validatorAddrStr)
	if err != nil {
		return nil, fmt.Errorf("submit join request: validator address: %w", err)
	}

	// Gate the verified validator address against the launch's join policy
	// (window-open + allowlist). D3: the allowlist controls the validator SET, so
	// it checks the gentx's validator — not the submitter, who needs only to be
	// authenticated. (Removing open-join / always-gating every launch is S4.)
	if err := l.CanValidatorApply(validatorAddr); err != nil {
		return nil, fmt.Errorf("submit join request: %w: %w", err, ports.ErrForbidden)
	}

	// Submission-window deadline: a launch-state gate, enforced here alongside
	// the WINDOW_OPEN check above (not in the JoinRequest constructor).
	if time.Now().After(l.Record.GentxDeadline) {
		return nil, fmt.Errorf("submit join request: gentx submission deadline has passed (%s): %w",
			l.Record.GentxDeadline.Format(time.RFC3339), ports.ErrBadRequest)
	}

	// Rate limit: cap submissions per submitter per launch.
	count, err := s.joinRequests.CountBySubmitter(ctx, launchID, input.OperatorAddress)
	if err != nil {
		return nil, fmt.Errorf("submit join request: count check: %w", err)
	}
	if count >= maxJoinRequestsPerSubmitter {
		return nil, fmt.Errorf("submit join request: max %d per window: %w", maxJoinRequestsPerSubmitter, ports.ErrSubmissionCapReached)
	}

	// Dedup on the validator identity (D4): supersede a stale PENDING request, or
	// reject if the validator already has a locked APPROVED one. Runs before the
	// consensus-pubkey check below so a re-submission is not blocked by the request
	// it is replacing.
	if err := s.supersedePending(ctx, launchID, validatorAddr); err != nil {
		return nil, err
	}

	peerAddr, rpcEndpoint, sig, err := parseConnectionFields(input)
	if err != nil {
		return nil, err
	}

	jr := joinrequest.New(
		uuid.New(),
		launchID,
		validatorAddr, // operator (validator), from the verified gentx
		submitterAddr, // request signer
		input.GentxJSON,
		peerAddr,
		rpcEndpoint,
		input.Memo,
		sig,
		consensusPubKey,
		time.Now(),
	)

	// No two ACTIVE requests in a launch may share a consensus key. CountByConsensusPubKey
	// counts only PENDING/APPROVED rows, so a re-submission of this validator's own key is not
	// blocked by its just-superseded request; a different active validator holding the key is.
	// The partial idx_jr_consensus_pubkey unique index is the raceless safety net.
	cpCount, err := s.joinRequests.CountByConsensusPubKey(ctx, launchID, jr.ConsensusPubKey)
	if err != nil {
		return nil, fmt.Errorf("submit join request: consensus pubkey check: %w", err)
	}
	if cpCount > 0 {
		return nil, fmt.Errorf("submit join request: %w", ports.ErrConsensusKeyAlreadyUsed)
	}

	if err := s.joinRequests.Save(ctx, jr); err != nil {
		return nil, fmt.Errorf("submit join request: save: %w", err)
	}
	return jr, nil
}

// GetByID returns a single join request. Coordinators can see any; otherwise the
// caller must be a party to the request — either the validator (OperatorAddress)
// or the submitter who signed it (SubmitterAddress), since the two may differ
// (an ops/company account may submit on a validator's behalf).
func (s *JoinRequestService) GetByID(
	ctx context.Context,
	id uuid.UUID,
	callerAddr string,
	isCoordinator bool,
) (*joinrequest.JoinRequest, error) {
	jr, err := s.joinRequests.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !isCoordinator &&
		jr.OperatorAddress.String() != callerAddr &&
		jr.SubmitterAddress.String() != callerAddr {
		return nil, ports.ErrForbidden
	}
	return jr, nil
}

// ListForLaunch returns all join requests for a launch. Coordinator only.
func (s *JoinRequestService) ListForLaunch(
	ctx context.Context,
	launchID uuid.UUID,
	status *joinrequest.Status,
	page, perPage int,
) ([]*joinrequest.JoinRequest, int, error) {
	return s.joinRequests.FindByLaunch(ctx, launchID, status, page, perPage)
}

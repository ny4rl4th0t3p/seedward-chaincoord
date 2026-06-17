package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-libs/gentxvalidate"

	"github.com/ny4rl4th0t3p/chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/chaincoord/pkg/canonicaljson"
)

const maxJoinRequestsPerOperator = 3

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
// declare one) and returns the extracted consensus pubkey. A failing gentx yields
// a *ports.GentxInvalidError carrying the per-invariant detail.
func (s *JoinRequestService) validateGentx(l *launch.Launch, gentxJSON json.RawMessage) (string, error) {
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
		return "", &ports.GentxInvalidError{Results: outcome.Results}
	}
	return outcome.ConsensusPubKeyB64, nil
}

// SubmitInput is the deserialized join request payload from the validator.
type SubmitInput struct {
	ChainID         string `json:"chain_id"`
	OperatorAddress string `json:"operator_address"`
	// PubKeyB64 is the operator's secp256k1 compressed public key (base64, 33 bytes) used to verify the
	// request signature. Distinct from the consensus key, which is extracted from the gentx.
	PubKeyB64   string          `json:"pubkey_b64"`
	GentxJSON   json.RawMessage `json:"gentx"`
	PeerAddress string          `json:"peer_address"`
	RPCEndpoint string          `json:"rpc_endpoint"`
	Memo        string          `json:"memo"`
	Timestamp   string          `json:"timestamp"`
	Nonce       string          `json:"nonce"`
	Signature   string          `json:"signature"`
}

// Submit validates and stores a join request from a validator.
func (s *JoinRequestService) Submit(ctx context.Context, launchID uuid.UUID, input SubmitInput) (*joinrequest.JoinRequest, error) {
	// Replay protection first.
	if err := s.nonces.Consume(ctx, input.OperatorAddress, input.Nonce); err != nil {
		return nil, fmt.Errorf("submit join request: nonce rejected: %w", err)
	}
	if err := validateTimestamp(input.Timestamp); err != nil {
		return nil, fmt.Errorf("submit join request: %w", err)
	}

	// Verify signature over canonical JSON of the payload.
	message, err := canonicaljson.MarshalForSigning(input)
	if err != nil {
		return nil, fmt.Errorf("submit join request: signing bytes: %w", err)
	}
	sigBytes, err := decodeBase64Sig(input.Signature)
	if err != nil {
		return nil, fmt.Errorf("submit join request: signature encoding: %w", err)
	}
	if err := s.verifier.Verify(input.OperatorAddress, input.PubKeyB64, message, sigBytes); err != nil {
		return nil, fmt.Errorf("submit join request: signature invalid: %w", err)
	}

	// Load the launch and check it's open for applications.
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("submit join request: launch: %w", err)
	}

	operatorAddr, err := launch.NewOperatorAddress(input.OperatorAddress)
	if err != nil {
		return nil, fmt.Errorf("submit join request: operator address: %w", err)
	}

	if err := l.CanValidatorApply(operatorAddr); err != nil {
		return nil, fmt.Errorf("submit join request: %w: %w", err, ports.ErrForbidden)
	}

	// Submission-window deadline: a launch-state gate, enforced here alongside
	// the WINDOW_OPEN check above (not in the JoinRequest constructor).
	if time.Now().After(l.Record.GentxDeadline) {
		return nil, fmt.Errorf("submit join request: gentx submission deadline has passed (%s): %w",
			l.Record.GentxDeadline.Format(time.RFC3339), ports.ErrBadRequest)
	}

	// Rate limit: max 3 submissions per operator per launch.
	count, err := s.joinRequests.CountByOperator(ctx, launchID, input.OperatorAddress)
	if err != nil {
		return nil, fmt.Errorf("submit join request: count check: %w", err)
	}
	if count >= maxJoinRequestsPerOperator {
		return nil, fmt.Errorf("submit join request: maximum %d submissions per validator per window", maxJoinRequestsPerOperator)
	}

	// Pre-acceptance gentx validation (shared invariant set, authoritative
	// server-side). Returns the extracted consensus pubkey or a per-invariant error.
	consensusPubKey, err := s.validateGentx(l, input.GentxJSON)
	if err != nil {
		return nil, err
	}

	peerAddr, err := launch.NewPeerAddress(input.PeerAddress)
	if err != nil {
		return nil, fmt.Errorf("submit join request: peer_address: %w: %w", err, ports.ErrBadRequest)
	}
	var rpcEndpoint launch.RPCEndpoint
	if input.RPCEndpoint != "" {
		if rpcEndpoint, err = launch.NewRPCEndpoint(input.RPCEndpoint); err != nil {
			return nil, fmt.Errorf("submit join request: rpc_endpoint: %w: %w", err, ports.ErrBadRequest)
		}
	}
	sig, err := launch.NewSignature(input.Signature)
	if err != nil {
		return nil, fmt.Errorf("submit join request: signature value: %w: %w", err, ports.ErrBadRequest)
	}

	jr := joinrequest.New(
		uuid.New(),
		launchID,
		operatorAddr,
		input.GentxJSON,
		peerAddr,
		rpcEndpoint,
		input.Memo,
		sig,
		consensusPubKey,
		time.Now(),
	)

	// Reject duplicate consensus pubkey for the same launch. The pubkey was
	// extracted by the validator above; the DB also enforces this via a UNIQUE
	// index as the raceless safety net.
	cpCount, err := s.joinRequests.CountByConsensusPubKey(ctx, launchID, jr.ConsensusPubKey)
	if err != nil {
		return nil, fmt.Errorf("submit join request: consensus pubkey check: %w", err)
	}
	if cpCount > 0 {
		return nil, fmt.Errorf("submit join request: consensus pubkey already submitted for this launch: %w", ports.ErrConflict)
	}

	if err := s.joinRequests.Save(ctx, jr); err != nil {
		return nil, fmt.Errorf("submit join request: save: %w", err)
	}
	return jr, nil
}

// GetByID returns a single join request. Coordinators can see any; validators
// can only see their own.
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
	if !isCoordinator && jr.OperatorAddress.String() != callerAddr {
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

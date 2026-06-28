package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func isNotFound(err error) bool { return errors.Is(err, ports.ErrNotFound) }

// PeerInfo holds the peer address and operator address for an approved validator.
type PeerInfo struct {
	OperatorAddress string
	PeerAddress     string
}

// ReadinessService handles validator readiness confirmations and the readiness dashboard.
type ReadinessService struct {
	launches     ports.LaunchRepository
	joinRequests ports.JoinRequestRepository
	readiness    ports.ReadinessRepository
	nonces       ports.NonceStore
	verifier     ports.SignatureVerifier
}

func NewReadinessService(
	launches ports.LaunchRepository,
	joinRequests ports.JoinRequestRepository,
	readiness ports.ReadinessRepository,
	nonces ports.NonceStore,
	verifier ports.SignatureVerifier,
) *ReadinessService {
	return &ReadinessService{
		launches:     launches,
		joinRequests: joinRequests,
		readiness:    readiness,
		nonces:       nonces,
		verifier:     verifier,
	}
}

// ConfirmInput is the payload a validator signs to confirm readiness.
type ConfirmInput struct {
	OperatorAddress string `json:"operator_address"`
	// PubKeyB64 is the operator's secp256k1 compressed public key (base64, 33 bytes) used to verify the
	// confirmation signature.
	PubKeyB64            string `json:"pubkey_b64"`
	GenesisHashConfirmed string `json:"genesis_hash_confirmed"`
	BinaryHashConfirmed  string `json:"binary_hash_confirmed"`
	Nonce                string `json:"nonce"`
	Timestamp            string `json:"timestamp"`
	Signature            string `json:"signature"`
}

// Confirm stores a validator's readiness confirmation.
func (s *ReadinessService) Confirm(ctx context.Context, launchID uuid.UUID, input ConfirmInput) (*launch.ReadinessConfirmation, error) {
	if err := s.nonces.Consume(ctx, input.OperatorAddress, input.Nonce); err != nil {
		return nil, fmt.Errorf("confirm readiness: nonce rejected: %w", err)
	}
	if err := validateTimestamp(input.Timestamp); err != nil {
		return nil, fmt.Errorf("confirm readiness: %w", err)
	}

	message, err := canonicaljson.MarshalForSigning(input)
	if err != nil {
		return nil, fmt.Errorf("confirm readiness: signing bytes: %w", err)
	}
	sigBytes, err := decodeBase64Sig(input.Signature)
	if err != nil {
		return nil, fmt.Errorf("confirm readiness: signature encoding: %w", err)
	}
	if err := s.verifier.Verify(input.OperatorAddress, input.PubKeyB64, message, sigBytes); err != nil {
		return nil, fmt.Errorf("confirm readiness: signature invalid: %w", err)
	}

	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("confirm readiness: launch: %w", err)
	}
	if l.Status != launch.StatusGenesisReady {
		return nil, fmt.Errorf("confirm readiness: launch is not in GENESIS_READY status (current: %s)", l.Status)
	}

	// Validator must have an approved join request.
	jr, err := s.joinRequests.FindByOperator(ctx, launchID, input.OperatorAddress)
	if err != nil {
		return nil, fmt.Errorf("confirm readiness: no approved join request for operator: %w", ports.ErrForbidden)
	}
	if jr.Status != joinrequest.StatusApproved {
		return nil, fmt.Errorf("confirm readiness: join request is not approved (status: %s)", jr.Status)
	}

	// The genesis hash the validator reports must match the published final genesis hash.
	if input.GenesisHashConfirmed != l.FinalGenesisSHA256 {
		return nil, fmt.Errorf("confirm readiness: genesis_hash_confirmed %q does not match published genesis hash %q",
			input.GenesisHashConfirmed, l.FinalGenesisSHA256)
	}

	// The binary hash must match the chain record, but only when the coordinator
	// published a reference hash. If BinarySHA256 is empty the check is skipped.
	if l.Record.BinarySHA256 != "" && input.BinaryHashConfirmed != l.Record.BinarySHA256 {
		return nil, fmt.Errorf("confirm readiness: binary_hash_confirmed %q does not match expected binary hash %q",
			input.BinaryHashConfirmed, l.Record.BinarySHA256)
	}

	operatorAddr, err := launch.NewOperatorAddress(input.OperatorAddress)
	if err != nil {
		return nil, fmt.Errorf("confirm readiness: operator address: %w", err)
	}
	sig, err := launch.NewSignature(input.Signature)
	if err != nil {
		return nil, fmt.Errorf("confirm readiness: signature value: %w", err)
	}

	// Spec §11.4: max 1 active confirmation per operator per genesis version.
	// If one already exists and is valid, reject the duplicate.
	existing, err := s.readiness.FindByOperator(ctx, launchID, input.OperatorAddress)
	if err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("confirm readiness: check existing: %w", err)
	}
	if err == nil && existing.IsValid() {
		return nil, fmt.Errorf("confirm readiness: a valid readiness confirmation already exists for this operator and genesis version")
	}

	rc := &launch.ReadinessConfirmation{
		ID:                   uuid.New(),
		LaunchID:             launchID,
		JoinRequestID:        jr.ID,
		OperatorAddress:      operatorAddr,
		GenesisHashConfirmed: input.GenesisHashConfirmed,
		BinaryHashConfirmed:  input.BinaryHashConfirmed,
		ConfirmedAt:          time.Now().UTC(),
		OperatorSignature:    sig,
	}

	if err := s.readiness.Save(ctx, rc); err != nil {
		return nil, fmt.Errorf("confirm readiness: save: %w", err)
	}
	return rc, nil
}

// Dashboard aggregates readiness metrics for the dashboard endpoint.
type ReadinessDashboard struct {
	TotalApproved        int
	ConfirmedReady       int
	VotingPowerConfirmed float64 // percentage of total genesis voting power
	ThresholdStatus      string  // "REACHABLE", "AT_RISK", "CONFIRMED"
	PerValidator         []ValidatorReadiness
}

type ValidatorReadiness struct {
	JoinRequestID        uuid.UUID
	OperatorAddress      string
	Moniker              string
	VotingPowerPct       float64
	IsReady              bool
	LastConfirmedAt      *time.Time
	GenesisHashConfirmed string // empty when not yet confirmed
	BinaryHashConfirmed  string // empty when not yet confirmed
}

// GetPeers returns the peer addresses of all approved validators for a launch.
func (s *ReadinessService) GetPeers(ctx context.Context, launchID uuid.UUID) ([]PeerInfo, error) {
	approved, err := s.joinRequests.FindApprovedByLaunch(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("get peers: %w", err)
	}
	out := make([]PeerInfo, len(approved))
	for i, jr := range approved {
		out[i] = PeerInfo{
			OperatorAddress: jr.OperatorAddress.String(),
			PeerAddress:     jr.PeerAddress.String(),
		}
	}
	return out, nil
}

// GetDashboard returns the readiness dashboard for a launch.
func (s *ReadinessService) GetDashboard(ctx context.Context, launchID uuid.UUID) (*ReadinessDashboard, error) {
	approved, err := s.joinRequests.FindApprovedByLaunch(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("readiness dashboard: %w", err)
	}

	confirmations, err := s.readiness.FindByLaunch(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("readiness dashboard: %w", err)
	}

	// Index valid confirmations by operator address.
	confirmed := make(map[string]*launch.ReadinessConfirmation)
	for _, rc := range confirmations {
		if rc.IsValid() {
			confirmed[rc.OperatorAddress.String()] = rc
		}
	}

	// Compute total stake and per-validator power.
	var totalStake int64
	for _, jr := range approved {
		totalStake += jr.SelfDelegationAmount()
	}

	var confirmedStake int64
	var perVal []ValidatorReadiness
	for _, jr := range approved {
		stake := jr.SelfDelegationAmount()
		pct := 0.0
		if totalStake > 0 {
			pct = float64(stake) / float64(totalStake) * 100
		}

		rc, ready := confirmed[jr.OperatorAddress.String()]
		var confirmedAt *time.Time
		var genesisHashConfirmed, binaryHashConfirmed string
		if ready {
			t := rc.ConfirmedAt
			confirmedAt = &t
			confirmedStake += stake
			genesisHashConfirmed = rc.GenesisHashConfirmed
			binaryHashConfirmed = rc.BinaryHashConfirmed
		}

		perVal = append(perVal, ValidatorReadiness{
			JoinRequestID:        jr.ID,
			OperatorAddress:      jr.OperatorAddress.String(),
			Moniker:              jr.Moniker(),
			VotingPowerPct:       pct,
			IsReady:              ready,
			LastConfirmedAt:      confirmedAt,
			GenesisHashConfirmed: genesisHashConfirmed,
			BinaryHashConfirmed:  binaryHashConfirmed,
		})
	}

	confirmedPct := 0.0
	if totalStake > 0 {
		confirmedPct = float64(confirmedStake) / float64(totalStake) * 100
	}

	// BFT consensus requires >2/3 of voting power. We use the exact rational threshold.
	const (
		bftThreshold    = 200.0 / 3.0 // 66.666...%
		atRiskBelow     = 50.0
		statusReachable = "REACHABLE"
		statusConfirmed = "CONFIRMED"
		statusAtRisk    = "AT_RISK"
	)
	threshold := statusReachable
	if confirmedPct >= bftThreshold {
		threshold = statusConfirmed
	} else if confirmedPct < atRiskBelow {
		threshold = statusAtRisk
	}

	return &ReadinessDashboard{
		TotalApproved:        len(approved),
		ConfirmedReady:       len(confirmed),
		VotingPowerConfirmed: confirmedPct,
		ThresholdStatus:      threshold,
		PerValidator:         perVal,
	}, nil
}

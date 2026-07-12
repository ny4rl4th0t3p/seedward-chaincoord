// Package jobs contains background goroutines that run for the lifetime of the
// server process.  Each job is started via a Run* function and respects context
// cancellation for clean shutdown.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// proposalExpirer is the subset of ProposalService the TTL job needs.
type proposalExpirer interface {
	ExpireStale(ctx context.Context) error
}

// launchMonitorRepo is the subset of LaunchRepository the monitor job needs.
type launchMonitorRepo interface {
	FindByStatus(ctx context.Context, status launch.Status) ([]*launch.Launch, error)
	Save(ctx context.Context, l *launch.Launch) error
}

// eventPublisher is the subset of EventPublisher the monitor job needs.
type eventPublisher interface {
	Publish(event domain.DomainEvent)
}

// RunLaunchMonitor polls CometBFT RPC endpoints for GENESIS_READY launches on a
// fixed interval and transitions them to LAUNCHED when block 1 is observed.
//
// For each GENESIS_READY launch with a non-empty MonitorRPCURL the job issues
// GET <MonitorRPCURL>/block?height=1 and, when the response carries a block at
// height 1 whose header chain_id matches the launch's ChainID, calls
// l.MarkLaunched(), saves the aggregate, and publishes a LaunchDetected event.
// The chain_id + height check keeps a wrong-chain or fabricated block from the
// content-trusted RPC from flipping the launch to LAUNCHED.
//
// The URL is re-read from the repository on every tick so that PATCH updates
// take effect without a server restart.
//
// validateURL is called as a defense-in-depth guard before each HTTP request.
// Pass nil to skip validation (e.g. in tests that use loopback test servers).
// In production, pass netutil.ValidateRPCURL.
//
// Errors are logged but do not stop the loop.
func RunLaunchMonitor(
	ctx context.Context, repo launchMonitorRepo, pub eventPublisher,
	log zerolog.Logger, interval time.Duration, validateURL func(string) error,
) {
	httpClient := &http.Client{Timeout: 5 * time.Second}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runMonitorTick(ctx, repo, pub, log, httpClient, validateURL)
		}
	}
}

func runMonitorTick(
	ctx context.Context, repo launchMonitorRepo, pub eventPublisher,
	log zerolog.Logger, httpClient *http.Client, validateURL func(string) error,
) {
	candidates, err := repo.FindByStatus(ctx, launch.StatusGenesisReady)
	if err != nil {
		log.Error().Err(err).Msg("launch monitor: find candidates")
		return
	}

	for _, l := range candidates {
		if l.MonitorRPCURL == "" {
			continue
		}
		if validateURL != nil {
			if err := validateURL(l.MonitorRPCURL); err != nil {
				log.Warn().Err(err).Str("rpc", l.MonitorRPCURL).Stringer("launch_id", l.ID).
					Msg("launch monitor: skipping URL that fails SSRF validation")
				continue
			}
		}
		if detected, err := pollBlock1(ctx, httpClient, l.MonitorRPCURL, l.Record.ChainID); err != nil {
			log.Error().Err(err).Str("rpc", l.MonitorRPCURL).Stringer("launch_id", l.ID).Msg("launch monitor: poll block1")
		} else if detected {
			markLaunched(ctx, repo, pub, log, l, l.MonitorRPCURL)
		}
	}
}

// pollBlock1 reports whether the CometBFT node at rpcURL has produced block 1 for THIS launch:
// a block at height 1 whose header chain_id equals expectedChainID. Verifying the chain_id and
// height (not merely that some blocks exist) stops a wrong-chain or fabricated block from the
// committee-set, content-trusted MonitorRPCURL from flipping the launch to LAUNCHED (NHP-5).
func pollBlock1(ctx context.Context, httpClient *http.Client, rpcURL, expectedChainID string) (bool, error) {
	url := fmt.Sprintf("%s/block?height=1", rpcURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, nil // node not ready yet — not an error
	}

	// CometBFT encodes header.height as a string. A null/absent block leaves the header fields
	// empty, so it fails the chain_id match below — no separate non-null check needed.
	var body struct {
		Result struct {
			Block struct {
				Header struct {
					ChainID string `json:"chain_id"`
					Height  string `json:"height"`
				} `json:"header"`
			} `json:"block"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("decode response: %w", err)
	}
	h := body.Result.Block.Header
	return expectedChainID != "" && h.ChainID == expectedChainID && h.Height == "1", nil
}

func markLaunched(ctx context.Context, repo launchMonitorRepo, pub eventPublisher, log zerolog.Logger, l *launch.Launch, sourceRPC string) {
	if err := l.MarkLaunched(); err != nil {
		log.Error().Err(err).Stringer("launch_id", l.ID).Msg("launch monitor: mark launched")
		return
	}
	if err := repo.Save(ctx, l); err != nil {
		log.Error().Err(err).Stringer("launch_id", l.ID).Msg("launch monitor: save")
		return
	}
	ev := domain.LaunchDetected{
		LaunchID:  l.ID,
		SourceRPC: sourceRPC,
	}
	pub.Publish(ev.WithTime(time.Now().UTC()))
	log.Info().Stringer("launch_id", l.ID).Str("rpc", sourceRPC).Msg("launch transitioned to LAUNCHED")
}

// RunProposalExpiry runs ExpireStale on a fixed interval until ctx is canceled.
// Any error is logged but does not stop the loop — a transient DB error should
// not permanently halt expiry.
func RunProposalExpiry(ctx context.Context, svc proposalExpirer, log zerolog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := svc.ExpireStale(ctx); err != nil {
				log.Error().Err(err).Msg("proposal expiry job")
			}
		}
	}
}

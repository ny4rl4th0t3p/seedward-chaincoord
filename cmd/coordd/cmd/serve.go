package cmd

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ratelimit"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/config"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/api"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/auditlog"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/auth"
	appCrypto "github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/crypto"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/gentxvalidation"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/jobs"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/sse"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/storage/fs"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/storage/sqlite"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/netutil"
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the coordd HTTP server",
		RunE:  runServe,
	}

	cmd.Flags().String("listen-addr", "", "address to listen on (default :8080)")
	cmd.Flags().String("db-path", "", "path to SQLite database file (required)")
	cmd.Flags().String("audit-log-path", "", "path to audit log JSONL file (required)")
	cmd.Flags().String("files-path", "", "directory for genesis + allocation file storage (required)")
	cmd.Flags().String("log-level", "", "log level: debug, info, warn, error (default info)")
	cmd.Flags().String("cors-origins", "", "comma-separated allowed CORS origins (use * for dev)")
	cmd.Flags().String("tls-cert", "", "path to TLS certificate file (PEM); requires --tls-key")
	cmd.Flags().String("tls-key", "", "path to TLS private key file (PEM); requires --tls-cert")
	cmd.Flags().Bool("insecure-no-tls", false, "suppress the TLS warning when TLS is terminated upstream (infra TLS mode)")
	cmd.Flags().Bool("insecure-no-rate-limit", false, "disable per-IP rate limit on /auth/challenge (automated test use only)")
	cmd.Flags().Bool("genesis-host-mode", false,
		"accept raw genesis file uploads and serve them from disk (Option C); default is attestor-only mode")
	cmd.Flags().Int64("genesis-max-bytes", 0, "maximum raw genesis upload size in bytes when host mode is on (default 700 MiB)")
	return cmd
}

// isLoopback reports whether the host part of addr resolves to a loopback
// interface. addr is in "host:port" or ":port" form (as used by net/http).
// Returns false on any parse error so that unknown addresses produce a warning.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		// ":port" form — binds on all interfaces, not loopback only.
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// buildLogger creates a zerolog.Logger wired to the given level string.
// Debug level uses a human-readable ConsoleWriter on stderr; all other levels
// emit structured JSON to stdout. The threshold is applied via zerolog's process-global level
// (not a per-logger level) so it can be changed at runtime through POST /api/v1/admin/log-level; the
// console-vs-JSON writer choice, by contrast, is fixed here at startup.
func buildLogger(level string) zerolog.Logger {
	lvl, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)
	if lvl == zerolog.DebugLevel {
		return zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	}
	return zerolog.New(os.Stdout).With().Timestamp().Logger()
}

func loadServeConfig(cmd *cobra.Command) (*config.Config, error) {
	v := viper.New()
	for _, m := range []struct{ flag, key string }{
		{"listen-addr", "listen_addr"},
		{"db-path", "db_path"},
		{"audit-log-path", "audit_log_path"},
		{"files-path", "files_path"},
		{"log-level", "log_level"},
		{"cors-origins", "cors_origins"},
		{"tls-cert", "tls_cert"},
		{"tls-key", "tls_key"},
		{"insecure-no-tls", "insecure_no_tls"},
		{"insecure-no-rate-limit", "insecure_no_rate_limit"},
		{"genesis-host-mode", "genesis_host_mode"},
		{"genesis-max-bytes", "genesis_max_bytes"},
	} {
		if err := v.BindPFlag(m.key, cmd.Flags().Lookup(m.flag)); err != nil {
			return nil, fmt.Errorf("binding flag %q: %w", m.flag, err)
		}
	}
	return config.Load(v, cfgFile)
}

// serveAndWait starts the HTTP server, logs readiness, waits for a signal or
// server error, then performs a graceful shutdown.
func serveAndWait(httpServer *http.Server, cfg *config.Config, logger zerolog.Logger, stopJobs context.CancelFunc) error {
	if cfg.TLSCert != "" {
		logger.Info().Str("addr", cfg.ListenAddr).Msg("coordd listening (TLS)")
	} else {
		if !cfg.InsecureNoTLS && !isLoopback(cfg.ListenAddr) {
			logger.Warn().Str("addr", cfg.ListenAddr).
				Msg("TLS not configured and COORD_INSECURE_NO_TLS is not set — " +
					"traffic is unencrypted; use --tls-cert/--tls-key or a reverse proxy")
		}
		logger.Info().Str("addr", cfg.ListenAddr).Msg("coordd listening")
	}

	serverErr := make(chan error, 1)
	go func() {
		if cfg.TLSCert != "" {
			serverErr <- httpServer.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			serverErr <- httpServer.ListenAndServe()
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server error: %w", err)
		}
	case sig := <-quit:
		logger.Info().Str("signal", sig.String()).Msg("shutting down")
		stopJobs()

		const shutdownTimeout = 30 * time.Second
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		logger.Info().Msg("shutdown complete")
	}
	return nil
}

func monitorURLValidatorFor(insecureNoSSRFCheck bool) func(string) error {
	if insecureNoSSRFCheck {
		return netutil.ValidateRPCURLFormat
	}
	return netutil.ValidateRPCURL
}

// openFileStores opens the genesis and allocation file stores. They share the same root
// directory: their filenames (initial/final vs alloc-<type>) never collide under
// <root>/<launchID>/, so allocation files need no separate path config.
func openFileStores(path string) (*fs.GenesisStore, *fs.AllocationStore, error) {
	genesisStore, err := fs.NewGenesisStore(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening genesis store: %w", err)
	}
	allocationStore, err := fs.NewAllocationStore(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening allocation store: %w", err)
	}
	return genesisStore, allocationStore, nil
}

// configureLaunchService applies config-driven overrides to the launch service: the SSRF-check
// bypass (trusted networks) and the rehearsal claim-lease TTL (empty config keeps the built-in default).
func configureLaunchService(svc *services.LaunchService, cfg *config.Config) *services.LaunchService {
	if cfg.InsecureNoSSRFCheck {
		svc = svc.WithURLValidator(netutil.ValidateRPCURLFormat)
	}
	if cfg.RehearsalLeaseTTL > 0 {
		svc = svc.WithRehearsalLeaseTTL(cfg.RehearsalLeaseTTL)
	}
	return svc
}

// startBackgroundJobs launches the proposal-expiry and launch-monitor goroutines on a fresh
// context and returns its cancel func for graceful shutdown.
func startBackgroundJobs(
	proposalSvc *services.ProposalService,
	launchRepo *sqlite.LaunchRepository,
	sseBroker *sse.Broker,
	logger zerolog.Logger,
	validateURL func(string) error,
) context.CancelFunc {
	jobCtx, stopJobs := context.WithCancel(context.Background())
	go jobs.RunProposalExpiry(jobCtx, proposalSvc, logger, time.Minute)
	go jobs.RunLaunchMonitor(jobCtx, launchRepo, sseBroker, logger, time.Minute, validateURL)
	return stopJobs
}

// openAuditLog opens the audit log, wires the operational logger, restores its hash-chain tip, and
// runs the boot-time integrity check per cfg.AuditStartupVerify (full scan vs. cheap tip-only). The
// caller owns closing the returned writer.
func openAuditLog(
	cfg *config.Config, chainStore ports.AuditChainStore, privKey ed25519.PrivateKey, logger zerolog.Logger,
) (*auditlog.JSONLWriter, error) {
	auditLog, err := auditlog.Open(cfg.AuditLogPath, privKey)
	if err != nil {
		return nil, fmt.Errorf("opening audit log: %w", err)
	}
	auditLog.WithLogger(logger)
	if err := auditLog.WithPrevHashStore(context.Background(), chainStore); err != nil {
		auditLog.Close()
		return nil, fmt.Errorf("restoring audit chain tip: %w", err)
	}
	// Boot-time integrity check: refuse to start on tamper/corruption; warn on a clock regression.
	// "full" scans the whole log; "tail" (large-log escape hatch) relies on the cheap tip check.
	if err := auditLog.VerifyOnStart(cfg.AuditStartupVerify != config.AuditStartupVerifyTail); err != nil {
		auditLog.Close()
		return nil, err
	}
	return auditLog, nil
}

// newHealthCheck returns the /healthz liveness probe: the DB is queryable (SELECT 1) and the audit
// log file is present. A failure of either (DB down, disk unmounted/full) surfaces as a 503.
func newHealthCheck(db *sql.DB, auditLogPath string) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := db.QueryRowContext(ctx, "SELECT 1").Scan(new(int)); err != nil {
			return fmt.Errorf("database: %w", err)
		}
		if _, err := os.Stat(auditLogPath); err != nil {
			return fmt.Errorf("audit log: %w", err)
		}
		return nil
	}
}

func runServe(cmd *cobra.Command, _ []string) error {
	// --- Config ----------------------------------------------------------
	cfg, err := loadServeConfig(cmd)
	if err != nil {
		return err
	}
	// Viper bool env-var unmarshalling can silently miss the env in some
	// configurations; read it directly as a hard override.
	if os.Getenv("COORD_INSECURE_NO_RATE_LIMIT") == "true" {
		cfg.InsecureNoRateLimit = true
	}

	// --- Logger ----------------------------------------------------------
	logger := buildLogger(cfg.LogLevel)

	// --- Storage ---------------------------------------------------------
	db, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	tx := sqlite.NewTransactor(db)
	launchRepo := sqlite.NewLaunchRepository(db)
	joinReqRepo := sqlite.NewJoinRequestRepository(db)
	proposalRepo := sqlite.NewProposalRepository(db)
	readinessRepo := sqlite.NewReadinessRepository(db)
	rehearsalAttemptRepo := sqlite.NewRehearsalAttemptRepository(db)
	rehearsalResultRepo := sqlite.NewRehearsalResultRepository(db)
	sessionStore, err := auth.NewJWTSessionStore(cfg.JWTPrivKeyB64, db)
	if err != nil {
		return fmt.Errorf("initializing JWT session store: %w", err)
	}
	var challengeStore ports.ChallengeStore = sqlite.NewChallengeStore(db)
	if !cfg.InsecureNoRateLimit {
		challengeStore = ratelimit.NewRateLimitedChallengeStore(
			challengeStore,
			sqlite.NewChallengeRateLimiterStore(db),
		)
	}
	nonceStore := sqlite.NewNonceStore(db)
	coordinatorAllowlistRepo := sqlite.NewCoordinatorAllowlistRepo(db)

	// --- Audit key -------------------------------------------------------
	// Config validation already ensures the key is valid base64 and 32 bytes.
	auditSeed, _ := base64.StdEncoding.DecodeString(cfg.AuditPrivKeyB64)
	auditPrivKey := ed25519.NewKeyFromSeed(auditSeed)

	// --- Audit log -------------------------------------------------------
	auditLog, err := openAuditLog(cfg, sqlite.NewAuditStateStore(db), auditPrivKey, logger)
	if err != nil {
		return err
	}
	defer auditLog.Close()

	// --- File stores -----------------------------------------------------
	genesisStore, allocationStore, err := openFileStores(cfg.FilesPath)
	if err != nil {
		return err
	}

	// --- Cross-cutting ---------------------------------------------------
	sseBroker := sse.New()
	verifier := appCrypto.NewSecp256k1Verifier()

	// --- Application services --------------------------------------------
	authSvc := services.NewAuthService(challengeStore, sessionStore, nonceStore, verifier, auditLog).WithLogger(logger)
	launchSvc := services.NewLaunchService(
		launchRepo,
		joinReqRepo,
		readinessRepo,
		genesisStore,
		allocationStore,
		sseBroker,
		auditLog,
		rehearsalAttemptRepo,
		rehearsalResultRepo,
	)
	launchSvc = configureLaunchService(launchSvc, cfg).WithLogger(logger)
	joinReqSvc := services.NewJoinRequestService(launchRepo, joinReqRepo, nonceStore, verifier, gentxvalidation.New(), sseBroker, auditLog).
		WithLogger(logger)
	proposalSvc := services.NewProposalService(
		launchRepo, joinReqRepo, proposalRepo, readinessRepo,
		nonceStore, verifier, sseBroker, auditLog, tx,
	).WithRehearsalGate(cfg.RehearsalGate, rehearsalResultRepo).WithLogger(logger)
	readinessSvc := services.NewReadinessService(launchRepo, joinReqRepo, readinessRepo, nonceStore, verifier, sseBroker, auditLog).
		WithLogger(logger)

	// --- HTTP server -----------------------------------------------------
	coordinatorSvc := services.NewCoordinatorService(coordinatorAllowlistRepo, auditLog).WithLogger(logger)
	apiServer := api.NewServer(
		logger,
		cfg.CORSOrigins,
		cfg.AdminAddresses,
		authSvc, launchSvc, joinReqSvc, proposalSvc, readinessSvc,
		sessionStore, sseBroker, genesisStore, allocationStore, auditLog,
		auditLog.PubKey(),
		coordinatorSvc,
		cfg.LaunchPolicy,
		cfg.GenesisHostMode,
		cfg.GenesisMaxBytes,
		cfg.InsecureNoRateLimit,
		cfg.RehearsalOpsToken,
	).WithHealthCheck(newHealthCheck(db, cfg.AuditLogPath)).WithTLS(cfg.TLSCert != "")
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// --- Background jobs -------------------------------------------------
	stopJobs := startBackgroundJobs(proposalSvc, launchRepo, sseBroker, logger, monitorURLValidatorFor(cfg.InsecureNoSSRFCheck))
	defer stopJobs()

	// --- Start + graceful shutdown ---------------------------------------
	return serveAndWait(httpServer, cfg, logger, stopJobs)
}

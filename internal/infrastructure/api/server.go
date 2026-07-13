// Package api contains the HTTP handler layer.
// It translates HTTP requests into application service calls and writes
// JSON responses.  No business logic lives here.
package api

import (
	"crypto/ed25519"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
)

// Server holds every dependency that HTTP handlers need and owns the
// Chi router that routes incoming requests.
type Server struct {
	log              zerolog.Logger
	corsOrigins      []string
	adminAddresses   map[string]struct{} // set for O(1) lookup
	launchPolicy     string              // "open" or "restricted"
	genesisHostMode  bool                // true → accept raw file uploads (Option C)
	genesisMaxBytes  int64               // max raw upload size when host mode is on
	disableRateLimit bool                // true → skip HTTP per-IP rate limiters (test only; storage-layer limiter is disabled at wire-up)
	auth             *services.AuthService
	launches         *services.LaunchService
	joinReqs         *services.JoinRequestService
	proposals        *services.ProposalService
	readiness        *services.ReadinessService
	sessions         ports.SessionStore
	sseBroker        sseSubscriber
	genesisStore     ports.GenesisStore
	allocationStore  ports.AllocationStore
	auditLog         ports.AuditLogReader
	auditPubKey      ed25519.PublicKey // nil if no audit signing key is configured
	coordinators     *services.CoordinatorService
	// rehearsalOpsToken is the shared ops-plane bearer token for the /bridge/* endpoints
	// Empty → the bridge is disabled (requireOps fails closed).
	rehearsalOpsToken string
}

// sseSubscriber is the subset of the SSE broker the server needs.
type sseSubscriber interface {
	Subscribe(launchID string) chan domain.DomainEvent
	Unsubscribe(launchID string, ch chan domain.DomainEvent)
}

// NewServer wires together all handler dependencies and registers routes.
// corsOriginsCSV is a comma-separated list of allowed origins (e.g. "https://app.example.com,https://dev.example.com").
// Pass "*" to allow all origins (development only). An empty string disables CORS headers entirely.
// adminAddresses is the list of operator addresses permitted to call admin-only endpoints.
// genesisHostMode enables Option C (raw file upload/serve); when false only attestor mode is accepted.
// genesisMaxBytes is the maximum raw genesis upload size (only relevant when genesisHostMode is true).
// disableRateLimit bypasses the HTTP per-IP rate limiters; only for automated test environments.
// The storage-layer per-operator rate limiter is disabled separately at wire-up time (serve.go).
func NewServer(
	log zerolog.Logger,
	corsOriginsCSV string,
	adminAddresses []string,
	auth *services.AuthService,
	launches *services.LaunchService,
	joinReqs *services.JoinRequestService,
	proposals *services.ProposalService,
	readiness *services.ReadinessService,
	sessions ports.SessionStore,
	sseBroker sseSubscriber,
	genesisStore ports.GenesisStore,
	allocationStore ports.AllocationStore,
	auditLog ports.AuditLogReader,
	auditPubKey ed25519.PublicKey,
	coordinators *services.CoordinatorService,
	launchPolicy string,
	genesisHostMode bool,
	genesisMaxBytes int64,
	disableRateLimit bool,
	rehearsalOpsToken string,
) *Server {
	var origins []string
	if corsOriginsCSV != "" {
		for _, o := range strings.Split(corsOriginsCSV, ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins = append(origins, o)
			}
		}
	}

	admins := make(map[string]struct{}, len(adminAddresses))
	for _, a := range adminAddresses {
		admins[accountLookupKey(a)] = struct{}{}
	}

	s := &Server{
		log:               log,
		corsOrigins:       origins,
		adminAddresses:    admins,
		launchPolicy:      launchPolicy,
		genesisHostMode:   genesisHostMode,
		genesisMaxBytes:   genesisMaxBytes,
		disableRateLimit:  disableRateLimit,
		auth:              auth,
		launches:          launches,
		joinReqs:          joinReqs,
		proposals:         proposals,
		readiness:         readiness,
		sessions:          sessions,
		sseBroker:         sseBroker,
		genesisStore:      genesisStore,
		allocationStore:   allocationStore,
		auditLog:          auditLog,
		auditPubKey:       auditPubKey,
		coordinators:      coordinators,
		rehearsalOpsToken: rehearsalOpsToken,
	}
	return s
}

// maxJSONBody is the body size cap for all JSON POST/PATCH endpoints (1 MiB).
// Genesis upload has its own higher cap and is exempt.
const (
	maxJSONBody         = 1 << 20
	challengeRatePerMin = 10 // max challenge requests per IP per minute
	validatorRatePerMin = 60 // max validator write requests per IP per minute
)

// jsonPOST composes requireJSONBody with requireAuth for authenticated JSON endpoints.
func (s *Server) jsonPOST(h http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(requireJSONBody(h))
}

// jsonAdminPOST composes requireJSONBody with requireAdmin for admin-only JSON endpoints.
func (s *Server) jsonAdminPOST(h http.HandlerFunc) http.HandlerFunc {
	return s.requireAdmin(requireJSONBody(h))
}

// requireJSONPOST composes requireJSONBody for unauthenticated JSON endpoints.
func requireJSONPOST(h http.HandlerFunc) http.HandlerFunc {
	return requireJSONBody(h)
}

// Handler builds and returns the configured Chi router.
// Called once at startup; the result is passed to http.Server.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	// Panic recovery — outermost so it catches panics from all other middleware
	// and handlers. Logs stack trace via zerolog and returns 500.
	r.Use(recoveryMiddleware)

	// CORS — only registered when origins are configured.
	if len(s.corsOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   s.corsOrigins,
			AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-Id"},
			ExposedHeaders:   []string{"X-Request-Id"},
			AllowCredentials: true,
			MaxAge:           300,
		}))
	}

	// Structured logging middleware — injects logger into context and emits
	// one access log line per request (method, path, status, latency, IP).
	r.Use(hlog.NewHandler(s.log))
	r.Use(hlog.RequestIDHandler("req_id", "X-Request-Id"))
	r.Use(hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
		hlog.FromRequest(r).Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Msg("request")
	}))
	r.Use(hlog.RemoteAddrHandler("ip"))

	// Health check and server metadata — no auth required.
	r.Get("/healthz", s.handleHealthz)
	r.Get("/audit/pubkey", s.handleAuditPubKey)

	// Admin endpoints — require authenticated admin address.
	r.Post("/admin/coordinators", s.jsonAdminPOST(s.handleCoordinatorAdd))
	r.Delete("/admin/coordinators/{address}", s.requireAdmin(s.handleCoordinatorRemove))
	r.Get("/admin/coordinators", s.requireAdmin(s.handleCoordinatorList))
	r.Delete("/admin/sessions/{address}", s.requireAdmin(s.handleAdminRevokeAllSessions))

	// Auth endpoints — challenge is rate-limited by IP unless disabled (e.g. in tests).
	if s.disableRateLimit {
		r.Post("/auth/challenge", requireJSONPOST(s.handleAuthChallenge))
	} else {
		r.With(httprate.LimitByIP(challengeRatePerMin, time.Minute)).Post("/auth/challenge", requireJSONPOST(s.handleAuthChallenge))
	}
	r.Post("/auth/verify", requireJSONPOST(s.handleAuthVerify))
	r.Delete("/auth/session", s.handleAuthRevoke)
	r.Get("/auth/session", s.handleAuthSessionInfo)
	r.Delete("/auth/sessions/all", s.requireAuth(s.handleAuthRevokeAll))

	// Committee endpoints.
	r.Post("/launch/{id}/committee", s.jsonPOST(s.handleCommitteeCreate))
	r.Get("/committee/{launch_id}", s.optionalAuth(s.handleCommitteeGet))

	// Launch endpoints.
	r.Post("/launch", s.jsonPOST(s.handleLaunchCreate))
	r.Get("/launches", s.optionalAuth(s.handleLaunchList))
	r.Get("/launch/{id}", s.optionalAuth(s.handleLaunchGet))
	r.Patch("/launch/{id}", s.jsonPOST(s.handleLaunchPatch))
	r.Post("/launch/{id}/open-window", s.requireAuth(s.handleOpenWindow))
	r.Post("/launch/{id}/cancel", s.requireAuth(s.handleLaunchCancel))

	// Members list — committee-gated (add/remove/list). requireAuth so an unauthenticated
	// caller is 401; the service enforces committee membership (403) and the editable-status
	// gate (409). These grant/revoke the hot-address see+submit membership.
	r.Post("/launch/{id}/members", s.jsonPOST(s.handleMemberAdd))
	r.Delete("/launch/{id}/members/{address}", s.requireAuth(s.handleMemberRemove))
	r.Post("/launch/{id}/rehearsal/{attempt_id}/reset", s.requireAuth(s.handleRehearsalReset))
	r.Get("/launch/{id}/members", s.requireAuth(s.handleMemberList))

	// Chain metadata a member needs to register the chain with a wallet. optionalAuth +
	// visibility-gated in the handler: a non-member (committee ∪ members) gets 404, so the
	// launch's existence is not revealed.
	r.Get("/launch/{id}/chain-hint", s.optionalAuth(s.handleChainHint))

	// Genesis endpoints — default is attestor mode (JSON ref); host mode must be explicitly enabled.
	r.Post("/launch/{id}/genesis", s.requireAuth(s.handleGenesisUpload))
	// optionalAuth so an allowlisted member can be identified and pass the visibility gate; an
	// anonymous caller resolves to "" and is treated as a non-member (private-always).
	r.Get("/launch/{id}/genesis", s.optionalAuth(s.handleGenesisGet))
	r.Get("/launch/{id}/genesis/hash", s.optionalAuth(s.handleGenesisHashGet))

	// Allocation file endpoints — committee-gated dual-mode upload (like genesis);
	// list + serve. Approval/rejection goes through the generic proposal endpoints.
	r.Post("/launch/{id}/allocations/{type}", s.requireAuth(s.handleAllocationUpload))
	r.Get("/launch/{id}/allocations", s.optionalAuth(s.handleAllocationList))
	r.Get("/launch/{id}/allocations/{type}", s.optionalAuth(s.handleAllocationGet))

	// Validator write endpoints — rate-limited to 60 req/IP/min (abuse prevention).
	r.Group(func(r chi.Router) {
		if !s.disableRateLimit {
			r.Use(httprate.LimitByIP(validatorRatePerMin, time.Minute))
		}

		r.Post("/launch/{id}/join", s.jsonPOST(s.handleJoinSubmit))
		r.Post("/launch/{id}/proposal", s.jsonPOST(s.handleProposalRaise))
		r.Post("/launch/{id}/proposal/{prop_id}/sign", s.jsonPOST(s.handleProposalSign))
		r.Post("/launch/{id}/ready", s.jsonPOST(s.handleReadinessConfirm))
	})

	s.registerBridgeRoutes(r)
	s.registerLaunchReadRoutes(r)

	return r
}

// registerLaunchReadRoutes mounts the non-rate-limited launch read endpoints (committee-member
// or optional-auth GETs). Grouped out of Handler to keep it under the statement limit.
func (s *Server) registerLaunchReadRoutes(r chi.Router) {
	r.Get("/launch/{id}/join", s.requireAuth(s.handleJoinList))
	r.Get("/launch/{id}/join/grouped", s.requireAuth(s.handleJoinGrouped))
	r.Get("/launch/{id}/join/{req_id}", s.requireAuth(s.handleJoinGet))
	r.Get("/launch/{id}/gentxs", s.requireAuth(s.handleGentxsGet))
	r.Get("/launch/{id}/proposals", s.requireAuth(s.handleProposalList))
	r.Get("/launch/{id}/proposal/{prop_id}", s.requireAuth(s.handleProposalGet))
	r.Get("/launch/{id}/dashboard", s.optionalAuth(s.handleDashboard))
	r.Get("/launch/{id}/peers", s.optionalAuth(s.handlePeers))
	r.Get("/launch/{id}/audit", s.optionalAuth(s.handleAudit))
	r.Get("/launch/{id}/events", s.optionalAuth(s.handleEvents))
	r.Get("/launch/{id}/rehearsal", s.requireAuth(s.handleRehearsalResultsList))
}

// registerBridgeRoutes mounts the rehearsal-bridge (ops-plane) endpoints under /bridge, so a
// devop can restrict the whole prefix to an internal VNet (cut internet access).
// requireOps gates every endpoint on the shared rehearsal ops token; the bridge is disabled
// (fail-closed) when the token is unconfigured. coordd never dials the rehearsal service.
func (s *Server) registerBridgeRoutes(r chi.Router) {
	r.Route("/bridge", func(r chi.Router) {
		r.Get("/launches/{id}/rehearsal-input", s.requireOps(s.handleRehearsalInput))
		r.Post("/launches/{id}/rehearsal-claim", s.requireOps(requireJSONBody(s.handleRehearsalClaim)))
		r.Get("/launches/{id}/allocations/{type}", s.requireOps(s.handleBridgeAllocationGet))
		r.Post("/launches/{id}/rehearsal-results", s.requireOps(requireJSONBody(s.handleRehearsalResults)))
	})
}

// handleHealthz responds 200 OK with a minimal JSON body.
//
// @Summary      Health check
// @Tags         health
// @Produce      json
// @Success      200  {object}  map[string]string
// @Router       /healthz [get]
func (*Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- stub handlers (implemented in subsequent phases) --------------------

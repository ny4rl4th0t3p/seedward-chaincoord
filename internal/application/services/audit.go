package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
)

// writeAuditEvent marshals a domain event and appends it to the audit log under the given scope —
// a launch ID for launch-scoped events, or ports.GlobalAuditScope for non-launch (admin-plane)
// actions. This is the single place that turns a domain event into an audit entry; all callers
// route through it so every event is recorded identically.
func writeAuditEvent(ctx context.Context, w ports.AuditLogWriter, scope string, ev domain.DomainEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	// Occurrence time: an event carrying an authoritative domain time (a proposal's ExecutedAt, set
	// via WithTime) keeps it; a bare event — written synchronously the moment it happens — is stamped
	// with the write time here, so the log always fills its own "now". This is the ONLY place a
	// synchronously-emitted event gets its timestamp; callers do not chain WithTime just to get "now".
	occurredAt := ev.OccurredAt()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return w.Append(ctx, ports.AuditEvent{
		LaunchID:   scope,
		EventName:  ev.EventName(),
		OccurredAt: occurredAt,
		Payload:    payload,
	})
}

// recordAudit writes an audit event and LOGS (does not fail the operation) on error. Used for
// post-commit, non-critical events where the mutation already succeeded — best-effort audit with
// an observable failure. Critical proposal-execution events use the fatal path in dispatchEvents.
func recordAudit(ctx context.Context, w ports.AuditLogWriter, log zerolog.Logger, scope string, ev domain.DomainEvent) {
	if err := writeAuditEvent(ctx, w, scope, ev); err != nil {
		log.Error().Err(err).Str("event", ev.EventName()).Str("scope", scope).
			Msg("audit write failed; event not recorded (mutation already committed)")
		return
	}
	// Operational trace: one Info line per recorded action so a live container-log follower can see
	// what happened without reading the (forensic, hash-chained) audit file. Suppressible by level.
	log.Info().Str("event", ev.EventName()).Str("scope", scope).Msg("action recorded")
}

// auditAccount returns the canonical account hex for audit payloads, falling back to the raw
// string if it can't be parsed (the address was already validated by the preceding mutation).
func auditAccount(addr string) string {
	if hex, err := accountKey(addr); err == nil {
		return hex
	}
	return addr
}

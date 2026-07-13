package services

import (
	"context"
	"encoding/json"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
)

// writeAuditEvent marshals a domain event and appends it to the audit log under the given scope —
// a launch ID for launch-scoped events, or ports.GlobalAuditScope for non-launch (admin-plane)
// actions. This is the single place that turns a domain event into an audit entry; the per-service
// writeAudit methods delegate here so every event is recorded identically.
func writeAuditEvent(ctx context.Context, w ports.AuditLogWriter, scope string, ev domain.DomainEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return w.Append(ctx, ports.AuditEvent{
		LaunchID:   scope,
		EventName:  ev.EventName(),
		OccurredAt: ev.OccurredAt(),
		Payload:    payload,
	})
}

// auditAccount returns the canonical account hex for audit payloads, falling back to the raw
// string if it can't be parsed (the address was already validated by the preceding mutation).
func auditAccount(addr string) string {
	if hex, err := accountKey(addr); err == nil {
		return hex
	}
	return addr
}

package services

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
)

// CoordinatorService wraps the coordinator allowlist repository, recording global-scope audit
// entries for the mutating operations. Reads (List/Contains) delegate unchanged. The coordinator
// allowlist is global (not launch-scoped), so its events use ports.GlobalAuditScope.
type CoordinatorService struct {
	repo   ports.CoordinatorAllowlistRepository
	audit  ports.AuditLogWriter
	logger zerolog.Logger
}

// NewCoordinatorService constructs a CoordinatorService over a repository and audit writer.
func NewCoordinatorService(repo ports.CoordinatorAllowlistRepository, audit ports.AuditLogWriter) *CoordinatorService {
	return &CoordinatorService{repo: repo, audit: audit, logger: zerolog.Nop()}
}

// WithLogger sets the logger used to report audit-write failures (defaults to no-op).
func (s *CoordinatorService) WithLogger(l zerolog.Logger) *CoordinatorService {
	s.logger = l
	return s
}

// writeAudit records an audit event under the given scope, logging (not failing) on error — the
// post-commit log-and-continue path, consistent with the other services.
func (s *CoordinatorService) writeAudit(ctx context.Context, scope string, ev domain.DomainEvent) {
	recordAudit(ctx, s.audit, s.logger, scope, ev)
}

// Add adds an address to the allowlist and records a CoordinatorAdded audit event.
func (s *CoordinatorService) Add(ctx context.Context, address, addedBy string) error {
	if err := s.repo.Add(ctx, address, addedBy); err != nil {
		return err
	}
	s.writeAudit(ctx, ports.GlobalAuditScope, domain.CoordinatorAdded{
		Address: auditAccount(address),
		AddedBy: auditAccount(addedBy),
	}.WithTime(time.Now().UTC()))
	return nil
}

// Remove removes an address from the allowlist and records a CoordinatorRemoved audit event.
// removedBy is the admin performing the removal (for the audit trail, not passed to the repo).
func (s *CoordinatorService) Remove(ctx context.Context, address, removedBy string) error {
	if err := s.repo.Remove(ctx, address); err != nil {
		return err
	}
	s.writeAudit(ctx, ports.GlobalAuditScope, domain.CoordinatorRemoved{
		Address:   auditAccount(address),
		RemovedBy: auditAccount(removedBy),
	}.WithTime(time.Now().UTC()))
	return nil
}

// List delegates to the repository.
func (s *CoordinatorService) List(ctx context.Context, page, perPage int) ([]*ports.CoordinatorAllowlistEntry, int, error) {
	return s.repo.List(ctx, page, perPage)
}

// Contains delegates to the repository.
func (s *CoordinatorService) Contains(ctx context.Context, address string) (bool, error) {
	return s.repo.Contains(ctx, address)
}

package services

import (
	"context"
	"time"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
)

// CoordinatorService wraps the coordinator allowlist repository, recording global-scope audit
// entries for the mutating operations. Reads (List/Contains) delegate unchanged. The coordinator
// allowlist is global (not launch-scoped), so its events use ports.GlobalAuditScope.
type CoordinatorService struct {
	repo  ports.CoordinatorAllowlistRepository
	audit ports.AuditLogWriter
}

// NewCoordinatorService constructs a CoordinatorService over a repository and audit writer.
func NewCoordinatorService(repo ports.CoordinatorAllowlistRepository, audit ports.AuditLogWriter) *CoordinatorService {
	return &CoordinatorService{repo: repo, audit: audit}
}

// Add adds an address to the allowlist and records a CoordinatorAdded audit event.
func (s *CoordinatorService) Add(ctx context.Context, address, addedBy string) error {
	if err := s.repo.Add(ctx, address, addedBy); err != nil {
		return err
	}
	_ = writeAuditEvent(ctx, s.audit, ports.GlobalAuditScope, domain.CoordinatorAdded{
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
	_ = writeAuditEvent(ctx, s.audit, ports.GlobalAuditScope, domain.CoordinatorRemoved{
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

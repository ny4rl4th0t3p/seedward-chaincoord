package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// AuditStateStore persists the audit log chain tip (SHA-256 of the last written
// line) so the hash chain survives server restarts.
type AuditStateStore struct {
	db *sql.DB
}

func NewAuditStateStore(db *sql.DB) *AuditStateStore {
	return &AuditStateStore{db: db}
}

func (s *AuditStateStore) LoadPrevHash(ctx context.Context) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT prev_hash FROM audit_state WHERE id = 1`).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load audit prev hash: %w", err)
	}
	return hash, nil
}

func (s *AuditStateStore) SavePrevHash(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_state (id, prev_hash) VALUES (1, ?)
		 ON CONFLICT(id) DO UPDATE SET prev_hash = excluded.prev_hash`,
		hash,
	)
	if err != nil {
		return fmt.Errorf("save audit prev hash: %w", err)
	}
	return nil
}

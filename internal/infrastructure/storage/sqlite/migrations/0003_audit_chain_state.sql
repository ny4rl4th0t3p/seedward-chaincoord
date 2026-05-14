-- Migration 0003: audit chain state
-- Stores the SHA-256 of the last written audit log line so the hash chain
-- survives server restarts. CHECK (id = 1) ensures at most one row.
CREATE TABLE IF NOT EXISTS audit_state (
    id        INTEGER PRIMARY KEY CHECK (id = 1),
    prev_hash TEXT    NOT NULL DEFAULT ''
);
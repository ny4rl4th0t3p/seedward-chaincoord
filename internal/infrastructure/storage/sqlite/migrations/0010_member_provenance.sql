-- M2: record who added each member and when. Set by the committee member-add endpoint
-- (POST /launch/{id}/members); empty for pre-provenance entries (the address-only
-- create/patch path). added_at is an RFC3339 string, matching the other time columns.
ALTER TABLE allowlist ADD COLUMN added_by TEXT NOT NULL DEFAULT '';
ALTER TABLE allowlist ADD COLUMN added_at TEXT NOT NULL DEFAULT '';

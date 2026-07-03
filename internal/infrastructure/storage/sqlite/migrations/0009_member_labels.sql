-- M1: the per-launch members list (allowlist of hot actor addresses) carries a label
-- per entry — a pointer to the committee's off-band verification of who holds the address.
-- The label is set when a member is added (M2 endpoints); existing address-only entries
-- default to an empty label. Provenance (added_by/added_at) lands with M2's add endpoint.
ALTER TABLE allowlist ADD COLUMN label TEXT NOT NULL DEFAULT '';

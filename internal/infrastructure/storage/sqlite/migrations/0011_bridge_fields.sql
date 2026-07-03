-- Bridge (B0): fields the chaincoord ↔ rehearsal bridge needs on the launch record.
--   total_supply             — genesis supply anchor (bigint string, base denom); required by
--                              the rehearsal service's genesis.Build (contract D5). Set at creation.
--   rehearsal_service_pubkey — base64 Ed25519 key coordd trusts for this launch's result facts (D2).
--   rehearsal_endpoint       — advertised URL of the launch's rehearsal service (D2).
-- All optional/empty by default so existing launches and create bodies are unaffected. Added via
-- ALTER TABLE (appended physically, so SELECT * scans them last, after bech32_prefix).
ALTER TABLE launches ADD COLUMN total_supply TEXT NOT NULL DEFAULT '';
ALTER TABLE launches ADD COLUMN rehearsal_service_pubkey TEXT NOT NULL DEFAULT '';
ALTER TABLE launches ADD COLUMN rehearsal_endpoint TEXT NOT NULL DEFAULT '';

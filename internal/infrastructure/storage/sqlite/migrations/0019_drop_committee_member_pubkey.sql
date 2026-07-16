-- 0019_drop_committee_member_pubkey.sql
-- Committee-member pubkeys are no longer stored. Proposal signatures are verified against the
-- request-sourced ADR-036 envelope pubkey, which must derive to the signer's committee account
-- address (assertSecp256k1AddressMatches) — so a per-member stored pubkey served no verification
-- purpose and was inconsistently populated (empty at creation once the web stopped sending it).
-- No index references this column, so DROP COLUMN is safe.
ALTER TABLE committee_members DROP COLUMN pubkey_b64;

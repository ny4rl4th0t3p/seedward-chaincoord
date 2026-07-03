-- M0: drop the vestigial `visibility` column. It was single-valued ('ALLOWLIST') and read by
-- no gate — every launch is private-always, enforced in the domain (IsVisibleTo = committee ∪
-- allowlist). Migration 0007 already backfilled any legacy 'PUBLIC' rows to 'ALLOWLIST' before
-- this drop. The column and its Go plumbing (Visibility type/field, New() param, DTOs, event)
-- are removed together.
ALTER TABLE launches DROP COLUMN visibility;

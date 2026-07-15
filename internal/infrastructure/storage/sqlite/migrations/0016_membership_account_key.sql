-- 5b: the launch list (FindAll) gates visibility by matching the caller's address against
-- committee_members.address / allowlist.address — but those store the per-launch DISPLAY bech32
-- (underPrefix), so a caller whose wallet HRP differs from a launch's prefix never matches and
-- cannot see the launch in the list. Add an HRP-independent account key (lowercase hex of the
-- account bytes, exactly like coordinator_allowlist.address) to match on; keep `address` for display.
-- Existing rows are populated by the startup backfill (backfillLaunchScopedAddresses re-saves every
-- launch), which now also writes `account`.
ALTER TABLE committee_members ADD COLUMN account TEXT NOT NULL DEFAULT '';
ALTER TABLE allowlist         ADD COLUMN account TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_committee_members_account ON committee_members (account);
CREATE INDEX idx_allowlist_account         ON allowlist (account);

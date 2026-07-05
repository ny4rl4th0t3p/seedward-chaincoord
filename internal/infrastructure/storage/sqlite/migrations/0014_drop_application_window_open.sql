-- Purge the vestigial `application_window_open` column. It was persisted, serialized, and
-- format-guarded (window <= gentx_deadline), but read by no logic — OpenWindow() ignored it and
-- nothing gated on it (dead-declarative, surfaced during bridge B0). The column and its Go plumbing
-- (ChainRecord.ApplicationWindowOpen field, the window<=deadline guard, and the DTO field) are
-- removed together. It sits in the original 0001 block, so the SELECT * positional scan drops one
-- target at that position; the migration-appended columns (0002 bech32, 0011 bridge fields) are
-- unaffected.
ALTER TABLE launches DROP COLUMN application_window_open;

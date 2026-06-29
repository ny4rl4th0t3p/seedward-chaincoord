-- Migration 0006: status-aware join request dedup (D4).
-- Dedup is keyed on the validator identity + status, with PENDING-supersede semantics:
-- a new submission for a validator with a PENDING request supersedes it; an APPROVED
-- request is locked; REJECTED/EXPIRED never blocks a fresh submission. The uniqueness
-- constraints must therefore apply only to ACTIVE (non-terminal) rows, so terminal
-- rows neither block re-submission nor collide.
--
-- 'PENDING' and 'APPROVED' are the active statuses; 'REJECTED' and 'EXPIRED' are terminal.

-- Consensus-pubkey uniqueness: previously all-status (migration 0001), which permanently
-- burned a validator's consensus key after a rejection. Make it partial so it guards only
-- the genesis-relevant (active) rows: no two active requests in a launch share a consensus key.
DROP INDEX idx_jr_consensus_pubkey;
CREATE UNIQUE INDEX idx_jr_consensus_pubkey
    ON join_requests (launch_id, consensus_pubkey)
    WHERE status IN ('PENDING', 'APPROVED');

-- One active request per validator per launch (operator_address now holds the validator,
-- migration 0005). Enforces D4's "at most one PENDING/APPROVED per validator" racelessly;
-- the non-unique idx_jr_operator (0001) remains for general validator-keyed lookups.
CREATE UNIQUE INDEX idx_jr_active_validator
    ON join_requests (launch_id, operator_address)
    WHERE status IN ('PENDING', 'APPROVED');

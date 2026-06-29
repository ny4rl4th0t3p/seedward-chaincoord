-- Migration 0005: join request identity.
-- Separate the request submitter (signer) from the validator operator (the gentx self-delegator).
-- From here on, operator_address holds the VALIDATOR operator account (set from the verified
-- gentx, not the request signer); the new submitter_address holds the request signer. The existing
-- idx_jr_operator therefore now indexes the validator identity.
--
-- POC reset: pre-existing rows are NOT backfilled. On old rows operator_address still holds the
-- legacy submitter value, so validator-keyed reads will not find them — dev databases are recreated
-- rather than migrated. (Deriving the validator address requires decoding each stored gentx with
-- its launch's bech32 prefix, which is Go; the SQL migration runner cannot do it.)

ALTER TABLE join_requests ADD COLUMN submitter_address TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_jr_submitter ON join_requests (launch_id, submitter_address);

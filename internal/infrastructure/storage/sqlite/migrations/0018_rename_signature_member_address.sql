-- Terminology alignment: a proposal signature is cast by a *committee member*, not a "coordinator"
-- (which now means only the allowlisted party permitted to create a launch — see roles.md). Rename the
-- proposal_signatures column to match the domain field (SignatureEntry.MemberAddress) and the wire field
-- (member_address). The column is part of the composite primary key (proposal_id, coordinator_address);
-- SQLite (>= 3.25) rewrites the PRIMARY KEY reference as part of RENAME COLUMN, so no table rebuild is
-- needed.
ALTER TABLE proposal_signatures RENAME COLUMN coordinator_address TO member_address;

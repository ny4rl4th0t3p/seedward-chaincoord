-- Migration 0004: file-level allocation governance (DEC-18)
-- Supersedes per-entry genesis-account governance with per-file allocation
-- governance. The file bytes (or attestor URL+hash) live in the AllocationStore
-- on the filesystem; this table holds only the per-file governance metadata
-- (one row per allocation type per launch).

DROP TABLE IF EXISTS launch_genesis_accounts;

CREATE TABLE launch_allocation_files
(
    launch_id            TEXT NOT NULL REFERENCES launches (id) ON DELETE CASCADE,
    alloc_type           TEXT NOT NULL, -- accounts | claims | grants | authz | feegrant
    sha256               TEXT NOT NULL,
    status               TEXT NOT NULL, -- PENDING | APPROVED | REJECTED
    approved_by_proposal TEXT,          -- proposal id; NULL unless status = APPROVED
    uploaded_at          TEXT NOT NULL,
    PRIMARY KEY (launch_id, alloc_type)
);
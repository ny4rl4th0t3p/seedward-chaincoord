-- Bridge (B3): rehearsal attempts — coordd's record that it served a specific approved input set
-- for a launch. An attempt is the anti-fabrication anchor: a result write-back must reference an
-- attempt coordd minted, so coordd never stores an input_set_hash it did not itself produce.
-- Identity is (launch_id, input_set_hash) — get-or-create; the same input set maps to one attempt.
--
-- The lease columns (status/claimed_at/lease_expires_at/runner_id) are INERT in B3; they carry the
-- claim-before-run lease enforced in B3.5. Shipped now so B3.5 needs no migration.
CREATE TABLE rehearsal_attempts (
    id               TEXT PRIMARY KEY,
    launch_id        TEXT NOT NULL,
    input_set_hash   TEXT NOT NULL,
    issued_at        TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'OPEN',
    claimed_at       TEXT,
    lease_expires_at TEXT,
    runner_id        TEXT NOT NULL DEFAULT '',
    UNIQUE (launch_id, input_set_hash)
);

CREATE INDEX idx_rehearsal_attempts_launch ON rehearsal_attempts (launch_id);

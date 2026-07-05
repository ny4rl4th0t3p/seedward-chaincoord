-- Bridge (B3): rehearsal results — stored, signature-verified result facts (§4), each bound to the
-- attempt it ran against. `stale` marks that the attempt's input set is no longer the launch's
-- current one (genuine result, drifted inputs). UNIQUE(signature) makes result write-back idempotent.
CREATE TABLE rehearsal_results (
    id              TEXT PRIMARY KEY,
    attempt_id      TEXT NOT NULL,
    launch_id       TEXT NOT NULL,
    input_set_hash  TEXT NOT NULL,
    outcome         TEXT NOT NULL,
    failed_step     TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    steps           TEXT NOT NULL DEFAULT '[]',
    engine_version  TEXT NOT NULL DEFAULT '',
    binary_name     TEXT NOT NULL DEFAULT '',
    binary_version  TEXT NOT NULL DEFAULT '',
    binary_sha256   TEXT NOT NULL DEFAULT '',
    validators      INTEGER NOT NULL DEFAULT 0,
    blocks_advanced INTEGER NOT NULL DEFAULT 0,
    started_at      TEXT NOT NULL DEFAULT '',
    finished_at     TEXT NOT NULL DEFAULT '',
    service_pubkey  TEXT NOT NULL DEFAULT '',
    signature       TEXT NOT NULL,
    stale           INTEGER NOT NULL DEFAULT 0,
    recorded_at     TEXT NOT NULL,
    UNIQUE (signature)
);

CREATE INDEX idx_rehearsal_results_launch ON rehearsal_results (launch_id, recorded_at);

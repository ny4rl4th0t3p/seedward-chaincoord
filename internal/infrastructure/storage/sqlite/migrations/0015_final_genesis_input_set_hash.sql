-- Bind the uploaded final genesis to the approved input set it was assembled from. Re-checked at
-- PUBLISH_GENESIS so a genesis that no longer matches the current approved validators cannot be
-- finalized (the approve/remove set can still change in WINDOW_CLOSED). Appended column.
ALTER TABLE launches ADD COLUMN final_genesis_input_set_hash TEXT NOT NULL DEFAULT '';

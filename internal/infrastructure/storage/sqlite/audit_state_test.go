package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditStateStore_LoadPrevHash_EmptyReturnsZeroValue(t *testing.T) {
	db := openTestDB(t)
	store := NewAuditStateStore(db)

	hash, err := store.LoadPrevHash(context.Background())
	require.NoError(t, err, "loading with no row must not error")
	assert.Empty(t, hash, "an unseeded chain tip is the empty string")
}

func TestAuditStateStore_SaveThenLoad(t *testing.T) {
	db := openTestDB(t)
	store := NewAuditStateStore(db)
	ctx := context.Background()

	const want = "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"
	require.NoError(t, store.SavePrevHash(ctx, want))

	got, err := store.LoadPrevHash(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestAuditStateStore_SavePrevHash_UpsertsSingleRow(t *testing.T) {
	db := openTestDB(t)
	store := NewAuditStateStore(db)
	ctx := context.Background()

	require.NoError(t, store.SavePrevHash(ctx, "first"))
	require.NoError(t, store.SavePrevHash(ctx, "second"))

	got, err := store.LoadPrevHash(ctx)
	require.NoError(t, err)
	assert.Equal(t, "second", got, "SavePrevHash must upsert the single chain-tip row, not append")

	// The upsert must keep exactly one row (id = 1).
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_state`).Scan(&count))
	assert.Equal(t, 1, count, "audit_state must hold a single row")
}

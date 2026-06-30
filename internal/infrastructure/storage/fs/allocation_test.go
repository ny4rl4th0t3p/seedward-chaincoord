package fs_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	fstore "github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/storage/fs"
)

const (
	allocURL    = "https://example.com/accounts.csv"
	allocSHA256 = "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"
)

func newAllocationStore(t *testing.T) *fstore.AllocationStore {
	t.Helper()
	s, err := fstore.NewAllocationStore(t.TempDir())
	require.NoError(t, err)
	return s
}

// --- host mode: Save / GetRef ---

func TestAllocationStore_Save_HostMode(t *testing.T) {
	t.Parallel()
	s := newAllocationStore(t)
	ctx := context.Background()
	data := []byte("address,amount\ncosmos1abc,1000\n")

	require.NoError(t, s.Save(ctx, "launch-1", "accounts", data))

	ref, err := s.GetRef(ctx, "launch-1", "accounts")
	require.NoError(t, err)
	require.NotEmpty(t, ref.LocalPath, "host-mode ref must carry a LocalPath")
	assert.Empty(t, ref.ExternalURL, "host-mode ref must not carry an ExternalURL")

	got, err := os.ReadFile(ref.LocalPath)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestAllocationStore_Save_ReadOnlyBaseDirFails(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("permission checks do not apply when running as root")
	}
	base := t.TempDir()
	s, err := fstore.NewAllocationStore(base)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(base, 0o500))
	t.Cleanup(func() { _ = os.Chmod(base, 0o700) })

	err = s.Save(context.Background(), "launch-perm", "accounts", []byte("data"))
	require.Error(t, err, "writing under a read-only base dir must fail")
}

// --- attestor mode: SaveRef / GetRef ---

func TestAllocationStore_SaveRef_AttestorMode(t *testing.T) {
	t.Parallel()
	s := newAllocationStore(t)
	ctx := context.Background()

	require.NoError(t, s.SaveRef(ctx, "launch-1", "accounts", allocURL, allocSHA256))

	ref, err := s.GetRef(ctx, "launch-1", "accounts")
	require.NoError(t, err)
	assert.Equal(t, allocURL, ref.ExternalURL)
	assert.Equal(t, allocSHA256, ref.SHA256)
	assert.Empty(t, ref.LocalPath, "attestor ref must not carry a LocalPath")
}

// --- GetRef: not found + precedence ---

func TestAllocationStore_GetRef_NotFound(t *testing.T) {
	t.Parallel()
	s := newAllocationStore(t)

	_, err := s.GetRef(context.Background(), "nonexistent", "accounts")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestAllocationStore_GetRef_RefTakesPriorityOverData(t *testing.T) {
	t.Parallel()
	s := newAllocationStore(t)
	ctx := context.Background()

	// Host-mode bytes first, then an attestor ref for the same type.
	require.NoError(t, s.Save(ctx, "launch-1", "accounts", []byte("address,amount\n")))
	require.NoError(t, s.SaveRef(ctx, "launch-1", "accounts", allocURL, allocSHA256))

	ref, err := s.GetRef(ctx, "launch-1", "accounts")
	require.NoError(t, err)
	assert.Equal(t, allocURL, ref.ExternalURL, "the .ref sidecar must take priority over the .data file")
	assert.Empty(t, ref.LocalPath)
}

// --- distinct allocation types are stored independently ---

func TestAllocationStore_TypesAreIndependent(t *testing.T) {
	t.Parallel()
	s := newAllocationStore(t)
	ctx := context.Background()

	require.NoError(t, s.Save(ctx, "launch-1", "accounts", []byte("accounts-data")))
	require.NoError(t, s.SaveRef(ctx, "launch-1", "claims", allocURL, allocSHA256))

	accountsRef, err := s.GetRef(ctx, "launch-1", "accounts")
	require.NoError(t, err)
	assert.NotEmpty(t, accountsRef.LocalPath, "accounts is host-mode")

	claimsRef, err := s.GetRef(ctx, "launch-1", "claims")
	require.NoError(t, err)
	assert.Equal(t, allocURL, claimsRef.ExternalURL, "claims is attestor-mode")
}

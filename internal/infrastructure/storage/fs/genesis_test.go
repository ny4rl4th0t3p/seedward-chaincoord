package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	fstore "github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/storage/fs"
)

func TestNewGenesisStore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseDir func(t *testing.T) string
		wantErr bool
		rootOK  bool
	}{
		{
			name: "creates base dir when it does not exist",
			baseDir: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "genesis")
			},
			rootOK: true,
		},
		{
			name: "returns error when parent dir is read-only",
			baseDir: func(t *testing.T) string {
				parent := t.TempDir()
				require.NoError(t, os.Chmod(parent, 0o500))
				t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
				return filepath.Join(parent, "sub")
			},
			wantErr: true,
			rootOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !tc.rootOK && os.Getuid() == 0 {
				t.Skip("permission checks do not apply when running as root")
			}
			dir := tc.baseDir(t)
			_, err := fstore.NewGenesisStore(dir)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			_, statErr := os.Stat(dir)
			require.NoError(t, statErr, "expected base dir to exist")
		})
	}
}

// --- Option C (host mode): SaveInitial / GetInitialRef ---

func TestGenesisStore_SaveInitial(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		launchID string
		data     []byte
		setup    func(t *testing.T, base string)
		wantErr  bool
		rootOK   bool
	}{
		{
			name:     "saves data successfully",
			launchID: "launch-1",
			data:     []byte(`{"chain_id":"test-1"}`),
			rootOK:   true,
		},
		{
			name:     "returns error when base dir is read-only",
			launchID: "launch-perm",
			data:     []byte(`{}`),
			setup: func(t *testing.T, base string) {
				require.NoError(t, os.Chmod(base, 0o500))
				t.Cleanup(func() { _ = os.Chmod(base, 0o700) })
			},
			wantErr: true,
			rootOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !tc.rootOK && os.Getuid() == 0 {
				t.Skip("permission checks do not apply when running as root")
			}
			base := t.TempDir()
			s, err := fstore.NewGenesisStore(base)
			require.NoError(t, err)
			if tc.setup != nil {
				tc.setup(t, base)
			}
			err = s.SaveInitial(context.Background(), tc.launchID, tc.data)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestGenesisStore_GetInitialRef_HostMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		seed     []byte
		launchID string
		wantErr  error
	}{
		{
			name:     "returns LocalPath for previously saved bytes",
			seed:     []byte(`{"chain_id":"test-1"}`),
			launchID: "launch-1",
		},
		{
			name:     "returns ErrNotFound when launch does not exist",
			launchID: "nonexistent",
			wantErr:  ports.ErrNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := fstore.NewGenesisStore(t.TempDir())
			require.NoError(t, err)
			ctx := context.Background()
			if tc.seed != nil {
				require.NoError(t, s.SaveInitial(ctx, tc.launchID, tc.seed))
			}
			ref, err := s.GetInitialRef(ctx, tc.launchID)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, ref.LocalPath, "expected non-empty LocalPath for host-mode ref")
			assert.Empty(t, ref.ExternalURL, "expected empty ExternalURL for host-mode ref")
			got, err := os.ReadFile(ref.LocalPath)
			require.NoError(t, err, "reading file at LocalPath")
			assert.Equal(t, tc.seed, got)
		})
	}
}

// --- Option C (host mode): SaveFinal / GetFinalRef ---

func TestGenesisStore_SaveFinal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		launchID string
		data     []byte
		setup    func(t *testing.T, base string)
		wantErr  bool
		rootOK   bool
	}{
		{
			name:     "saves data successfully",
			launchID: "launch-1",
			data:     []byte(`{"chain_id":"test-1","genesis_time":"2026-01-01T00:00:00Z"}`),
			rootOK:   true,
		},
		{
			name:     "returns error when base dir is read-only",
			launchID: "launch-perm",
			data:     []byte(`{}`),
			setup: func(t *testing.T, base string) {
				require.NoError(t, os.Chmod(base, 0o500))
				t.Cleanup(func() { _ = os.Chmod(base, 0o700) })
			},
			wantErr: true,
			rootOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !tc.rootOK && os.Getuid() == 0 {
				t.Skip("permission checks do not apply when running as root")
			}
			base := t.TempDir()
			s, err := fstore.NewGenesisStore(base)
			require.NoError(t, err)
			if tc.setup != nil {
				tc.setup(t, base)
			}
			err = s.SaveFinal(context.Background(), tc.launchID, tc.data)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestGenesisStore_GetFinalRef_HostMode(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	data := []byte(`{"chain_id":"test-1","genesis_time":"2026-01-01T00:00:00Z"}`)
	require.NoError(t, s.SaveFinal(ctx, "launch-1", data))

	ref, err := s.GetFinalRef(ctx, "launch-1")
	require.NoError(t, err)
	require.NotEmpty(t, ref.LocalPath, "expected non-empty LocalPath")
	got, err := os.ReadFile(ref.LocalPath)
	require.NoError(t, err, "reading file")
	assert.Equal(t, data, got)
}

func TestGenesisStore_GetFinalRef_NotFound(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	require.NoError(t, err)
	_, err = s.GetFinalRef(context.Background(), "nonexistent")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

// --- Option A (attestor mode): SaveInitialRef / GetInitialRef ---

func TestGenesisStore_SaveInitialRef(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	const url = "https://example.com/genesis.json"
	const sha256 = "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"

	require.NoError(t, s.SaveInitialRef(ctx, "launch-1", url, sha256))

	ref, err := s.GetInitialRef(ctx, "launch-1")
	require.NoError(t, err)
	assert.Equal(t, url, ref.ExternalURL)
	assert.Equal(t, sha256, ref.SHA256)
	assert.Empty(t, ref.LocalPath, "LocalPath should be empty for attestor ref")
}

func TestGenesisStore_SaveFinalRef(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	const url = "https://cdn.example.com/final-genesis.json"
	const sha256 = "b4e0c83d2e5f9016b7c3d4e5f6789012a2b3c4d5e6f7890123456789012345bc"

	require.NoError(t, s.SaveFinalRef(ctx, "launch-1", url, sha256))

	ref, err := s.GetFinalRef(ctx, "launch-1")
	require.NoError(t, err)
	assert.Equal(t, url, ref.ExternalURL)
	assert.Equal(t, sha256, ref.SHA256)
}

// --- Precedence: .ref sidecar takes priority over .json file ---

func TestGenesisStore_RefTakesPriorityOverFile(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()

	// Store raw bytes first (Option C).
	require.NoError(t, s.SaveInitial(ctx, "launch-1", []byte(`{"chain_id":"test-1"}`)))
	// Then register an external ref (Option A).
	const url = "https://example.com/genesis.json"
	const sha256 = "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"
	require.NoError(t, s.SaveInitialRef(ctx, "launch-1", url, sha256))

	// GetInitialRef must return the external ref, not the local file.
	ref, err := s.GetInitialRef(ctx, "launch-1")
	require.NoError(t, err)
	assert.Equal(t, url, ref.ExternalURL, "expected the external ref, got LocalPath=%q", ref.LocalPath)
}

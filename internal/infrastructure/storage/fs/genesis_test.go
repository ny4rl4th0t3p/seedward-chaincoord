package fs_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
				if err := os.Chmod(parent, 0o500); err != nil {
					t.Fatal(err)
				}
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
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewGenesisStore() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if _, err := os.Stat(dir); err != nil {
					t.Fatalf("expected base dir to exist: %v", err)
				}
			}
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
				if err := os.Chmod(base, 0o500); err != nil {
					t.Fatal(err)
				}
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
			if err != nil {
				t.Fatal(err)
			}
			if tc.setup != nil {
				tc.setup(t, base)
			}
			err = s.SaveInitial(context.Background(), tc.launchID, tc.data)
			if (err != nil) != tc.wantErr {
				t.Fatalf("SaveInitial() error = %v, wantErr %v", err, tc.wantErr)
			}
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
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if tc.seed != nil {
				if err := s.SaveInitial(ctx, tc.launchID, tc.seed); err != nil {
					t.Fatal(err)
				}
			}
			ref, err := s.GetInitialRef(ctx, tc.launchID)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetInitialRef() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if ref.LocalPath == "" {
					t.Fatal("expected non-empty LocalPath for host-mode ref")
				}
				if ref.ExternalURL != "" {
					t.Errorf("expected empty ExternalURL for host-mode ref, got %q", ref.ExternalURL)
				}
				got, err := os.ReadFile(ref.LocalPath)
				if err != nil {
					t.Fatalf("reading file at LocalPath: %v", err)
				}
				if !bytes.Equal(got, tc.seed) {
					t.Fatalf("file contents: got %q, want %q", got, tc.seed)
				}
			}
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
				if err := os.Chmod(base, 0o500); err != nil {
					t.Fatal(err)
				}
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
			if err != nil {
				t.Fatal(err)
			}
			if tc.setup != nil {
				tc.setup(t, base)
			}
			err = s.SaveFinal(context.Background(), tc.launchID, tc.data)
			if (err != nil) != tc.wantErr {
				t.Fatalf("SaveFinal() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestGenesisStore_GetFinalRef_HostMode(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	data := []byte(`{"chain_id":"test-1","genesis_time":"2026-01-01T00:00:00Z"}`)
	if err := s.SaveFinal(ctx, "launch-1", data); err != nil {
		t.Fatal(err)
	}

	ref, err := s.GetFinalRef(ctx, "launch-1")
	if err != nil {
		t.Fatalf("GetFinalRef() error = %v", err)
	}
	if ref.LocalPath == "" {
		t.Fatal("expected non-empty LocalPath")
	}
	got, err := os.ReadFile(ref.LocalPath)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("contents: got %q, want %q", got, data)
	}
}

func TestGenesisStore_GetFinalRef_NotFound(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetFinalRef(context.Background(), "nonexistent")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// --- Option A (attestor mode): SaveInitialRef / GetInitialRef ---

func TestGenesisStore_SaveInitialRef(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const url = "https://example.com/genesis.json"
	const sha256 = "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"

	if err := s.SaveInitialRef(ctx, "launch-1", url, sha256); err != nil {
		t.Fatalf("SaveInitialRef() error = %v", err)
	}

	ref, err := s.GetInitialRef(ctx, "launch-1")
	if err != nil {
		t.Fatalf("GetInitialRef() error = %v", err)
	}
	if ref.ExternalURL != url {
		t.Errorf("ExternalURL: got %q, want %q", ref.ExternalURL, url)
	}
	if ref.SHA256 != sha256 {
		t.Errorf("SHA256: got %q, want %q", ref.SHA256, sha256)
	}
	if ref.LocalPath != "" {
		t.Errorf("LocalPath should be empty for attestor ref, got %q", ref.LocalPath)
	}
}

func TestGenesisStore_SaveFinalRef(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const url = "https://cdn.example.com/final-genesis.json"
	const sha256 = "b4e0c83d2e5f9016b7c3d4e5f6789012a2b3c4d5e6f7890123456789012345bc"

	if err := s.SaveFinalRef(ctx, "launch-1", url, sha256); err != nil {
		t.Fatalf("SaveFinalRef() error = %v", err)
	}

	ref, err := s.GetFinalRef(ctx, "launch-1")
	if err != nil {
		t.Fatalf("GetFinalRef() error = %v", err)
	}
	if ref.ExternalURL != url {
		t.Errorf("ExternalURL: got %q, want %q", ref.ExternalURL, url)
	}
	if ref.SHA256 != sha256 {
		t.Errorf("SHA256: got %q, want %q", ref.SHA256, sha256)
	}
}

// --- Precedence: .ref sidecar takes priority over .json file ---

func TestGenesisStore_RefTakesPriorityOverFile(t *testing.T) {
	t.Parallel()

	s, err := fstore.NewGenesisStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Store raw bytes first (Option C).
	if err := s.SaveInitial(ctx, "launch-1", []byte(`{"chain_id":"test-1"}`)); err != nil {
		t.Fatal(err)
	}
	// Then register an external ref (Option A).
	const url = "https://example.com/genesis.json"
	const sha256 = "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"
	if err := s.SaveInitialRef(ctx, "launch-1", url, sha256); err != nil {
		t.Fatal(err)
	}

	// GetInitialRef must return the external ref, not the local file.
	ref, err := s.GetInitialRef(ctx, "launch-1")
	if err != nil {
		t.Fatalf("GetInitialRef() error = %v", err)
	}
	if ref.ExternalURL != url {
		t.Errorf("expected ExternalURL %q, got %q (LocalPath=%q)", url, ref.ExternalURL, ref.LocalPath)
	}
}

package fs

import (
	"context"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// GenesisStore is a filesystem-backed implementation of ports.GenesisStore.
//
// Option C (host mode) files are stored under:
//
//	<baseDir>/<launchID>/initial.json
//	<baseDir>/<launchID>/final.json
//
// Option A (attestor mode) references are stored as small JSON sidecars under:
//
//	<baseDir>/<launchID>/initial.ref
//	<baseDir>/<launchID>/final.ref
//
// On GetInitialRef / GetFinalRef the .ref sidecar is checked first; if absent
// the .json file is checked; if neither exists ErrNotFound is returned.
type GenesisStore struct {
	fileStore
}

// NewGenesisStore returns a GenesisStore rooted at baseDir.
// The directory is created if it does not exist.
func NewGenesisStore(baseDir string) (*GenesisStore, error) {
	fs, err := newFileStore(baseDir, "genesis store")
	if err != nil {
		return nil, err
	}
	return &GenesisStore{fs}, nil
}

// SaveInitial stores the raw initial genesis bytes (Option C).
func (s *GenesisStore) SaveInitial(_ context.Context, launchID string, data []byte) error {
	return s.writeBytes(launchID, "initial.json", data)
}

// SaveFinal stores the raw final genesis bytes (Option C).
func (s *GenesisStore) SaveFinal(_ context.Context, launchID string, data []byte) error {
	return s.writeBytes(launchID, "final.json", data)
}

// SaveInitialRef records an external URL reference for the initial genesis (Option A).
func (s *GenesisStore) SaveInitialRef(_ context.Context, launchID, url, sha256 string) error {
	return s.writeRef(launchID, "initial.ref", url, sha256)
}

// SaveFinalRef records an external URL reference for the final genesis (Option A).
func (s *GenesisStore) SaveFinalRef(_ context.Context, launchID, url, sha256 string) error {
	return s.writeRef(launchID, "final.ref", url, sha256)
}

// GetInitialRef returns how to serve the initial genesis file.
func (s *GenesisStore) GetInitialRef(_ context.Context, launchID string) (*ports.StoredFileRef, error) {
	return s.getRef(launchID, "initial.ref", "initial.json")
}

// GetFinalRef returns how to serve the final genesis file.
func (s *GenesisStore) GetFinalRef(_ context.Context, launchID string) (*ports.StoredFileRef, error) {
	return s.getRef(launchID, "final.ref", "final.json")
}

package fs

import (
	"context"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// AllocationStore is a filesystem-backed implementation of ports.AllocationStore.
// It mirrors GenesisStore's dual-mode layout, one file per allocation type per launch:
//
//	<baseDir>/<launchID>/alloc-<type>.data   (host mode, raw opaque bytes)
//	<baseDir>/<launchID>/alloc-<type>.ref    (attestor mode, URL + sha256 sidecar)
//
// The content is opaque (gentool emits CSV/TSV, not JSON), so the host-mode file uses a
// format-neutral .data extension. On GetRef the .ref sidecar is checked first; if absent
// the .data file is checked; if neither exists ErrNotFound is returned.
type AllocationStore struct {
	fileStore
}

// NewAllocationStore returns an AllocationStore rooted at baseDir.
// The directory is created if it does not exist.
func NewAllocationStore(baseDir string) (*AllocationStore, error) {
	fs, err := newFileStore(baseDir, "allocation store")
	if err != nil {
		return nil, err
	}
	return &AllocationStore{fs}, nil
}

// Save stores the raw allocation-file bytes for allocType (host mode).
func (s *AllocationStore) Save(_ context.Context, launchID, allocType string, data []byte) error {
	return s.writeBytes(launchID, "alloc-"+allocType+".data", data)
}

// SaveRef records an external URL reference for allocType (attestor mode).
func (s *AllocationStore) SaveRef(_ context.Context, launchID, allocType, url, sha256 string) error {
	return s.writeRef(launchID, "alloc-"+allocType+".ref", url, sha256)
}

// GetRef returns how to serve the file of the given type.
func (s *AllocationStore) GetRef(_ context.Context, launchID, allocType string) (*ports.StoredFileRef, error) {
	return s.getRef(launchID, "alloc-"+allocType+".ref", "alloc-"+allocType+".data")
}

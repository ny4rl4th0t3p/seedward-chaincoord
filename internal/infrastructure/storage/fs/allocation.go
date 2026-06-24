package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	baseDir string
}

// NewAllocationStore returns an AllocationStore rooted at baseDir.
// The directory is created if it does not exist.
func NewAllocationStore(baseDir string) (*AllocationStore, error) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, fmt.Errorf("allocation store: creating base dir: %w", err)
	}
	return &AllocationStore{baseDir: baseDir}, nil
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

func (s *AllocationStore) getRef(launchID, refName, jsonName string) (*ports.StoredFileRef, error) {
	refPath := filepath.Join(s.baseDir, launchID, refName)
	raw, err := os.ReadFile(refPath)
	if err == nil {
		var f genesisRefFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("allocation store: parsing ref sidecar: %w", err)
		}
		return &ports.StoredFileRef{ExternalURL: f.URL, SHA256: f.SHA256}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("allocation store: reading ref sidecar: %w", err)
	}

	jsonPath := filepath.Join(s.baseDir, launchID, jsonName)
	if _, err := os.Stat(jsonPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ports.ErrNotFound
		}
		return nil, fmt.Errorf("allocation store: stating %s: %w", jsonName, err)
	}
	return &ports.StoredFileRef{LocalPath: jsonPath}, nil
}

func (s *AllocationStore) writeBytes(launchID, filename string, data []byte) error {
	dir := filepath.Join(s.baseDir, launchID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("allocation store: creating launch dir: %w", err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("allocation store: writing %s: %w", filename, err)
	}
	return nil
}

func (s *AllocationStore) writeRef(launchID, filename, url, sha256 string) error {
	dir := filepath.Join(s.baseDir, launchID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("allocation store: creating launch dir: %w", err)
	}
	data, err := json.Marshal(genesisRefFile{URL: url, SHA256: sha256})
	if err != nil {
		return fmt.Errorf("allocation store: marshaling ref: %w", err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("allocation store: writing ref %s: %w", filename, err)
	}
	return nil
}

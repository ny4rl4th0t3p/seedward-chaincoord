package fs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// fileStore is the shared dual-mode filesystem logic behind GenesisStore and
// AllocationStore. For each stored file under <baseDir>/<launchID>/:
//
//   - host mode: raw bytes at the file name.
//   - attestor mode: a small JSON sidecar (url + sha256) at the .ref name.
//
// label prefixes error messages (e.g. "genesis store" / "allocation store").
type fileStore struct {
	baseDir string
	label   string
}

// refFile is the on-disk JSON format for an attestor-mode (Option A) sidecar.
type refFile struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// newFileStore creates the base directory and returns a fileStore.
func newFileStore(baseDir, label string) (fileStore, error) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return fileStore{}, fmt.Errorf("%s: creating base dir: %w", label, err)
	}
	return fileStore{baseDir: baseDir, label: label}, nil
}

func (s fileStore) writeBytes(launchID, filename string, data []byte) error {
	dir := filepath.Join(s.baseDir, launchID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%s: creating launch dir: %w", s.label, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0o600); err != nil {
		return fmt.Errorf("%s: writing %s: %w", s.label, filename, err)
	}
	return nil
}

func (s fileStore) writeRef(launchID, filename, url, sha256 string) error {
	dir := filepath.Join(s.baseDir, launchID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%s: creating launch dir: %w", s.label, err)
	}
	data, err := json.Marshal(refFile{URL: url, SHA256: sha256})
	if err != nil {
		return fmt.Errorf("%s: marshaling ref: %w", s.label, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0o600); err != nil {
		return fmt.Errorf("%s: writing ref %s: %w", s.label, filename, err)
	}
	return nil
}

// getRef checks for a .ref sidecar first (attestor mode), then the host-mode file;
// returns ErrNotFound if neither exists.
func (s fileStore) getRef(launchID, refName, fileName string) (*ports.StoredFileRef, error) {
	raw, err := os.ReadFile(filepath.Join(s.baseDir, launchID, refName))
	if err == nil {
		var f refFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("%s: parsing ref sidecar: %w", s.label, err)
		}
		return &ports.StoredFileRef{ExternalURL: f.URL, SHA256: f.SHA256}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: reading ref sidecar: %w", s.label, err)
	}

	filePath := filepath.Join(s.baseDir, launchID, fileName)
	if _, err := os.Stat(filePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ports.ErrNotFound
		}
		return nil, fmt.Errorf("%s: stating %s: %w", s.label, fileName, err)
	}
	return &ports.StoredFileRef{LocalPath: filePath}, nil
}

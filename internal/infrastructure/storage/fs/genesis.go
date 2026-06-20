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
	baseDir string
}

// genesisRef is the on-disk JSON format for Option A sidecars.
type genesisRefFile struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// NewGenesisStore returns a GenesisStore rooted at baseDir.
// The directory is created if it does not exist.
func NewGenesisStore(baseDir string) (*GenesisStore, error) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, fmt.Errorf("genesis store: creating base dir: %w", err)
	}
	return &GenesisStore{baseDir: baseDir}, nil
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
func (s *GenesisStore) GetInitialRef(_ context.Context, launchID string) (*ports.GenesisRef, error) {
	return s.getRef(launchID, "initial.ref", "initial.json")
}

// GetFinalRef returns how to serve the final genesis file.
func (s *GenesisStore) GetFinalRef(_ context.Context, launchID string) (*ports.GenesisRef, error) {
	return s.getRef(launchID, "final.ref", "final.json")
}

// getRef checks for a .ref sidecar first (Option A), then a .json file (Option C).
func (s *GenesisStore) getRef(launchID, refName, jsonName string) (*ports.GenesisRef, error) {
	refPath := filepath.Join(s.baseDir, launchID, refName)
	raw, err := os.ReadFile(refPath)
	if err == nil {
		var f genesisRefFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("genesis store: parsing ref sidecar: %w", err)
		}
		return &ports.GenesisRef{ExternalURL: f.URL, SHA256: f.SHA256}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("genesis store: reading ref sidecar: %w", err)
	}

	// No sidecar — check for a local file (Option C).
	jsonPath := filepath.Join(s.baseDir, launchID, jsonName)
	if _, err := os.Stat(jsonPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ports.ErrNotFound
		}
		return nil, fmt.Errorf("genesis store: stating %s: %w", jsonName, err)
	}
	return &ports.GenesisRef{LocalPath: jsonPath}, nil
}

func (s *GenesisStore) writeBytes(launchID, filename string, data []byte) error {
	dir := filepath.Join(s.baseDir, launchID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("genesis store: creating launch dir: %w", err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("genesis store: writing %s: %w", filename, err)
	}
	return nil
}

func (s *GenesisStore) writeRef(launchID, filename, url, sha256 string) error {
	dir := filepath.Join(s.baseDir, launchID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("genesis store: creating launch dir: %w", err)
	}
	data, err := json.Marshal(genesisRefFile{URL: url, SHA256: sha256})
	if err != nil {
		return fmt.Errorf("genesis store: marshaling ref: %w", err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("genesis store: writing ref %s: %w", filename, err)
	}
	return nil
}

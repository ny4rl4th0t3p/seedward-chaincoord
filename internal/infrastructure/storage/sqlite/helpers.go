package sqlite

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// nowUTC is a package-level helper that returns the current time in UTC.
func nowUTC() time.Time { return time.Now().UTC() }

// randomHex generates a cryptographically random hex string of length 2n.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// timeToStr serializes a time.Time to RFC3339Nano UTC for storage.
func timeToStr(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// strToTime parses an RFC3339(Nano) string from storage.
func strToTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Fallback for rows stored without sub-second precision.
		t, err = time.Parse(time.RFC3339, s)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", s, err)
	}
	return t.UTC(), nil
}

// nullTimeToStr converts a nullable time pointer to a nullable string pointer.
func nullTimeToStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := timeToStr(*t)
	return &s
}

// nullStrToTime converts a nullable string pointer (from a SQL NULL column) to a time pointer.
func nullStrToTime(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := strToTime(*s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// uuidToStr serializes a UUID to its canonical string form.
func uuidToStr(id uuid.UUID) string {
	return id.String()
}

// strToUUID parses a UUID string from storage.
func strToUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("parse uuid %q: %w", s, err)
	}
	return id, nil
}

// nullStrToUUID parses a nullable UUID string (e.g. approved_by_proposal).
func nullStrToUUID(s *string) (*uuid.UUID, error) {
	if s == nil {
		return nil, nil
	}
	id, err := strToUUID(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// nullUUIDToStr converts a *uuid.UUID to a *string for SQL nullable columns.
func nullUUIDToStr(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

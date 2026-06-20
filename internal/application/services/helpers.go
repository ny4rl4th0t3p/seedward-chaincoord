package services

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// signedPayloadMaxAge is the maximum age (and future skew) accepted for a
// signed payload's timestamp field.  A payload whose timestamp differs from
// the server clock by more than this window is rejected as stale or invalid.
//
// Combined with the NonceStore TTL (10 min), this ensures that a captured
// payload cannot be replayed: the timestamp check rejects it after 5 minutes
// even if the nonce has since expired from the store.
const signedPayloadMaxAge = 5 * time.Minute

// validateTimestamp parses ts as RFC 3339 and returns an error if the
// timestamp is more than signedPayloadMaxAge in the past or future.
func validateTimestamp(ts string) error {
	if ts == "" {
		return fmt.Errorf("timestamp is required: %w", ports.ErrUnauthorized)
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return fmt.Errorf("timestamp is not valid RFC 3339: %w", ports.ErrUnauthorized)
	}
	diff := time.Since(t)
	if diff < 0 {
		diff = -diff
	}
	if diff > signedPayloadMaxAge {
		return fmt.Errorf("timestamp is outside the %s acceptance window: %w", signedPayloadMaxAge, ports.ErrUnauthorized)
	}
	return nil
}

// decodeBase64Sig is shared by all service files that verify signatures.
func decodeBase64Sig(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

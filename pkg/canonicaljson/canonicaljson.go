// Package canonicaljson produces canonical JSON for use in secp256k1 and Ed25519 signatures.
//
// Rules (must be identical between server and CLI):
//   - Keys sorted lexicographically at every level of nesting
//   - No whitespace
//   - UTF-8 encoding
//   - Numbers without trailing zeros
//   - Timestamps as RFC 3339 UTC with second precision ("2006-01-02T15:04:05Z")
//   - The "signature" and "nonce" fields are excluded when signing (callers must
//     strip them before passing to Marshal)
package canonicaljson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Marshal returns the canonical JSON encoding of v. v must be JSON-serialisable.
// The output has sorted keys, no whitespace, and is UTF-8 encoded.
func Marshal(v any) ([]byte, error) {
	// Round-trip through encoding/json to get a generic structure, then
	// re-serialize with sorted keys.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonicaljson: marshal: %w", err)
	}
	return canonicalise(json.RawMessage(raw))
}

// MarshalForSigning strips the "signature" and "pubkey_b64" fields from the
// top-level object before producing canonical JSON. This is the function to use
// when computing the bytes that will be signed or verified.
//
// The "nonce" is deliberately kept in the signed bytes: it binds the replay
// protection nonce to the signature, so a captured request cannot be replayed by
// swapping in a fresh nonce. (The signature itself and the public key used to
// verify it are excluded, since they are not part of the signed content.)
func MarshalForSigning(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonicaljson: marshal for signing: %w", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("canonicaljson: input must be a JSON object: %w", err)
	}

	delete(m, "signature")
	delete(m, "pubkey_b64")

	return canonicalise(m)
}

// canonicalise recursively sorts object keys and removes all whitespace.
func canonicalise(v any) ([]byte, error) {
	switch val := v.(type) {
	case map[string]json.RawMessage:
		return serialiseObject(val)

	case json.RawMessage:
		return canonicaliseRaw(val)

	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return canonicaliseRaw(raw)
	}
}

func canonicaliseRaw(raw json.RawMessage) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("canonicaljson: empty input")
	}

	switch raw[0] {
	case '{':
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return serialiseObject(m)

	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, err
		}
		return serialiseArray(arr)

	default:
		// Scalar: number, string, bool, null — re-encode via json to normalise.
		var scalar any
		if err := json.Unmarshal(raw, &scalar); err != nil {
			return nil, err
		}
		return json.Marshal(scalar)
	}
}

func serialiseObject(m map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')

		valBytes, err := canonicaliseRaw(m[k])
		if err != nil {
			return nil, err
		}
		buf.Write(valBytes)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func serialiseArray(arr []json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, elem := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		b, err := canonicaliseRaw(elem)
		if err != nil {
			return nil, err
		}
		buf.Write(b)
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

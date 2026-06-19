package canonicaljson_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/ny4rl4th0t3p/chaincoord/pkg/canonicaljson"
)

type vector struct {
	Description        string `json:"description"`
	Input              any    `json:"input"`
	Expected           string `json:"expected"`
	ExpectedForSigning string `json:"expected_for_signing"`
}

func TestVectors(t *testing.T) {
	data, err := os.ReadFile("../../testdata/canonical_json_vectors.json")
	if err != nil {
		t.Fatalf("read test vectors: %v", err)
	}

	var vectors []vector
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatalf("parse test vectors: %v", err)
	}

	for _, v := range vectors {
		t.Run(v.Description, func(t *testing.T) {
			if v.Expected != "" {
				got, err := canonicaljson.Marshal(v.Input)
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				if string(got) != v.Expected {
					t.Errorf("Marshal\n got:  %s\n want: %s", got, v.Expected)
				}
			}

			if v.ExpectedForSigning != "" {
				got, err := canonicaljson.MarshalForSigning(v.Input)
				if err != nil {
					t.Fatalf("MarshalForSigning: %v", err)
				}
				if string(got) != v.ExpectedForSigning {
					t.Errorf("MarshalForSigning\n got:  %s\n want: %s", got, v.ExpectedForSigning)
				}
			}
		})
	}
}

func TestMarshalDeterministic(t *testing.T) {
	input := map[string]any{
		"z":      "last",
		"a":      "first",
		"m":      42,
		"nested": map[string]any{"y": true, "b": false},
	}

	first, err := canonicaljson.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		got, err := canonicaljson.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, first) {
			t.Fatalf("non-deterministic output on iteration %d:\n got:  %s\n want: %s", i, got, first)
		}
	}
}

func TestMarshalForSigningStripsFields(t *testing.T) {
	input := map[string]any{
		"chain_id":         "mychain-1",
		"operator_address": "cosmos1abc",
		"signature":        "shouldberemoved",
		"nonce":            "kept-for-replay",
		"pubkey_b64":       "shouldberemoved",
	}

	got, err := canonicaljson.MarshalForSigning(input)
	if err != nil {
		t.Fatal(err)
	}

	// signature and pubkey_b64 are stripped; nonce is KEPT (bound to the signature).
	want := `{"chain_id":"mychain-1","nonce":"kept-for-replay","operator_address":"cosmos1abc"}`
	if string(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestMarshalForSigningRequiresObject(t *testing.T) {
	_, err := canonicaljson.MarshalForSigning([]int{1, 2, 3})
	if err == nil {
		t.Error("expected error for non-object input, got nil")
	}
}

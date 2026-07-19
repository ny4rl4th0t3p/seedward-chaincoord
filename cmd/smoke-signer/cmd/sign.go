package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"
)

func newSignCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sign",
		Short: "Sign a JSON payload from stdin and output the filled JSON to stdout",
		Long: `Reads a JSON payload from stdin, injects a fresh nonce and timestamp,
computes ADR-036 signing bytes (stripping signature/nonce/pubkey_b64), signs with
the derived secp256k1 key, and outputs the completed JSON.

Works for all replay-protected payload types:
  VerifyChallengeInput (auth), SubmitInput (join), ConfirmInput (readiness),
  RaiseInput (proposal create), SignInput (proposal sign).

The signer address is derived from the selected key (--key-index or --privkey-hex). The JSON must
contain either an "operator_address" or "member_address" field matching that address.
If the input JSON contains a "pubkey_b64" field it is filled with the derived
public key; otherwise it is omitted from the output.`,
		Example: `  echo '{"operator_address":"cosmos1...","challenge":"abc","nonce":"","timestamp":"","pubkey_b64":"","signature":""}' \
    | smoke-signer sign --key-index 1
  echo '{"operator_address":"cosmos1...", ...}' | smoke-signer sign --privkey-hex <hex>`,
		RunE: runSign,
	}
	addKeySelectionFlags(cmd)
	return cmd
}

func runSign(cmd *cobra.Command, _ []string) error {
	priv, err := resolvePrivKey(cmd)
	if err != nil {
		return err
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	// Resolve signer address from the key (default HRP cosmos — smoke test + demo use cosmos keys).
	signerAddr, err := deriveAddress(priv, "cosmos")
	if err != nil {
		return fmt.Errorf("deriving address: %w", err)
	}

	// Parse JSON into a generic map so we can manipulate fields regardless of payload type.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parsing stdin JSON: %w", err)
	}

	// Track whether pubkey_b64 was present in the original payload.
	_, hasPubKey := m["pubkey_b64"]

	// Inject fresh nonce and timestamp — these must be in the map before MarshalForSigning
	// because timestamp IS included in the signing bytes (nonce is stripped).
	nonce := uuid.New().String()
	timestamp := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	m["nonce"], _ = json.Marshal(nonce)
	m["timestamp"], _ = json.Marshal(timestamp)

	// Compute signing bytes: MarshalForSigning strips signature, nonce, pubkey_b64,
	// then canonicalises the remaining fields. This mirrors the server's computation exactly.
	signingBytes, err := canonicaljson.MarshalForSigning(m)
	if err != nil {
		return fmt.Errorf("computing signing bytes: %w", err)
	}

	// Sign with ADR-036.
	sig := signADR036(priv, signerAddr, signingBytes)
	m["signature"], _ = json.Marshal(sig)

	// Fill pubkey_b64 only if the original payload had it (e.g. auth/join/readiness).
	// Proposal payloads (RaiseInput, SignInput) look up the pubkey from the committee server-side.
	if hasPubKey {
		m["pubkey_b64"], _ = json.Marshal(pubKeyB64(priv))
	}

	out, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshaling output: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

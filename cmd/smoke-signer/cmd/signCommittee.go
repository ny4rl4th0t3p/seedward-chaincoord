package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/pkg/canonicaljson"
)

func newSignCommitteeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sign-committee",
		Short: "Sign a CommitteeSignPayload JSON from stdin; outputs just the base64 signature",
		Long: `Reads a CommitteeSignPayload JSON from stdin, canonicalises it, wraps in
ADR-036 amino bytes, and signs. Outputs the base64 compact r‖s signature suitable
for the "creation_signature" field in POST /launch.

The payload must be:
  {"lead_address":"...","members":[{"address":"...","moniker":"...","pub_key_b64":"..."}],
   "threshold_m":1,"total_n":1}`,
		Example: `  echo '{"lead_address":"cosmos1...","members":[...],"threshold_m":1,"total_n":1}' \
    | smoke-signer sign-committee --key-index 0`,
		RunE: runSignCommittee,
	}
	cmd.Flags().Int("key-index", 0, "key index (0 = coordinator, 1-4 = validators)")
	_ = cmd.MarkFlagRequired("key-index")
	return cmd
}

func runSignCommittee(cmd *cobra.Command, _ []string) error {
	index, _ := cmd.Flags().GetInt("key-index")

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	signerAddr, err := deriveAddress(index, "cosmos")
	if err != nil {
		return fmt.Errorf("deriving address: %w", err)
	}

	// Canonicalise the payload. CommitteeSignPayload has no signing-stripped fields,
	// so Marshal and MarshalForSigning are equivalent here.
	canonical, err := canonicaljson.Marshal(raw)
	if err != nil {
		return fmt.Errorf("canonicalising committee payload: %w", err)
	}

	sig := signADR036(index, signerAddr, canonical)
	fmt.Println(sig)
	return nil
}

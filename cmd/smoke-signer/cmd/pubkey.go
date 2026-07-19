package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newPubkeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pubkey",
		Short: "Print the base64-encoded compressed public key",
		Example: `  smoke-signer pubkey --key-index 0        # coordinator pubkey for committee creation
  smoke-signer pubkey --privkey-hex <hex>`,
		RunE: runPubkey,
	}
	addKeySelectionFlags(cmd)
	return cmd
}

func runPubkey(cmd *cobra.Command, _ []string) error {
	priv, err := resolvePrivKey(cmd)
	if err != nil {
		return err
	}
	fmt.Println(pubKeyB64(priv))
	return nil
}

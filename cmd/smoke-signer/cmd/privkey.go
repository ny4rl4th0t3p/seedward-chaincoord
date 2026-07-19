package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newPrivkeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "privkey",
		Short: "Print the raw private key as hex for a key index (for gaiad keys import-hex)",
		Example: `  smoke-signer privkey --key-index 1
  gaiad keys import-hex operator $(smoke-signer privkey --key-index 1) --keyring-backend test`,
		RunE: runPrivkey,
	}
	// Index-only: this exports a deterministic key. (--privkey-hex would be a no-op here — the
	// caller already has the hex.)
	cmd.Flags().Int("key-index", 0, "deterministic key index (0 = coordinator, 1-4 = validators)")
	_ = cmd.MarkFlagRequired("key-index")
	return cmd
}

func runPrivkey(cmd *cobra.Command, _ []string) error {
	index, _ := cmd.Flags().GetInt("key-index")
	fmt.Println(privKeyHex(privKeyFromIndex(index)))
	return nil
}

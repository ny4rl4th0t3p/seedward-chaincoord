package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "smoke-signer",
		Short: "Deterministic secp256k1 signer for chaincoord smoke tests",
		Long: `smoke-signer generates deterministic key pairs and produces signed payloads
for the chaincoord Docker smoke test. Keys are derived from a fixed seed so the
same index always yields the same address and private key across runs.

Key index 0 is the coordinator; indices 1-4 are validators.`,
	}
	root.AddCommand(
		newAddressCmd(),
		newPrivkeyCmd(),
		newPubkeyCmd(),
		newSignCmd(),
	)
	return root
}

func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

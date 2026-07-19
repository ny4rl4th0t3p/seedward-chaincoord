package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "smoke-signer",
		Short: "Deterministic secp256k1 signer for chaincoord smoke tests",
		Long: `smoke-signer produces the ADR-036 signed payloads coordd's auth expects, for the
chaincoord Docker smoke test and the demo seeder.

By default keys are derived from a fixed seed so the same --key-index always yields the same
address and private key across runs (index 0 = coordinator, indices 1-4 = validators).
Alternatively, supply an externally-derived key with --privkey-hex (e.g. a gaiad HD key exported
with 'keys export --unarmored-hex') so the signer can act as any wallet-importable account while
reusing the exact same ADR-036 signing and address derivation.`,
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

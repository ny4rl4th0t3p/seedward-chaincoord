package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAddressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "address",
		Short: "Print the bech32 operator address for a key",
		Example: `  smoke-signer address --key-index 0
  smoke-signer address --key-index 1 --hrp cosmos
  smoke-signer address --privkey-hex <hex> --hrp cosmos`,
		RunE: runAddress,
	}
	addKeySelectionFlags(cmd)
	cmd.Flags().String("hrp", "cosmos", "bech32 human-readable part")
	return cmd
}

func runAddress(cmd *cobra.Command, _ []string) error {
	priv, err := resolvePrivKey(cmd)
	if err != nil {
		return err
	}
	hrp, _ := cmd.Flags().GetString("hrp")
	addr, err := deriveAddress(priv, hrp)
	if err != nil {
		return err
	}
	fmt.Println(addr)
	return nil
}

// Package gentxvalidation adapts the shared seedward-libs/gentxvalidate library
// to the ports.GentxValidator interface, keeping the SDK/validation weight out of
// the domain and application layers (DEC-16).
package gentxvalidation

import (
	"encoding/base64"

	"github.com/ny4rl4th0t3p/seedward-libs/gentxvalidate"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// Validator runs the server invariant set (RunAll) and, on a fully-passing
// result, extracts the consensus pubkey for the caller.
type Validator struct{}

// New returns a Validator.
func New() *Validator { return &Validator{} }

// Validate runs every server invariant over gentxJSON. On success it also returns
// the base64 consensus pubkey in the exact format coordd persists, so the
// consensus-pubkey unique index keeps matching across old and new rows.
func (*Validator) Validate(gentxJSON []byte, p gentxvalidate.Params) ports.GentxValidationOutcome {
	results := gentxvalidate.RunAll(gentxJSON, p)
	out := ports.GentxValidationOutcome{Results: results}
	if gentxvalidate.AllOK(results) {
		// RunAll already passed well_formed, so Decode cannot fail here.
		if g, err := gentxvalidate.Decode(gentxJSON); err == nil {
			out.ConsensusPubKeyB64 = base64.StdEncoding.EncodeToString(g.Msg.ConsensusPubKey)
		}
	}
	return out
}

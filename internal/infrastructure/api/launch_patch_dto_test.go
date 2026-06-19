package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ptrTo returns a pointer to v — used to populate the all-optional patch DTO.
func ptrTo[T any](v T) *T { return &v }

// validOperatorAddr is a valid bech32 operator address for exercising the
// allowlist field (which is validated by parseOperatorAddresses).
const validOperatorAddr = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"

// TestPatchLaunchRequestCoversCommand guards against silent drift between the
// wire DTO (patchLaunchRequest, the documented PATCH /launch/{id} body) and the
// domain command it maps to (services.PatchLaunchInput). The two intentionally
// differ in field *types* (wire strings vs domain value types), but they must
// expose the same logical *fields*: every wire field must reach the command, and
// every command field must be reachable from the wire.
//
// The guard sets every field on the wire DTO, runs the real decode path
// (parsePatchInput), then asserts every field of the resulting command is
// populated. Add a field to either side without the other and this fails —
// pointing you at exactly what to reconcile.
func TestPatchLaunchRequestCoversCommand(t *testing.T) {
	req := patchLaunchRequest{
		ChainName:         ptrTo("newchain"),
		BinaryVersion:     ptrTo("v2.0.0"),
		BinarySHA256:      ptrTo("abc123"),
		RepoURL:           ptrTo("https://example.com/repo"),
		RepoCommit:        ptrTo("deadbeef"),
		MonitorRPCURL:     ptrTo("https://rpc.example.com"),
		GenesisTime:       ptrTo(time.Now().UTC()),
		MinValidatorCount: ptrTo(4),
		Visibility:        ptrTo("public"),
		Allowlist:         []string{validOperatorAddr},
	}

	body, err := json.Marshal(req)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))

	input, err := parsePatchInput(raw)
	require.NoError(t, err)

	// Same field count in both directions: catches a phantom wire field the
	// handler ignores (the population check below catches the reverse).
	wireFields := reflect.TypeOf(patchLaunchRequest{}).NumField()
	cmdFields := reflect.TypeOf(input).NumField()
	require.Equalf(t, cmdFields, wireFields,
		"patchLaunchRequest has %d fields but PatchLaunchInput has %d — they have drifted",
		wireFields, cmdFields)

	v := reflect.ValueOf(input)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		assert.NotNilf(t, v.Field(i).Interface(),
			"PatchLaunchInput.%s was not populated — patchLaunchRequest and parsePatchInput have "+
				"drifted; update the wire DTO and/or parsePatchInput so both cover this field", name)
	}
}

package cmd

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/auditlog"
)

func newAuditCmd() *cobra.Command {
	audit := &cobra.Command{
		Use:   "audit",
		Short: "Audit log utilities",
	}
	audit.AddCommand(newAuditVerifyCmd())
	return audit
}

func newAuditVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify structural integrity and Ed25519 signatures of a local JSONL audit log",
		Long: `Reads a JSONL audit log file produced by coordd and checks:
  - Every line is valid JSON with required fields (launch_id, event_name, occurred_at, payload)
  - Timestamps are monotonically non-decreasing
  - Ed25519 signatures are valid (if a public key is available)

The audit public key can be supplied via --pubkey or fetched automatically from
a running server with --server-url (uses GET /api/v1/audit/pubkey).`,
		Example: `  # Verify offline with an explicit pubkey
  coordd audit verify --file audit.jsonl --pubkey <base64-ed25519-pubkey>

  # Fetch pubkey from a live server
  coordd audit verify --file audit.jsonl --server-url http://coordd:8080`,
		RunE: runAuditVerify,
	}
	cmd.Flags().String("file", "", "path to local JSONL audit log file (required)")
	cmd.Flags().String("pubkey", "", "base64 Ed25519 public key for signature verification")
	cmd.Flags().String("server-url", "", "coordd base URL — fetches audit pubkey via GET /api/v1/audit/pubkey if --pubkey is omitted")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func runAuditVerify(cmd *cobra.Command, _ []string) error {
	filePath, _ := cmd.Flags().GetString("file")
	pubKeyB64, _ := cmd.Flags().GetString("pubkey")
	serverURL, _ := cmd.Flags().GetString("server-url")

	pubKey, err := resolveAuditPubKey(pubKeyB64, serverURL)
	if err != nil {
		return err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	res, err := auditlog.Scan(f, pubKey)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	fmt.Printf("entries:    %d\n", res.Count)
	if res.Count > 0 {
		fmt.Printf("time range: %s → %s\n",
			res.FirstTime.Format(time.RFC3339),
			res.LastTime.Format(time.RFC3339),
		)
	}
	if pubKey != nil {
		fmt.Println("signatures: verified (where present)")
	} else {
		fmt.Println("signatures: not checked (no pubkey provided or fetched)")
	}
	if res.ChainChecked {
		fmt.Println("chain:      verified (where present)")
	} else {
		fmt.Println("chain:      not checked (no prev_hash fields in log)")
	}

	if len(res.Anomalies) == 0 {
		fmt.Println("result:     OK — no anomalies found")
		return nil
	}
	fmt.Printf("result:     %d anomaly(ies) found\n", len(res.Anomalies))
	for _, a := range res.Anomalies {
		fmt.Printf("  - %s\n", a)
	}
	return fmt.Errorf("audit log has %d anomaly(ies)", len(res.Anomalies))
}

// resolveAuditPubKey returns the Ed25519 public key to use for verification.
// Priority: explicit --pubkey flag > fetch from server > nil (skip sig checks).
func resolveAuditPubKey(pubKeyB64, serverURL string) (ed25519.PublicKey, error) {
	if pubKeyB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(pubKeyB64)
		if err != nil {
			return nil, fmt.Errorf("decoding --pubkey: %w", err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("--pubkey must be a 32-byte Ed25519 public key (%d bytes given)", len(raw))
		}
		return ed25519.PublicKey(raw), nil
	}

	if serverURL != "" {
		pub, err := fetchAuditPubKey(serverURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not fetch audit pubkey from server (%v) — signatures will not be verified\n", err)
			return nil, nil
		}
		fmt.Fprintf(os.Stderr, "info: using audit pubkey fetched from %s\n", serverURL)
		return pub, nil
	}

	return nil, nil
}

// fetchAuditPubKey calls GET /api/v1/audit/pubkey on the given server and returns the key.
func fetchAuditPubKey(serverURL string) (ed25519.PublicKey, error) {
	resp, err := http.Get(serverURL + "/api/v1/audit/pubkey") //nolint:noctx // simple CLI fetch, no context needed
	if err != nil {
		return nil, fmt.Errorf("GET /api/v1/audit/pubkey: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/v1/audit/pubkey: server returned %d", resp.StatusCode)
	}
	var body struct {
		PubKeyB64 string `json:"pub_key_b64"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding /api/v1/audit/pubkey response: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(body.PubKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decoding pubkey from server: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("server returned a key of unexpected size %d", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

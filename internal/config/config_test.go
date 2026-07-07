package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/config"
)

func newViper() *viper.Viper { return viper.New() }

func TestLoad_RehearsalLeaseTTL(t *testing.T) {
	v := newViper()
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("audit_private_key", testAuditKey)
	v.Set("jwt_private_key", testJWTKey)
	v.Set("files_path", "/tmp/genesis")
	v.Set("rehearsal_lease_ttl", "30m")

	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RehearsalLeaseTTL != 30*time.Minute {
		t.Errorf("RehearsalLeaseTTL: got %v, want 30m", cfg.RehearsalLeaseTTL)
	}
}

// testAuditKey is a valid base64-encoded 32-byte Ed25519 seed for use in tests.
const testAuditKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// testJWTKey is a valid base64-encoded 32-byte Ed25519 seed for use in tests.
// Must differ from testAuditKey to catch accidental key reuse.
const testJWTKey = "AQIDAQIDAQIDAQIDAQIDAQIDAQIDAQIDAQIDAQIDAQI="

// allRequired sets all required fields on v so tests can focus on one thing at a time.
func allRequired(v *viper.Viper) {
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("audit_private_key", testAuditKey)
	v.Set("files_path", "/tmp/genesis")
	v.Set("jwt_private_key", testJWTKey)
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("COORD_ADMIN_ADDRESSES", "")
	v := newViper()
	allRequired(v)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr default: got %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel default: got %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.LaunchPolicy != config.LaunchPolicyRestricted {
		t.Errorf("LaunchPolicy default: got %q, want %q", cfg.LaunchPolicy, config.LaunchPolicyRestricted)
	}
	if len(cfg.AdminAddresses) != 0 {
		t.Errorf("AdminAddresses default: got %v, want empty", cfg.AdminAddresses)
	}
}

func TestLoad_LaunchPolicyOpen(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("launch_policy", config.LaunchPolicyOpen)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LaunchPolicy != config.LaunchPolicyOpen {
		t.Errorf("LaunchPolicy: got %q, want %q", cfg.LaunchPolicy, config.LaunchPolicyOpen)
	}
}

func TestLoad_LaunchPolicyInvalid(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("launch_policy", "permissive")
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error for invalid launch_policy")
	}
}

func TestLoad_RehearsalGateDefault(t *testing.T) {
	v := newViper()
	allRequired(v)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RehearsalGate != "off" {
		t.Errorf("RehearsalGate default: got %q, want off", cfg.RehearsalGate)
	}
}

func TestLoad_RehearsalGateInvalid(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("rehearsal_gate", "sometimes")
	if _, err := config.Load(v, ""); err == nil {
		t.Fatal("expected validation error for invalid rehearsal_gate")
	}
}

func TestLoad_RehearsalGateRequiredWithoutBridge(t *testing.T) {
	// required is meaningless without the bridge that produces result facts → fail fast at startup.
	v := newViper()
	allRequired(v)
	v.Set("rehearsal_gate", "required")
	if _, err := config.Load(v, ""); err == nil {
		t.Fatal("expected fail-fast: rehearsal_gate=required without a rehearsal ops token")
	}
}

func TestLoad_RehearsalGateRequiredWithBridge(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("rehearsal_gate", "required")
	v.Set("rehearsal_ops_token", "secret")
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("required + ops token should be valid: %v", err)
	}
	if cfg.RehearsalGate != "required" {
		t.Errorf("RehearsalGate: got %q, want required", cfg.RehearsalGate)
	}
}

func TestLoad_AdminAddressesFromEnv(t *testing.T) {
	t.Setenv("COORD_ADMIN_ADDRESSES", "cosmos1aaa,cosmos1bbb,cosmos1ccc")

	v := newViper()
	allRequired(v)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"cosmos1aaa", "cosmos1bbb", "cosmos1ccc"}
	if len(cfg.AdminAddresses) != len(want) {
		t.Fatalf("AdminAddresses: got %v, want %v", cfg.AdminAddresses, want)
	}
	for i, w := range want {
		if cfg.AdminAddresses[i] != w {
			t.Errorf("AdminAddresses[%d]: got %q, want %q", i, cfg.AdminAddresses[i], w)
		}
	}
}

func TestLoad_AdminAddressesBlankEnv(t *testing.T) {
	t.Setenv("COORD_ADMIN_ADDRESSES", "")

	v := newViper()
	allRequired(v)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AdminAddresses) != 0 {
		t.Errorf("AdminAddresses with blank env: got %v, want empty", cfg.AdminAddresses)
	}
}

func TestLoad_FromConfigFile(t *testing.T) {
	yaml := `
listen_addr: ":9090"
db_path: "/data/coord.db"
audit_log_path: "/data/audit.jsonl"
audit_private_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
jwt_private_key: "AQIDAQIDAQIDAQIDAQIDAQIDAQIDAQIDAQIDAQIDAQI="
files_path: "/data/genesis"
log_level: "debug"
`
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	v := newViper()
	cfg, err := config.Load(v, cfgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr: got %q, want %q", cfg.ListenAddr, ":9090")
	}
	if cfg.DBPath != "/data/coord.db" {
		t.Errorf("DBPath: got %q", cfg.DBPath)
	}
	if cfg.AuditLogPath != "/data/audit.jsonl" {
		t.Errorf("AuditLogPath: got %q", cfg.AuditLogPath)
	}
	if cfg.FilesPath != "/data/genesis" {
		t.Errorf("FilesPath: got %q", cfg.FilesPath)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel: got %q", cfg.LogLevel)
	}
}

func TestLoad_FromEnvVars(t *testing.T) {
	t.Setenv("COORD_DB_PATH", "/env/coord.db")
	t.Setenv("COORD_AUDIT_LOG_PATH", "/env/audit.jsonl")
	t.Setenv("COORD_AUDIT_PRIVATE_KEY", testAuditKey)
	t.Setenv("COORD_FILES_PATH", "/env/genesis")
	t.Setenv("COORD_JWT_PRIVATE_KEY", testJWTKey)

	v := newViper()
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DBPath != "/env/coord.db" {
		t.Errorf("DBPath from env: got %q", cfg.DBPath)
	}
	if cfg.AuditLogPath != "/env/audit.jsonl" {
		t.Errorf("AuditLogPath from env: got %q", cfg.AuditLogPath)
	}
	if cfg.FilesPath != "/env/genesis" {
		t.Errorf("FilesPath from env: got %q", cfg.FilesPath)
	}
}

func TestLoad_MissingDBPath(t *testing.T) {
	v := newViper()
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("files_path", "/tmp/genesis")
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error for missing db_path")
	}
}

func TestLoad_MissingAuditLogPath(t *testing.T) {
	v := newViper()
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_private_key", testAuditKey)
	v.Set("files_path", "/tmp/genesis")
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error for missing audit_log_path")
	}
}

func TestLoad_MissingAuditPrivateKey(t *testing.T) {
	v := newViper()
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("files_path", "/tmp/genesis")
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error for missing audit_private_key")
	}
}

func TestLoad_InvalidAuditPrivateKey(t *testing.T) {
	v := newViper()
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("audit_private_key", "not-valid-base64!!!")
	v.Set("files_path", "/tmp/genesis")
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error for invalid audit_private_key base64")
	}
}

func TestLoad_MissingFilesPath(t *testing.T) {
	v := newViper()
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("audit_private_key", testAuditKey)
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error for missing files_path")
	}
}

func TestLoad_TLSBothSet(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("tls_cert", "/etc/coordd/cert.pem")
	v.Set("tls_key", "/etc/coordd/key.pem")
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TLSCert != "/etc/coordd/cert.pem" {
		t.Errorf("TLSCert: got %q", cfg.TLSCert)
	}
	if cfg.TLSKey != "/etc/coordd/key.pem" {
		t.Errorf("TLSKey: got %q", cfg.TLSKey)
	}
}

func TestLoad_TLSOnlyCert(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("tls_cert", "/etc/coordd/cert.pem")
	// tls_key intentionally absent
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error when only tls_cert is set")
	}
}

func TestLoad_TLSOnlyKey(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("tls_key", "/etc/coordd/key.pem")
	// tls_cert intentionally absent
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error when only tls_key is set")
	}
}

func TestLoad_InsecureNoTLS(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("insecure_no_tls", true)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.InsecureNoTLS {
		t.Error("InsecureNoTLS: expected true")
	}
}

func TestLoad_TLSDefaultsOff(t *testing.T) {
	v := newViper()
	allRequired(v)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TLSCert != "" {
		t.Errorf("TLSCert default: expected empty, got %q", cfg.TLSCert)
	}
	if cfg.TLSKey != "" {
		t.Errorf("TLSKey default: expected empty, got %q", cfg.TLSKey)
	}
	if cfg.InsecureNoTLS {
		t.Error("InsecureNoTLS default: expected false")
	}
}

func TestLoad_MissingJWTPrivateKey(t *testing.T) {
	v := newViper()
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("audit_private_key", testAuditKey)
	v.Set("files_path", "/tmp/genesis")
	// jwt_private_key intentionally absent
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error for missing jwt_private_key")
	}
}

func TestLoad_InvalidJWTPrivateKey(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("jwt_private_key", "not-valid-base64!!!")
	_, err := config.Load(v, "")
	if err == nil {
		t.Fatal("expected validation error for invalid jwt_private_key base64")
	}
}

func TestLoad_AuditPrivateKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "audit.key")
	if err := os.WriteFile(keyFile, []byte(testAuditKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v := newViper()
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("audit_private_key_file", keyFile)
	v.Set("files_path", "/tmp/genesis")
	v.Set("jwt_private_key", testJWTKey)
	// audit_private_key intentionally absent — should be loaded from file

	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuditPrivKeyB64 != testAuditKey {
		t.Errorf("AuditPrivKeyB64 from file: got %q, want %q", cfg.AuditPrivKeyB64, testAuditKey)
	}
}

func TestLoad_InsecureNoRateLimit(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("insecure_no_rate_limit", true)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.InsecureNoRateLimit {
		t.Error("InsecureNoRateLimit: expected true")
	}
}

func TestLoad_InsecureNoRateLimitDefault(t *testing.T) {
	v := newViper()
	allRequired(v)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InsecureNoRateLimit {
		t.Error("InsecureNoRateLimit default: expected false")
	}
}

func TestLoad_InsecureNoSSRFCheck(t *testing.T) {
	v := newViper()
	allRequired(v)
	v.Set("insecure_no_ssrf_check", true)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.InsecureNoSSRFCheck {
		t.Error("InsecureNoSSRFCheck: expected true")
	}
}

func TestLoad_InsecureNoSSRFCheckDefault(t *testing.T) {
	v := newViper()
	allRequired(v)
	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InsecureNoSSRFCheck {
		t.Error("InsecureNoSSRFCheck default: expected false")
	}
}

func TestLoad_JWTPrivateKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "jwt.key")
	if err := os.WriteFile(keyFile, []byte(testJWTKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v := newViper()
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("audit_private_key", testAuditKey)
	v.Set("files_path", "/tmp/genesis")
	v.Set("jwt_private_key_file", keyFile)
	// jwt_private_key intentionally absent — should be loaded from file

	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.JWTPrivKeyB64 != testJWTKey {
		t.Errorf("JWTPrivKeyB64 from file: got %q, want %q", cfg.JWTPrivKeyB64, testJWTKey)
	}
}

func TestLoad_RehearsalOpsTokenFromFile(t *testing.T) {
	const opsToken = "rehearsal-ops-token-abc123"
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "ops.token")
	if err := os.WriteFile(tokenFile, []byte(opsToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v := newViper()
	v.Set("db_path", "/tmp/coord.db")
	v.Set("audit_log_path", "/tmp/audit.jsonl")
	v.Set("audit_private_key", testAuditKey)
	v.Set("jwt_private_key", testJWTKey)
	v.Set("files_path", "/tmp/genesis")
	v.Set("rehearsal_ops_token_file", tokenFile)
	// rehearsal_ops_token intentionally absent — should be loaded from file (trimmed).

	cfg, err := config.Load(v, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RehearsalOpsToken != opsToken {
		t.Errorf("RehearsalOpsToken from file: got %q, want %q", cfg.RehearsalOpsToken, opsToken)
	}
}

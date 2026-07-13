package config

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// LaunchPolicy controls who may create new launches.
const (
	LaunchPolicyOpen       = "open"       // any authenticated address may create a launch
	LaunchPolicyRestricted = "restricted" // only addresses on the coordinator allowlist may create a launch

	genesisMaxMiB = 700 // default genesis file size limit in mebibytes
	mibShift      = 20  // bit-shift to convert mebibytes to bytes (1 MiB = 1 << 20)
)

// Config holds all coordd runtime configuration.
type Config struct {
	ListenAddr      string   `mapstructure:"listen_addr"`
	DBPath          string   `mapstructure:"db_path"`
	AuditLogPath    string   `mapstructure:"audit_log_path"`
	AuditPrivKeyB64 string   `mapstructure:"audit_private_key"`
	FilesPath       string   `mapstructure:"files_path"` // root dir for genesis + allocation file storage
	LogLevel        string   `mapstructure:"log_level"`
	CORSOrigins     string   `mapstructure:"cors_origins"`
	AdminAddresses  []string `mapstructure:"admin_addresses"`
	LaunchPolicy    string   `mapstructure:"launch_policy"`

	// GenesisHostMode enables Option C (host mode): raw genesis file uploads are
	// accepted and served directly from disk. When false (the default), only
	// Option A (attestor mode) is accepted — coordinators register an external
	// URL + SHA-256 hash and clients are redirected there.
	GenesisHostMode bool `mapstructure:"genesis_host_mode"`

	// GenesisMaxBytes is the maximum accepted raw genesis file size when host
	// mode is enabled (COORD_GENESIS_HOST_MODE=true). Defaults to 700 MiB.
	GenesisMaxBytes int64 `mapstructure:"genesis_max_bytes"`

	// AuditPrivKeyFile is an alternative to AuditPrivKeyB64: a path to a file
	// containing the base64-encoded Ed25519 seed. Intended for use with Docker
	// secrets, Kubernetes secrets, or similar secrets managers so the key is
	// never exposed as a plain environment variable. Generate with: coordd keygen
	AuditPrivKeyFile string `mapstructure:"audit_private_key_file"`

	// TLS configuration. TLSCert and TLSKey must be set together (or both empty).
	// When both are set, coordd terminates TLS itself.
	// When empty, coordd binds plain HTTP. Set InsecureNoTLS to suppress the
	// startup warning when TLS is handled by an upstream proxy (infra TLS mode).
	TLSCert       string `mapstructure:"tls_cert"`
	TLSKey        string `mapstructure:"tls_key"`
	InsecureNoTLS bool   `mapstructure:"insecure_no_tls"`

	// InsecureNoSSRFCheck disables DNS-resolution and private-IP checks on
	// user-supplied RPC URLs (monitor_rpc_url, genesis attestor URLs). Only
	// enable this in trusted environments such as smoke-test Docker networks
	// where RPC hosts are internal container names, not user-controlled input.
	InsecureNoSSRFCheck bool `mapstructure:"insecure_no_ssrf_check"`

	// InsecureNoRateLimit disables all rate limiters: the HTTP per-IP middleware on auth challenge
	// and validator write endpoints, and the storage-layer per-operator challenge rate limiter.
	// Only for use in automated test environments.
	InsecureNoRateLimit bool `mapstructure:"insecure_no_rate_limit"`

	// JWTPrivKeyB64 is a base64-encoded Ed25519 seed used to sign session JWTs.
	// Must be different from AuditPrivKeyB64. Generate with: coordd keygen
	// Can also be provided via a file path using jwt_private_key_file.
	JWTPrivKeyB64  string `mapstructure:"jwt_private_key"`
	JWTPrivKeyFile string `mapstructure:"jwt_private_key_file"`

	// RehearsalOpsToken is the shared bearer token authenticating the ops plane on the
	// rehearsal bridge endpoints (/bridge/*) — deployment-wide, not per-launch. Prefer
	// the _file variant so the secret is never a plain env var.
	// Empty disables the bridge (requireOps fails closed).
	RehearsalOpsToken     string `mapstructure:"rehearsal_ops_token"`
	RehearsalOpsTokenFile string `mapstructure:"rehearsal_ops_token_file"`

	// RehearsalLeaseTTL bounds how long a claimed rehearsal run holds its single-writer lease before
	// it is considered stale and re-claimable (a crashed runner self-heals). Accepts a Go duration
	// string, e.g. "45m", "1h". Empty/zero uses the built-in default (45m).
	RehearsalLeaseTTL time.Duration `mapstructure:"rehearsal_lease_ttl"`

	// RehearsalGate is the opt-in policy for requiring a passing rehearsal before a launch may
	// finalize genesis: off (default) | advisory | required. Default off keeps coordd standalone —
	// rehearsal is an optional bolt-on. required additionally needs the bridge enabled (ops token).
	RehearsalGate string `mapstructure:"rehearsal_gate"`
}

// Load reads configuration into a Config from the provided Viper instance.
// Precedence (highest to lowest): CLI flags (bound by caller) → COORD_ env vars → config file → defaults.
// cfgFile may be empty, in which case the standard search paths are used.
func Load(v *viper.Viper, cfgFile string) (*Config, error) {
	v.SetDefault("listen_addr", ":8080")
	v.SetDefault("log_level", "info")
	v.SetDefault("launch_policy", LaunchPolicyRestricted)
	v.SetDefault("genesis_max_bytes", int64(genesisMaxMiB<<mibShift))
	v.SetDefault("rehearsal_gate", "off")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.coordd")
		v.AddConfigPath("/etc/coordd")
	}

	v.SetEnvPrefix("COORD")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Explicit bindings are required, so Unmarshal picks up env vars for keys
	// that have no default value (AutomaticEnv alone does not register them in
	// AllKeys, which Unmarshal iterates).
	_ = v.BindEnv("db_path", "COORD_DB_PATH")
	_ = v.BindEnv("audit_log_path", "COORD_AUDIT_LOG_PATH")
	_ = v.BindEnv("audit_private_key", "COORD_AUDIT_PRIVATE_KEY")
	_ = v.BindEnv("audit_private_key_file", "COORD_AUDIT_PRIVATE_KEY_FILE")
	_ = v.BindEnv("files_path", "COORD_FILES_PATH")
	_ = v.BindEnv("listen_addr", "COORD_LISTEN_ADDR")
	_ = v.BindEnv("log_level", "COORD_LOG_LEVEL")
	_ = v.BindEnv("cors_origins", "COORD_CORS_ORIGINS")
	_ = v.BindEnv("admin_addresses", "COORD_ADMIN_ADDRESSES")
	_ = v.BindEnv("launch_policy", "COORD_LAUNCH_POLICY")
	_ = v.BindEnv("tls_cert", "COORD_TLS_CERT")
	_ = v.BindEnv("tls_key", "COORD_TLS_KEY")
	_ = v.BindEnv("insecure_no_tls", "COORD_INSECURE_NO_TLS")
	_ = v.BindEnv("insecure_no_ssrf_check", "COORD_INSECURE_NO_SSRF_CHECK")
	_ = v.BindEnv("insecure_no_rate_limit", "COORD_INSECURE_NO_RATE_LIMIT")
	_ = v.BindEnv("genesis_host_mode", "COORD_GENESIS_HOST_MODE")
	_ = v.BindEnv("genesis_max_bytes", "COORD_GENESIS_MAX_BYTES")
	_ = v.BindEnv("jwt_private_key", "COORD_JWT_PRIVATE_KEY")
	_ = v.BindEnv("jwt_private_key_file", "COORD_JWT_PRIVATE_KEY_FILE")
	_ = v.BindEnv("rehearsal_ops_token", "COORD_REHEARSAL_OPS_TOKEN")
	_ = v.BindEnv("rehearsal_ops_token_file", "COORD_REHEARSAL_OPS_TOKEN_FILE")
	_ = v.BindEnv("rehearsal_lease_ttl", "COORD_REHEARSAL_LEASE_TTL")
	_ = v.BindEnv("rehearsal_gate", "COORD_REHEARSAL_GATE")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	// Unmarshal won't split a comma-separated env var string into a []string slice,
	// so we do it explicitly here.
	raw := v.GetString("admin_addresses")
	cfg.AdminAddresses = nil
	for _, a := range strings.Split(raw, ",") {
		if a = strings.TrimSpace(a); a != "" {
			cfg.AdminAddresses = append(cfg.AdminAddresses, a)
		}
	}

	if err := cfg.loadKeyFiles(); err != nil {
		return nil, err
	}

	return &cfg, cfg.validate()
}

// loadKeyFiles resolves _FILE variants for private key fields, reading key
// material from the referenced path when the inline value is absent.
func (c *Config) loadKeyFiles() error {
	if c.AuditPrivKeyB64 == "" && c.AuditPrivKeyFile != "" {
		data, err := os.ReadFile(c.AuditPrivKeyFile)
		if err != nil {
			return fmt.Errorf("config: reading audit_private_key_file: %w", err)
		}
		c.AuditPrivKeyB64 = strings.TrimSpace(string(data))
	}
	if c.JWTPrivKeyB64 == "" && c.JWTPrivKeyFile != "" {
		data, err := os.ReadFile(c.JWTPrivKeyFile)
		if err != nil {
			return fmt.Errorf("config: reading jwt_private_key_file: %w", err)
		}
		c.JWTPrivKeyB64 = strings.TrimSpace(string(data))
	}
	if c.RehearsalOpsToken == "" && c.RehearsalOpsTokenFile != "" {
		data, err := os.ReadFile(c.RehearsalOpsTokenFile)
		if err != nil {
			return fmt.Errorf("config: reading rehearsal_ops_token_file: %w", err)
		}
		c.RehearsalOpsToken = strings.TrimSpace(string(data))
	}
	return nil
}

func (c *Config) validate() error {
	if c.DBPath == "" {
		return errors.New("config: db_path is required (flag --db-path or env COORD_DB_PATH)")
	}
	if c.AuditLogPath == "" {
		return errors.New("config: audit_log_path is required (flag --audit-log-path or env COORD_AUDIT_LOG_PATH)")
	}
	if c.AuditPrivKeyB64 == "" {
		return errors.New("config: audit_private_key is required" +
			" (env COORD_AUDIT_PRIVATE_KEY or COORD_AUDIT_PRIVATE_KEY_FILE)" +
			" — generate with: coordd keygen")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(c.AuditPrivKeyB64))
	if err != nil {
		return fmt.Errorf("config: audit_private_key is not valid base64: %w", err)
	}
	if len(keyBytes) != ed25519.SeedSize {
		return fmt.Errorf(
			"config: audit_private_key must be a %d-byte Ed25519 seed (got %d bytes after base64 decode)",
			ed25519.SeedSize, len(keyBytes),
		)
	}
	if c.FilesPath == "" {
		return errors.New("config: files_path is required (flag --files-path or env COORD_FILES_PATH)")
	}
	if c.LaunchPolicy != LaunchPolicyOpen && c.LaunchPolicy != LaunchPolicyRestricted {
		return fmt.Errorf("config: launch_policy must be %q or %q, got %q", LaunchPolicyOpen, LaunchPolicyRestricted, c.LaunchPolicy)
	}
	switch c.RehearsalGate {
	case "", "off", "advisory", "required":
	default:
		return fmt.Errorf("config: rehearsal_gate must be off|advisory|required, got %q", c.RehearsalGate)
	}
	// Fail fast: requiring rehearsal is meaningless without the bridge that produces result facts.
	if c.RehearsalGate == "required" && c.RehearsalOpsToken == "" && c.RehearsalOpsTokenFile == "" {
		return errors.New("config: rehearsal_gate=required but the rehearsal bridge is disabled" +
			" (set COORD_REHEARSAL_OPS_TOKEN or COORD_REHEARSAL_OPS_TOKEN_FILE)")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("config: tls_cert and tls_key must both be set or both be empty")
	}
	if c.JWTPrivKeyB64 == "" {
		return errors.New("config: jwt_private_key is required" +
			" (env COORD_JWT_PRIVATE_KEY or COORD_JWT_PRIVATE_KEY_FILE)" +
			" — generate with: coordd keygen")
	}
	jwtKeyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(c.JWTPrivKeyB64))
	if err != nil {
		return fmt.Errorf("config: jwt_private_key is not valid base64: %w", err)
	}
	if len(jwtKeyBytes) != ed25519.SeedSize {
		return fmt.Errorf(
			"config: jwt_private_key must be a %d-byte Ed25519 seed (got %d bytes after base64 decode)",
			ed25519.SeedSize, len(jwtKeyBytes),
		)
	}
	// Defense-in-depth: reusing one seed for both the audit-log signer and the JWT session signer
	// means a leak of one context's key can forge the other's artifacts. Require distinct keys.
	if bytes.Equal(keyBytes, jwtKeyBytes) {
		return errors.New("config: jwt_private_key must differ from audit_private_key" +
			" (using one key for both the audit-log and session signers is not allowed)")
	}
	return nil
}

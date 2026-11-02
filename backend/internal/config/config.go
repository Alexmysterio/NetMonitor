package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Env                   string
	HTTPAddr              string
	DatabaseURL           string
	RedisURL              string
	JWTSecret             string
	EncryptionKey         string
	AdminUsername         string
	AdminPassword         string
	SeedDemo              bool
	PublicURL             string
	PollerWorkers         int
	PollInterval          int
	EnableDemoPoll        bool
	EnableTraps           bool
	EnableSyslog          bool
	TrapAddr              string
	SyslogAddr            string
	AgentTLSEnabled       bool
	AgentTLSAddr          string
	AgentTLSCert          string
	AgentTLSKey           string
	AgentTLSCA            string
	AlertMaxPerMin        int
	EncryptionKeyFile     string
	VaultAddr             string
	VaultToken            string
	VaultPath             string
	EncryptionKeyCmd      string
	KeyRotationDays       int
	LeaderElection        bool
	ReportDir             string
	AllowInsecureDefaults bool
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envIntAllowZero(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func Load() Config {
	return Config{
		Env:                   env("APP_ENV", "development"),
		HTTPAddr:              env("HTTP_ADDR", ":8080"),
		DatabaseURL:           env("DATABASE_URL", "postgres://netmonitor:netmonitor_change_me@localhost:5432/netmonitor?sslmode=disable"),
		RedisURL:              env("REDIS_URL", "redis://localhost:6379/0"),
		JWTSecret:             env("JWT_SECRET", "development-only-secret-change-me"),
		EncryptionKey:         env("ENCRYPTION_KEY", ""),
		AdminUsername:         env("ADMIN_USERNAME", "admin"),
		AdminPassword:         env("ADMIN_PASSWORD", "admin"),
		SeedDemo:              envBool("SEED_DEMO", true),
		PublicURL:             env("PUBLIC_URL", "http://localhost:8080"),
		PollerWorkers:         envInt("POLLER_WORKERS", 64),
		PollInterval:          envInt("POLL_INTERVAL_SEC", 30),
		EnableTraps:           envBool("ENABLE_TRAP_RECEIVER", true),
		EnableSyslog:          envBool("ENABLE_SYSLOG_RECEIVER", true),
		TrapAddr:              env("TRAP_ADDR", "0.0.0.0:1620"),
		SyslogAddr:            env("SYSLOG_ADDR", "0.0.0.0:5514"),
		AgentTLSEnabled:       envBool("AGENT_TLS_ENABLED", false),
		AgentTLSAddr:          env("AGENT_TLS_ADDR", ":8443"),
		AgentTLSCert:          env("AGENT_TLS_CERT", "/app/certs/server.crt"),
		AgentTLSKey:           env("AGENT_TLS_KEY", "/app/certs/server.key"),
		AgentTLSCA:            env("AGENT_TLS_CA", "/app/certs/ca.crt"),
		AlertMaxPerMin:        envIntAllowZero("ALERT_MAX_PER_MINUTE", 120),
		EncryptionKeyFile:     env("ENCRYPTION_KEY_FILE", ""),
		VaultAddr:             env("VAULT_ADDR", ""),
		VaultToken:            env("VAULT_TOKEN", ""),
		VaultPath:             env("VAULT_SECRET_PATH", "secret/data/netmonitor"),
		EncryptionKeyCmd:      env("ENCRYPTION_KEY_CMD", ""),
		KeyRotationDays:       envIntAllowZero("ENCRYPTION_KEY_ROTATION_DAYS", 0),
		LeaderElection:        envBool("CORE_LEADER_ELECTION", false),
		ReportDir:             env("REPORT_DIR", "./reports"),
		AllowInsecureDefaults: envBool("ALLOW_INSECURE_DEFAULTS", false),
	}
}

// Validate rejects known development credentials in production. Local/dev
// stacks can opt out explicitly with ALLOW_INSECURE_DEFAULTS=true.
func (c Config) Validate() error {
	if c.Env != "production" || c.AllowInsecureDefaults {
		return nil
	}
	if len(c.JWTSecret) < 32 || strings.Contains(c.JWTSecret, "change-this") || strings.Contains(c.JWTSecret, "development-only") {
		return fmt.Errorf("JWT_SECRET must be a random value of at least 32 characters in production")
	}
	keyConfigured := c.EncryptionKey != "" || c.EncryptionKeyFile != "" || c.EncryptionKeyCmd != "" ||
		(c.VaultAddr != "" && c.VaultToken != "")
	if !keyConfigured {
		return fmt.Errorf("configure ENCRYPTION_KEY, ENCRYPTION_KEY_FILE, Vault or ENCRYPTION_KEY_CMD in production")
	}
	if c.EncryptionKey != "" && (len(c.EncryptionKey) < 32 ||
		strings.Contains(c.EncryptionKey, "change-this") || strings.Contains(c.EncryptionKey, "development-only")) {
		return fmt.Errorf("ENCRYPTION_KEY must be a random value of at least 32 characters in production")
	}
	if c.AdminPassword == "" || c.AdminPassword == "admin" || len(c.AdminPassword) < 12 {
		return fmt.Errorf("ADMIN_PASSWORD must not be the default and must have at least 12 characters")
	}
	if strings.Contains(c.DatabaseURL, "netmonitor_change_me") || strings.Contains(c.DatabaseURL, "password@") {
		return fmt.Errorf("DATABASE_URL still uses a placeholder password")
	}
	return nil
}

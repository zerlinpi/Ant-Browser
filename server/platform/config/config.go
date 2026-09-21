package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const developmentJWTSecret = "development-only-change-me-before-production"
const developmentSecretMasterKey = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="

// Config contains the control-plane settings shared by the HTTP API and worker.
// Secrets are loaded from the process environment and are never persisted here.
type Config struct {
	Environment           string
	HTTPAddress           string
	DatabaseURL           string
	RedisURL              string
	NATSURL               string
	ObjectStoreURL        string
	ObjectStoreBucket     string
	ObjectStoreAccessKey  string
	ObjectStoreSecretKey  string
	ObjectStoreRegion     string
	ObjectStoreAutoCreate bool
	EncryptionKeyRef      string
	SecretMasterKey       string
	SecretKeyVersion      string
	JWTIssuer             string
	JWTSecret             string
	AccessTokenTTL        time.Duration
	RefreshTokenTTL       time.Duration
	ShutdownTimeout       time.Duration
	AllowMemoryStore      bool
	TrustedProxyCIDRs     []string
}

type WorkerConfig struct {
	Environment     string
	DatabaseURL     string
	RedisURL        string
	NATSURL         string
	SMTPAddress     string
	SMTPUsername    string
	SMTPPassword    string
	SMTPFrom        string
	SMTPTLSMode     string
	WorkerID        string
	LeaseTTL        time.Duration
	PollInterval    time.Duration
	Parallelism     int
	ShutdownTimeout time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Environment:           env("ANT_ENV", "development"),
		HTTPAddress:           env("ANT_HTTP_ADDRESS", ":8080"),
		DatabaseURL:           strings.TrimSpace(os.Getenv("ANT_DATABASE_URL")),
		RedisURL:              strings.TrimSpace(os.Getenv("ANT_REDIS_URL")),
		NATSURL:               strings.TrimSpace(os.Getenv("ANT_NATS_URL")),
		ObjectStoreURL:        strings.TrimSpace(os.Getenv("ANT_OBJECT_STORE_URL")),
		ObjectStoreBucket:     strings.TrimSpace(os.Getenv("ANT_OBJECT_STORE_BUCKET")),
		ObjectStoreAccessKey:  strings.TrimSpace(os.Getenv("ANT_OBJECT_STORE_ACCESS_KEY")),
		ObjectStoreSecretKey:  strings.TrimSpace(os.Getenv("ANT_OBJECT_STORE_SECRET_KEY")),
		ObjectStoreRegion:     env("ANT_OBJECT_STORE_REGION", "us-east-1"),
		ObjectStoreAutoCreate: envBool("ANT_OBJECT_STORE_AUTO_CREATE_BUCKET", false),
		EncryptionKeyRef:      strings.TrimSpace(os.Getenv("ANT_ENCRYPTION_KEY_REF")),
		SecretMasterKey:       strings.TrimSpace(os.Getenv("ANT_SECRET_MASTER_KEY")),
		SecretKeyVersion:      env("ANT_SECRET_KEY_VERSION", "v1"),
		JWTIssuer:             env("ANT_JWT_ISSUER", "ant-browser-control-plane"),
		JWTSecret:             strings.TrimSpace(os.Getenv("ANT_JWT_SECRET")),
		AccessTokenTTL:        envDuration("ANT_ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:       envDuration("ANT_REFRESH_TOKEN_TTL", 30*24*time.Hour),
		ShutdownTimeout:       envDuration("ANT_SHUTDOWN_TIMEOUT", 15*time.Second),
		AllowMemoryStore:      envBool("ANT_ALLOW_MEMORY_STORE", true),
	}

	if raw := strings.TrimSpace(os.Getenv("ANT_TRUSTED_PROXY_CIDRS")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			if value := strings.TrimSpace(item); value != "" {
				cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, value)
			}
		}
	}

	if cfg.JWTSecret == "" && cfg.Environment == "development" {
		cfg.JWTSecret = developmentJWTSecret
	}
	if cfg.EncryptionKeyRef == "" && cfg.Environment == "development" {
		cfg.EncryptionKeyRef = "development-only-local-profile-key"
	}
	if cfg.SecretMasterKey == "" && cfg.Environment == "development" {
		cfg.SecretMasterKey = developmentSecretMasterKey
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadWorker() (WorkerConfig, error) {
	hostname, _ := os.Hostname()
	if strings.TrimSpace(hostname) == "" {
		hostname = "worker"
	}
	workerDatabaseURL := strings.TrimSpace(os.Getenv("ANT_WORKER_DATABASE_URL"))
	if workerDatabaseURL == "" && env("ANT_ENV", "development") == "development" {
		workerDatabaseURL = strings.TrimSpace(os.Getenv("ANT_DATABASE_URL"))
	}
	cfg := WorkerConfig{
		Environment:     env("ANT_ENV", "development"),
		DatabaseURL:     workerDatabaseURL,
		RedisURL:        strings.TrimSpace(os.Getenv("ANT_REDIS_URL")),
		NATSURL:         strings.TrimSpace(os.Getenv("ANT_NATS_URL")),
		SMTPAddress:     strings.TrimSpace(os.Getenv("ANT_SMTP_ADDRESS")),
		SMTPUsername:    strings.TrimSpace(os.Getenv("ANT_SMTP_USERNAME")),
		SMTPPassword:    os.Getenv("ANT_SMTP_PASSWORD"),
		SMTPFrom:        strings.TrimSpace(os.Getenv("ANT_SMTP_FROM")),
		SMTPTLSMode:     env("ANT_SMTP_TLS_MODE", "starttls"),
		WorkerID:        env("ANT_WORKER_ID", hostname),
		LeaseTTL:        envDuration("ANT_TASK_LEASE_TTL", 45*time.Second),
		PollInterval:    envDuration("ANT_TASK_POLL_INTERVAL", 2*time.Second),
		Parallelism:     envInt("ANT_TASK_PARALLELISM", 4),
		ShutdownTimeout: envDuration("ANT_SHUTDOWN_TIMEOUT", 15*time.Second),
	}
	var problems []string
	if cfg.DatabaseURL == "" {
		problems = append(problems, "ANT_WORKER_DATABASE_URL is required (use the ant_worker role)")
	}
	if cfg.Environment != "development" && cfg.RedisURL == "" {
		problems = append(problems, "ANT_REDIS_URL is required outside development")
	}
	if cfg.Environment != "development" && cfg.NATSURL == "" {
		problems = append(problems, "ANT_NATS_URL is required outside development")
	}
	if cfg.LeaseTTL < 10*time.Second || cfg.LeaseTTL > 30*time.Minute {
		problems = append(problems, "ANT_TASK_LEASE_TTL must be between 10s and 30m")
	}
	if cfg.PollInterval < 100*time.Millisecond || cfg.PollInterval > time.Minute {
		problems = append(problems, "ANT_TASK_POLL_INTERVAL must be between 100ms and 1m")
	}
	if cfg.Parallelism < 1 || cfg.Parallelism > 64 {
		problems = append(problems, "ANT_TASK_PARALLELISM must be between 1 and 64")
	}
	smtpConfigured := cfg.SMTPAddress != "" || cfg.SMTPUsername != "" || cfg.SMTPPassword != "" || cfg.SMTPFrom != ""
	if smtpConfigured && (cfg.SMTPAddress == "" || cfg.SMTPFrom == "") {
		problems = append(problems, "ANT_SMTP_ADDRESS and ANT_SMTP_FROM are required when SMTP is configured")
	}
	if cfg.SMTPPassword != "" && cfg.SMTPUsername == "" {
		problems = append(problems, "ANT_SMTP_USERNAME is required when ANT_SMTP_PASSWORD is configured")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SMTPTLSMode)) {
	case "starttls", "tls":
	case "plain":
		if cfg.Environment != "development" {
			problems = append(problems, "ANT_SMTP_TLS_MODE=plain is forbidden outside development")
		}
	default:
		problems = append(problems, "ANT_SMTP_TLS_MODE must be starttls, tls, or plain")
	}
	if len(problems) > 0 {
		return WorkerConfig{}, errors.New(strings.Join(problems, "; "))
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string
	if strings.TrimSpace(c.HTTPAddress) == "" {
		problems = append(problems, "ANT_HTTP_ADDRESS is required")
	}
	if len(c.JWTSecret) < 32 {
		problems = append(problems, "ANT_JWT_SECRET must contain at least 32 characters")
	}
	if c.AccessTokenTTL <= 0 || c.AccessTokenTTL > time.Hour {
		problems = append(problems, "ANT_ACCESS_TOKEN_TTL must be between 1ns and 1h")
	}
	if c.RefreshTokenTTL < time.Hour {
		problems = append(problems, "ANT_REFRESH_TOKEN_TTL must be at least 1h")
	}
	if c.Environment != "development" && c.DatabaseURL == "" {
		problems = append(problems, "ANT_DATABASE_URL is required outside development")
	}
	if c.Environment != "development" && c.RedisURL == "" {
		problems = append(problems, "ANT_REDIS_URL is required outside development")
	}
	if c.Environment != "development" && c.NATSURL == "" {
		problems = append(problems, "ANT_NATS_URL is required outside development")
	}
	if c.Environment != "development" {
		if c.ObjectStoreURL == "" || c.ObjectStoreBucket == "" || c.ObjectStoreAccessKey == "" || c.ObjectStoreSecretKey == "" {
			problems = append(problems, "ANT_OBJECT_STORE_URL, ANT_OBJECT_STORE_BUCKET, ANT_OBJECT_STORE_ACCESS_KEY, and ANT_OBJECT_STORE_SECRET_KEY are required outside development")
		}
		if c.EncryptionKeyRef == "" {
			problems = append(problems, "ANT_ENCRYPTION_KEY_REF is required outside development")
		}
		if c.SecretMasterKey == "" {
			problems = append(problems, "ANT_SECRET_MASTER_KEY is required outside development")
		}
	}
	if c.SecretMasterKey != "" {
		decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(c.SecretMasterKey, "="))
		if err != nil || len(decoded) != 32 {
			problems = append(problems, "ANT_SECRET_MASTER_KEY must be base64-encoded 32-byte key material")
		}
		developmentKey, _ := base64.StdEncoding.DecodeString(developmentSecretMasterKey)
		if c.Environment != "development" && string(decoded) == string(developmentKey) {
			problems = append(problems, "development secret master key is forbidden outside development")
		}
	}
	if strings.TrimSpace(c.SecretKeyVersion) == "" {
		problems = append(problems, "ANT_SECRET_KEY_VERSION is required")
	}
	if c.Environment != "development" && c.JWTSecret == developmentJWTSecret {
		problems = append(problems, "development JWT secret is forbidden outside development")
	}
	if c.DatabaseURL == "" && !c.AllowMemoryStore {
		problems = append(problems, "ANT_DATABASE_URL is required when memory store is disabled")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c Config) RedactedSummary() string {
	store := "postgres"
	if c.DatabaseURL == "" {
		store = "memory"
	}
	return fmt.Sprintf("environment=%s address=%s store=%s", c.Environment, c.HTTPAddress, store)
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

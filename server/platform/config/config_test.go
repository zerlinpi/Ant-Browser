package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestProductionConfigRequiresDurableDependenciesAndProfileEncryption(t *testing.T) {
	cfg := Config{
		Environment: "production", HTTPAddress: ":8080",
		DatabaseURL: "postgres://database", RedisURL: "redis://cache", NATSURL: "nats://queue",
		JWTSecret: strings.Repeat("s", 32), AccessTokenTTL: 15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour, AllowMemoryStore: false, AllowedOrigins: []string{"https://app.example.test", "http://wails.localhost"},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "ANT_OBJECT_STORE_URL") || !strings.Contains(err.Error(), "ANT_ENCRYPTION_KEY_REF") || !strings.Contains(err.Error(), "ANT_SECRET_MASTER_KEY") {
		t.Fatalf("production config accepted missing profile durability controls: %v", err)
	}

	cfg.ObjectStoreURL = "https://objects.example.test"
	cfg.ObjectStoreBucket = "profiles"
	cfg.ObjectStoreAccessKey = "access-key"
	cfg.ObjectStoreSecretKey = "secret-key"
	cfg.ObjectStoreRegion = "us-east-1"
	cfg.EncryptionKeyRef = "kms://profiles/key"
	cfg.SecretMasterKey = "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXowMTIzNDU="
	cfg.SecretKeyVersion = "v1"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
	cfg.SecretMasterKey = strings.TrimRight(developmentSecretMasterKey, "=")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "development secret master key") {
		t.Fatalf("production accepted development master key: %v", err)
	}
}

func TestDevelopmentLoadUsesExplicitNonProductionDefaults(t *testing.T) {
	for _, name := range []string{
		"ANT_ENV", "ANT_HTTP_ADDRESS", "ANT_DATABASE_URL", "ANT_REDIS_URL", "ANT_NATS_URL",
		"ANT_OBJECT_STORE_URL", "ANT_OBJECT_STORE_BUCKET", "ANT_OBJECT_STORE_ACCESS_KEY",
		"ANT_OBJECT_STORE_SECRET_KEY", "ANT_ENCRYPTION_KEY_REF", "ANT_SECRET_MASTER_KEY",
		"ANT_SECRET_KEY_VERSION", "ANT_JWT_SECRET", "ANT_ALLOWED_ORIGINS",
	} {
		t.Setenv(name, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != "development" || cfg.JWTSecret != developmentJWTSecret || cfg.EncryptionKeyRef != "development-only-local-profile-key" || cfg.SecretMasterKey != developmentSecretMasterKey {
		t.Fatalf("unexpected development defaults: %+v", cfg)
	}
	if len(cfg.AllowedOrigins) != 3 {
		t.Fatalf("unexpected development allowed origins: %v", cfg.AllowedOrigins)
	}
}

func TestAllowedOriginsRejectWildcardAndPaths(t *testing.T) {
	base := Config{
		Environment: "development", HTTPAddress: ":8080", JWTSecret: strings.Repeat("s", 32),
		AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 24 * time.Hour, AllowMemoryStore: true,
		SecretMasterKey: developmentSecretMasterKey, SecretKeyVersion: "v1",
	}
	for _, origin := range []string{"*", "https://app.example.test/path", "javascript:alert(1)", "https://user@app.example.test"} {
		cfg := base
		cfg.AllowedOrigins = []string{origin}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "ANT_ALLOWED_ORIGINS") {
			t.Fatalf("unsafe origin %q accepted: %v", origin, err)
		}
	}
}

// The worker validates its proxy-probe master key with ValidateSecretMasterKey,
// so it must reject exactly what Config.Validate rejects for the control plane.
func TestValidateSecretMasterKeyMatchesControlPlanePolicy(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))
	for _, tc := range []struct {
		name, environment, key, wantErr string
	}{
		{name: "valid key in production", environment: "production", key: valid},
		{name: "unpadded valid key", environment: "production", key: strings.TrimRight(valid, "=")},
		{name: "development key in development", environment: "development", key: developmentSecretMasterKey},
		{name: "missing key", environment: "production", wantErr: "ANT_SECRET_MASTER_KEY is required"},
		{name: "missing key has no development default", environment: "development", wantErr: "ANT_SECRET_MASTER_KEY is required"},
		{name: "placeholder is not base64", environment: "production", key: "replace-with-base64-encoded-32-byte-key", wantErr: "base64-encoded 32-byte"},
		{name: "31-byte key", environment: "production", key: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31)), wantErr: "base64-encoded 32-byte"},
		{name: "33-byte key", environment: "development", key: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 33)), wantErr: "base64-encoded 32-byte"},
		{name: "development key in production", environment: "production", key: developmentSecretMasterKey, wantErr: "development secret master key is forbidden"},
		{name: "unpadded development key in staging", environment: "staging", key: strings.TrimRight(developmentSecretMasterKey, "="), wantErr: "development secret master key is forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSecretMasterKey(tc.environment, tc.key)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateSecretMasterKey(%q) rejected a valid key: %v", tc.environment, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateSecretMasterKey(%q) error = %v, want %q", tc.environment, err, tc.wantErr)
			}
			if tc.key == "" {
				return
			}
			// Parity with the control plane for every configured key.
			cfg := Config{
				Environment: tc.environment, HTTPAddress: ":8080",
				DatabaseURL: "postgres://database", RedisURL: "redis://cache", NATSURL: "nats://queue",
				ObjectStoreURL: "https://objects.example.test", ObjectStoreBucket: "profiles",
				ObjectStoreAccessKey: "access-key", ObjectStoreSecretKey: "secret-key",
				EncryptionKeyRef: "kms://profiles/key", SecretMasterKey: tc.key, SecretKeyVersion: "v1",
				JWTSecret: strings.Repeat("s", 32), AccessTokenTTL: 15 * time.Minute,
				RefreshTokenTTL: 24 * time.Hour, AllowedOrigins: []string{"https://app.example.test"},
			}
			validateErr := cfg.Validate()
			controlPlaneRejects := validateErr != nil && (strings.Contains(validateErr.Error(), "ANT_SECRET_MASTER_KEY") || strings.Contains(validateErr.Error(), "development secret master key"))
			if controlPlaneRejects != (err != nil) {
				t.Fatalf("worker and control plane disagree: worker=%v control plane=%v", err, validateErr)
			}
		})
	}
}

func TestTrustedProxyCIDRsMustBeRangesOrAddresses(t *testing.T) {
	base := Config{
		Environment: "development", HTTPAddress: ":8080", JWTSecret: strings.Repeat("s", 32),
		AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 24 * time.Hour, AllowMemoryStore: true,
		SecretMasterKey: developmentSecretMasterKey, SecretKeyVersion: "v1",
	}
	valid := base
	valid.TrustedProxyCIDRs = []string{"10.0.0.0/8", "192.0.2.10", "2001:db8::/32", "::1", "::ffff:172.16.0.0/108"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid trusted proxies rejected: %v", err)
	}
	for _, entry := range []string{"10.0.0.0/33", "proxy.internal", "10.0.0.1:80", "fe80::1%eth0", "*"} {
		cfg := base
		cfg.TrustedProxyCIDRs = []string{"10.0.0.0/8", entry}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "ANT_TRUSTED_PROXY_CIDRS") || !strings.Contains(err.Error(), entry) {
			t.Fatalf("invalid trusted proxy %q accepted or not reported: %v", entry, err)
		}
	}
	for _, entry := range []string{"0.0.0.0/0", "::/0", "::ffff:0.0.0.0/96"} {
		cfg := base
		cfg.TrustedProxyCIDRs = []string{entry}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must not trust every address") {
			t.Fatalf("trust-everything range %q accepted: %v", entry, err)
		}
	}
}

func TestLoadParsesTrustedProxyCIDRs(t *testing.T) {
	for _, name := range []string{
		"ANT_ENV", "ANT_HTTP_ADDRESS", "ANT_DATABASE_URL", "ANT_REDIS_URL", "ANT_NATS_URL",
		"ANT_OBJECT_STORE_URL", "ANT_OBJECT_STORE_BUCKET", "ANT_OBJECT_STORE_ACCESS_KEY",
		"ANT_OBJECT_STORE_SECRET_KEY", "ANT_ENCRYPTION_KEY_REF", "ANT_SECRET_MASTER_KEY",
		"ANT_SECRET_KEY_VERSION", "ANT_JWT_SECRET", "ANT_ALLOWED_ORIGINS",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("ANT_TRUSTED_PROXY_CIDRS", " 10.0.0.0/8, ,192.0.2.10 ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 || cfg.TrustedProxyCIDRs[0] != "10.0.0.0/8" || cfg.TrustedProxyCIDRs[1] != "192.0.2.10" {
		t.Fatalf("trusted proxies = %v", cfg.TrustedProxyCIDRs)
	}
	t.Setenv("ANT_TRUSTED_PROXY_CIDRS", "10.0.0.0/8,not-a-range")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ANT_TRUSTED_PROXY_CIDRS") {
		t.Fatalf("invalid trusted proxy configuration loaded: %v", err)
	}
}

package config

import (
	"strings"
	"testing"
	"time"
)

func TestProductionConfigRequiresDurableDependenciesAndProfileEncryption(t *testing.T) {
	cfg := Config{
		Environment: "production", HTTPAddress: ":8080",
		DatabaseURL: "postgres://database", RedisURL: "redis://cache", NATSURL: "nats://queue",
		JWTSecret: strings.Repeat("s", 32), AccessTokenTTL: 15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour, AllowMemoryStore: false,
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
		"ANT_SECRET_KEY_VERSION", "ANT_JWT_SECRET",
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
}

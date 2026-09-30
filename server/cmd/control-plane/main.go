package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/config"
	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	"github.com/zerlinpi/Ant-Browser/server/platform/natstask"
	"github.com/zerlinpi/Ant-Browser/server/platform/objectstore"
	"github.com/zerlinpi/Ant-Browser/server/platform/observability"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
	"github.com/zerlinpi/Ant-Browser/server/platform/redisrt"
	"github.com/zerlinpi/Ant-Browser/server/platform/secureenvelope"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	"github.com/zerlinpi/Ant-Browser/server/platform/taskwake"
	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	adminservice "github.com/zerlinpi/Ant-Browser/server/services/admin-service"
	analyticsservice "github.com/zerlinpi/Ant-Browser/server/services/analytics-service"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	batchservice "github.com/zerlinpi/Ant-Browser/server/services/batch-service"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	fingerprintservice "github.com/zerlinpi/Ant-Browser/server/services/fingerprint-service"
	gatewayservice "github.com/zerlinpi/Ant-Browser/server/services/gateway-service"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

type repositoryStore interface {
	authservice.Repository
	// Listed so both stores must keep implementing second factors; the auth
	// service detects the capability on the repository it is given.
	authservice.MFARepository
	workspaceservice.Repository
	deviceservice.Repository
	browserinstanceservice.Repository
	fingerprintservice.Repository
	profilesyncservice.Repository
	automationservice.Repository
	accountservice.Repository
	adminservice.Repository
	analyticsservice.Repository
	proxyservice.Repository
	notificationservice.Repository
	billingservice.Repository
	secureenvelope.ProxyCredentialRepository
	taskservice.Repository
	scheduleservice.Repository
	scheduleservice.WorkflowLookup
	Ping(context.Context) error
	Close() error
}

type dependencyGroup []interface {
	Ping(context.Context) error
}

type profileObjectStore interface {
	profilesyncservice.ObjectStorage
	Ping(context.Context) error
	Close() error
}

func (group dependencyGroup) Ping(ctx context.Context) error {
	for _, item := range group {
		if err := item.Ping(ctx); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	logger := observability.NewLogger(os.Getenv("ANT_ENV"), os.Stdout)
	if err := run(logger); err != nil {
		logger.Error("control_plane_stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger.Info("control_plane_starting", "config", cfg.RedactedSummary())

	rootContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := openStore(rootContext, cfg, logger)
	if err != nil {
		return err
	}
	defer store.Close()
	realtimeBus, err := openRealtime(rootContext, cfg, logger)
	if err != nil {
		return err
	}
	defer realtimeBus.Close()
	taskWake, err := openTaskWake(cfg, logger)
	if err != nil {
		return err
	}
	defer taskWake.Close()
	profileObjects, storageBackend, err := openProfileObjectStore(rootContext, cfg, logger)
	if err != nil {
		return err
	}
	defer profileObjects.Close()

	passwords := security.NewPasswords()
	tokens := security.NewTokens(cfg.JWTIssuer, cfg.JWTSecret, cfg.AccessTokenTTL)
	auth := authservice.New(store, passwords, tokens, security.NewOpaqueToken, cfg.RefreshTokenTTL)
	workspaces := workspaceservice.New(store)
	devices := deviceservice.New(store, security.NewOpaqueToken, workspaces)
	instances := browserinstanceservice.New(store, workspaces)
	tasks := taskservice.New(store, workspaces)
	fingerprints := fingerprintservice.New(store, workspaces)
	profiles := profilesyncservice.New(
		store, workspaces, security.NewOpaqueToken, profileObjects,
		cfg.EncryptionKeyRef, storageBackend,
	)
	workflows := automationservice.New(store, workspaces)
	envelopeCrypto, err := secureenvelope.New(cfg.SecretMasterKey, cfg.EncryptionKeyRef, cfg.SecretKeyVersion)
	if err != nil {
		return err
	}
	mfaSealer, err := secureenvelope.NewTextSealer(envelopeCrypto)
	if err != nil {
		return err
	}
	auth.ConfigureMFA(mfaSealer, "Ant Browser")
	proxySecrets, err := secureenvelope.NewProxyProvider(store, envelopeCrypto)
	if err != nil {
		return err
	}
	accounts := accountservice.New(store, workspaces, envelopeCrypto)
	proxies := proxyservice.New(store, workspaces, proxySecrets)
	batches := batchservice.New(instances, accounts, proxies, tasks, workspaces)
	notifications := notificationservice.New(store, workspaces)
	schedules := scheduleservice.New(store, store, store, workspaces)
	analytics := analyticsservice.New(store, workspaces)
	admin := adminservice.New(store)
	billing := billingservice.NewAuthorized(store, workspaces)
	dependencies := dependencyGroup{store, realtimeBus, taskWake, profileObjects}
	handler := gatewayservice.NewWithInfrastructure(
		rootContext, auth, workspaces, devices, instances, tokens,
		dependencies, realtimeBus, tasks, taskWake, fingerprints, profiles, workflows, accounts, proxies, logger, notifications, schedules, analytics, admin,
		batches, billing, gatewayservice.AllowedOrigins(cfg.AllowedOrigins),
		gatewayservice.TrustedProxies(cfg.TrustedProxyCIDRs),
	)

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("control_plane_listening", "address", cfg.HTTPAddress)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-rootContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		logger.Info("control_plane_shutdown_complete")
		return nil
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func openTaskWake(cfg config.Config, logger *slog.Logger) (taskwake.Bus, error) {
	if cfg.NATSURL == "" {
		logger.Warn("memory_task_wake_enabled", "warning", "task wakeups are process-local; PostgreSQL polling remains authoritative")
		return taskwake.NewMemory(), nil
	}
	return natstask.Open(cfg.NATSURL, "ant-browser-control-plane")
}

func openRealtime(ctx context.Context, cfg config.Config, logger *slog.Logger) (realtime.Bus, error) {
	if cfg.RedisURL == "" {
		logger.Warn("memory_realtime_enabled", "warning", "presence and command fan-out are process-local")
		return realtime.NewMemory(), nil
	}
	return redisrt.Open(ctx, cfg.RedisURL)
}

func openProfileObjectStore(ctx context.Context, cfg config.Config, logger *slog.Logger) (profileObjectStore, string, error) {
	if cfg.ObjectStoreURL == "" {
		logger.Warn("metadata_profile_store_enabled", "warning", "profile object bytes are not persisted; this mode is forbidden in production")
		return objectstore.MetadataStore{}, "metadata", nil
	}
	store, err := objectstore.Open(ctx, objectstore.Config{
		Endpoint: cfg.ObjectStoreURL, Bucket: cfg.ObjectStoreBucket,
		AccessKey: cfg.ObjectStoreAccessKey, SecretKey: cfg.ObjectStoreSecretKey,
		Region: cfg.ObjectStoreRegion, AutoCreate: cfg.ObjectStoreAutoCreate,
	})
	if err != nil {
		return nil, "", err
	}
	return store, "s3", nil
}

func openStore(ctx context.Context, cfg config.Config, logger *slog.Logger) (repositoryStore, error) {
	if cfg.DatabaseURL == "" {
		logger.Warn("memory_store_enabled", "warning", "data is ephemeral and this mode is forbidden in production")
		return memory.New(), nil
	}
	return postgres.Open(ctx, cfg.DatabaseURL)
}

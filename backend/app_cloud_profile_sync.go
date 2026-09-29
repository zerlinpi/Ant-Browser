package backend

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ant-chrome/backend/internal/cloudagent"
	cloudprofile "ant-chrome/desktop/profile-sync"
)

const (
	// A CDP close returns once the debugging port closes, while Chromium's
	// child processes can still be flushing cookies and databases. Uploading
	// waits this long for them to exit.
	cloudProfileReleaseTimeout      = 15 * time.Second
	cloudProfileReleasePollInterval = 500 * time.Millisecond
)

type cloudProfileSyncBridge struct {
	app              *App
	config           cloudagent.Config
	deviceCredential string
	encryptionKey    [32]byte
	encryptionKeyRef string
	releaseTimeout   time.Duration
}

// cloudProfileSyncTarget is a bound local profile resolved under the browser
// manager lock.
type cloudProfileSyncTarget struct {
	cloudProfileID string
	directory      string
	// running is the manager's recorded state; live additionally confirms
	// the browser process still exists.
	running bool
	live    bool
}

func (a *App) newCloudProfileSyncBridge(config cloudagent.Config) (*cloudProfileSyncBridge, error) {
	if len(config.CloudProfiles) == 0 {
		return nil, errors.New("cloud profile bindings are not configured")
	}
	credential := strings.TrimSpace(os.Getenv("ANT_CLOUD_DEVICE_CREDENTIAL"))
	if credential == "" || len(credential) > 8192 || strings.ContainsAny(credential, "\x00\r\n") {
		return nil, errors.New("cloud device credential is unavailable")
	}
	encodedKey := strings.TrimSpace(os.Getenv("ANT_CLOUD_PROFILE_ENCRYPTION_KEY"))
	decodedKey, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(decodedKey) != 32 {
		return nil, errors.New("ANT_CLOUD_PROFILE_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	keyRef := strings.TrimSpace(os.Getenv("ANT_CLOUD_PROFILE_ENCRYPTION_KEY_REF"))
	if keyRef == "" || len(keyRef) > 512 || strings.ContainsAny(keyRef, "\x00\r\n") {
		return nil, errors.New("ANT_CLOUD_PROFILE_ENCRYPTION_KEY_REF is invalid")
	}
	bridge := &cloudProfileSyncBridge{
		app: a, config: config, deviceCredential: credential, encryptionKeyRef: keyRef,
		releaseTimeout: cloudProfileReleaseTimeout,
	}
	copy(bridge.encryptionKey[:], decodedKey)
	for index := range decodedKey {
		decodedKey[index] = 0
	}
	return bridge, nil
}

// Synchronizes reports whether instanceID has a cloud profile on this device.
// Instances bound only to a local profile run without synchronization.
func (b *cloudProfileSyncBridge) Synchronizes(instanceID string) bool {
	if b == nil {
		return false
	}
	_, ok := b.config.CloudProfiles[instanceID]
	return ok
}

// Push uploads the stopped local profile. A device whose last synchronized
// revision is no longer current opens a conflict instead of overwriting the
// cloud; the returned error names the conflict to resolve.
func (b *cloudProfileSyncBridge) Push(ctx context.Context, instanceID, localProfileID string) error {
	target, err := b.resolveTarget(instanceID, localProfileID)
	if err != nil {
		return err
	}
	if target.running {
		return errors.New("profile synchronization requires the local browser to be stopped")
	}
	if err := waitForBrowserProfileRelease(ctx, target.directory, b.releaseTimeout); err != nil {
		return err
	}
	client, err := b.syncClient(target.cloudProfileID)
	if err != nil {
		return err
	}
	_, err = client.Push(ctx, target.directory, "")
	return err
}

// PullLatest restores the cloud's current revision before a start. A browser
// that is already running owns its directory, so the start reuses it instead
// of swapping state underneath a live process.
func (b *cloudProfileSyncBridge) PullLatest(ctx context.Context, instanceID, localProfileID string) error {
	target, err := b.resolveTarget(instanceID, localProfileID)
	if err != nil {
		return err
	}
	if target.live {
		return nil
	}
	client, err := b.syncClient(target.cloudProfileID)
	if err != nil {
		return err
	}
	_, err = client.PullCurrent(ctx, target.directory)
	if errors.Is(err, cloudprofile.ErrNoCloudRevision) {
		return nil
	}
	return err
}

func (b *cloudProfileSyncBridge) resolveTarget(instanceID, localProfileID string) (cloudProfileSyncTarget, error) {
	cloudProfileID, ok := b.config.CloudProfiles[instanceID]
	if !ok || b.config.Bindings[instanceID] != localProfileID {
		return cloudProfileSyncTarget{}, errors.New("cloud profile is not authorized for this local binding")
	}
	if b.app == nil || b.app.browserMgr == nil {
		return cloudProfileSyncTarget{}, errors.New("local browser manager is unavailable")
	}
	b.app.browserMgr.Mutex.Lock()
	defer b.app.browserMgr.Mutex.Unlock()
	localProfile := b.app.browserMgr.Profiles[localProfileID]
	if localProfile == nil || localProfile.DeletedAt != "" {
		return cloudProfileSyncTarget{}, errors.New("bound local profile is unavailable")
	}
	target := cloudProfileSyncTarget{
		cloudProfileID: cloudProfileID,
		directory:      b.app.browserMgr.ResolveUserDataDir(localProfile),
		running:        localProfile.Running,
	}
	if target.running {
		target.live = isBrowserProfileLive(localProfile, b.app.browserMgr.BrowserProcesses[localProfileID])
	}
	if strings.TrimSpace(target.directory) == "" {
		return cloudProfileSyncTarget{}, errors.New("bound local profile directory is unavailable")
	}
	return target, nil
}

func (b *cloudProfileSyncBridge) syncClient(cloudProfileID string) (*cloudprofile.SyncClient, error) {
	client, err := cloudprofile.NewSyncClient(cloudprofile.SyncConfig{
		BaseURL: b.config.BaseURL, DeviceCredential: b.deviceCredential,
		EncryptionKey: b.encryptionKey[:], EncryptionKeyRef: b.encryptionKeyRef,
		WorkspaceID: b.config.WorkspaceID, ProfileID: cloudProfileID, DeviceID: b.config.DeviceID,
	})
	if err != nil {
		return nil, fmt.Errorf("configure cloud profile synchronization: %w", err)
	}
	return client, nil
}

// waitForBrowserProfileRelease waits until no browser process uses directory.
// Process discovery is best effort (it is unavailable on some platforms);
// archiving still rejects files that change while they are read.
func waitForBrowserProfileRelease(ctx context.Context, directory string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		processes, err := findBrowserUserDataProcesses(directory)
		if err != nil || len(processes) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%d browser process(es) still use the profile directory; retry after they exit", len(processes))
		}
		if !sleepWithContext(ctx, cloudProfileReleasePollInterval) {
			return ctx.Err()
		}
	}
}

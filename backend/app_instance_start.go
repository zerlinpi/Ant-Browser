package backend

import (
	"context"
	"fmt"

	"ant-chrome/backend/internal/cloudagent"
)

func (a *App) BrowserInstanceStart(profileId string) (*BrowserProfile, error) {
	return a.browserInstanceStartInternal(profileId, nil, nil, false, false, false, "", "")
}

func shouldPreferVisibleWindowForStartWithParams(startURLs []string) bool {
	return len(normalizeNonEmptyStrings(startURLs)) > 0
}

// BrowserInstanceStartDirect 仅本次启动走直连，不落库修改实例代理配置。
func (a *App) BrowserInstanceStartDirect(profileId string) (*BrowserProfile, error) {
	return a.browserInstanceStartInternal(profileId, nil, nil, false, false, true, "", "")
}

// BrowserInstanceStartWithParams 通过额外参数启动实例（仅本次启动生效，不落库）
func (a *App) BrowserInstanceStartWithParams(profileId string, extraLaunchArgs []string, startURLs []string, skipDefaultStartURLs bool) (*BrowserProfile, error) {
	preferVisibleWindow := shouldPreferVisibleWindowForStartWithParams(startURLs)
	return a.browserInstanceStartInternal(profileId, extraLaunchArgs, startURLs, skipDefaultStartURLs, preferVisibleWindow, false, "", "")
}

func (a *App) browserInstanceStartInternal(profileId string, extraLaunchArgs []string, startURLs []string, skipDefaultStartURLs bool, preferVisibleWindow bool, forceDirectProxy bool, proxyId string, proxyConfig string) (*BrowserProfile, error) {
	return a.browserInstanceStartInternalContext(context.Background(), profileId, extraLaunchArgs, startURLs, skipDefaultStartURLs, preferVisibleWindow, forceDirectProxy, proxyId, proxyConfig)
}

func (a *App) browserInstanceStartInternalContext(ctx context.Context, profileId string, extraLaunchArgs []string, startURLs []string, skipDefaultStartURLs bool, preferVisibleWindow bool, forceDirectProxy bool, proxyId string, proxyConfig string) (*BrowserProfile, error) {
	input := newBrowserStartInput(profileId, extraLaunchArgs, startURLs, skipDefaultStartURLs, preferVisibleWindow, forceDirectProxy, proxyId, proxyConfig)
	return a.browserInstanceStartWithInputContext(ctx, input)
}

// browserInstanceStartWithCloudRuntimeContext starts the local profile bound
// to a cloud instance with that instance's runtime configuration. The config
// must have been resolved for exactly instanceID; the local profile ID comes
// from the device binding and is not required to equal the cloud UUID.
func (a *App) browserInstanceStartWithCloudRuntimeContext(ctx context.Context, instanceID, profileID string, runtimeConfig cloudagent.InstanceRuntimeConfig) (*BrowserProfile, error) {
	if instanceID == "" || runtimeConfig.InstanceID != instanceID {
		return nil, fmt.Errorf("cloud runtime configuration for %q does not match instance %q", runtimeConfig.InstanceID, instanceID)
	}
	if profileID == "" {
		return nil, fmt.Errorf("cloud instance %q has no local profile binding", instanceID)
	}
	input := newBrowserStartInput(profileID, nil, nil, false, false, false, "", "")
	input.CloudRuntimeConfig = &runtimeConfig
	input.CloudInstanceID = instanceID
	return a.browserInstanceStartWithInputContext(ctx, input)
}

func (a *App) browserInstanceStartWithInputContext(ctx context.Context, input browserStartInput) (*BrowserProfile, error) {
	a.browserMgr.Mutex.Lock()
	defer a.browserMgr.Mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	profile, handled, err := a.resolveBrowserStartProfile(input)
	if err != nil || handled {
		return profile, err
	}

	plan, err := a.prepareBrowserStartPlan(input, profile)
	if err == errBrowserStartHandledByRecoveredRuntime {
		a.emitBrowserInstanceStarted(profile, true)
		return profile, nil
	}
	if err != nil {
		return profile, err
	}
	defer plan.releaseBridgeIfNeeded(a)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return a.startBrowserProfileWithPlan(input, plan)
}

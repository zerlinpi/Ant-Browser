package profilesync

import (
	"path"
	"strings"
)

// Chromium keeps process locks, crash reports, metrics and caches inside the
// user data directory. They describe one machine or one browser process,
// regenerate on demand and can still be held open by an exiting browser, so
// profile synchronization never archives, restores or diffs them.
//
// Names are matched exactly and only where Chromium creates them (the user
// data root or a profile directory such as "Default" or "Profile 1"), so an
// extension that ships a directory called "Cache" is still synchronized.
var transientProfileDirectories = map[string]struct{}{
	"Cache":                {},
	"Code Cache":           {},
	"GPUCache":             {},
	"ShaderCache":          {},
	"GrShaderCache":        {},
	"GraphiteDawnCache":    {},
	"Crashpad":             {},
	"BrowserMetrics":       {},
	"component_crx_cache":  {},
	"extensions_crx_cache": {},
}

// transientProfileRootFiles describe a running browser process rather than
// profile state. Singleton* are symbolic links on Linux and macOS; skipping
// them by name keeps the general refusal of symbolic links intact.
// DevToolsActivePort must never travel: another device would probe the
// recorded port and could adopt an unrelated process listening on it.
var transientProfileRootFiles = map[string]struct{}{
	"lockfile":           {},
	"SingletonLock":      {},
	"SingletonCookie":    {},
	"SingletonSocket":    {},
	"DevToolsActivePort": {},
}

// isTransientProfileEntry classifies one slash-separated path relative to
// the profile root. Callers walking a directory skip a transient directory
// together with everything below it.
func isTransientProfileEntry(relative string, isDir bool) bool {
	depth := strings.Count(relative, "/")
	name := path.Base(relative)
	if isDir {
		if depth > 1 {
			return false
		}
		if _, ok := transientProfileDirectories[name]; ok {
			return true
		}
		// DawnCache, DawnGraphiteCache, DawnWebGPUCache and future variants.
		return strings.HasPrefix(name, "Dawn") && strings.HasSuffix(name, "Cache")
	}
	if depth == 0 {
		if _, ok := transientProfileRootFiles[name]; ok {
			return true
		}
		// BrowserMetrics-spare.pma and per-session BrowserMetrics-*.pma.
		if strings.HasPrefix(name, "BrowserMetrics") {
			return true
		}
	}
	// Leftovers of interrupted atomic writes (for example Preferences or
	// Local State replacements) near the profile root.
	return depth <= 1 && strings.HasSuffix(strings.ToLower(name), ".tmp")
}

// isTransientProfilePath reports whether an archive entry is transient or
// lies below a transient directory. Archives written by older clients may
// still carry such entries; restoring them would reintroduce the problems
// the filter exists to avoid.
func isTransientProfilePath(name string) bool {
	parts := strings.Split(name, "/")
	for index := 0; index < len(parts)-1 && index <= 1; index++ {
		if isTransientProfileEntry(strings.Join(parts[:index+1], "/"), true) {
			return true
		}
	}
	return isTransientProfileEntry(name, false)
}

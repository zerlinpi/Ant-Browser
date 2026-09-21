package automation

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// installPuppeteerRuntime installs puppeteer-core and its transitive runtime
// dependencies without downloading a second Chromium build. Ant-Browser always
// attaches to the managed browser instance over its authenticated CDP endpoint.
func (m *Manager) installPuppeteerRuntime(ctx context.Context, stagingDir, nodePath, version string) error {
	version = strings.TrimSpace(version)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(version) {
		return fmt.Errorf("puppeteer-core requires an exact release version")
	}
	playwrightVersion := readPackageVersion(filepath.Join(stagingDir, "node_modules", "playwright-core", "package.json"))
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(playwrightVersion) {
		return fmt.Errorf("staged playwright-core version is invalid")
	}
	npmExecutable, prefixArgs, err := resolveNPMInvocation(nodePath)
	if err != nil {
		return err
	}

	m.emitProgress("installing", 87, "正在安装 puppeteer-core", "puppeteer")
	args := append(prefixArgs,
		"install",
		"--ignore-scripts",
		"--omit=optional",
		"--no-audit",
		"--no-fund",
		"--package-lock=false",
		"--save=false",
		"--registry", strings.TrimRight(m.options.NPMRegistryBaseURL, "/"),
		"--prefix", stagingDir,
		"puppeteer-core@"+version,
		// npm prunes extraneous packages: explicitly retain the staged engine.
		"playwright-core@"+playwrightVersion,
	)
	cmd := exec.CommandContext(ctx, npmExecutable, args...)
	cmd.Dir = stagingDir
	cmd.Env = append(os.Environ(),
		"PUPPETEER_SKIP_DOWNLOAD=true",
		"PUPPETEER_SKIP_CHROME_DOWNLOAD=true",
		"npm_config_ignore_scripts=true",
	)
	hideWindow(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("安装 puppeteer-core 失败: %s", message)
	}
	if readPackageVersion(filepath.Join(stagingDir, "node_modules", "puppeteer-core", "package.json")) != version ||
		readPackageVersion(filepath.Join(stagingDir, "node_modules", "playwright-core", "package.json")) != playwrightVersion {
		return fmt.Errorf("安装 puppeteer-core 失败: package.json is missing")
	}
	return nil
}

func resolveNPMInvocation(nodePath string) (string, []string, error) {
	nodePath = strings.TrimSpace(nodePath)
	if nodePath == "" {
		return "", nil, fmt.Errorf("node path is empty while resolving npm")
	}
	nodeDir := filepath.Dir(nodePath)
	// Invoke npm's JS entry point with the selected Node binary. Executing
	// npm.cmd directly is not supported by Windows CreateProcess, and a shell
	// wrapper could select a different Node version via PATH.
	for _, cliPath := range []string{
		filepath.Join(nodeDir, "node_modules", "npm", "bin", "npm-cli.js"),
		filepath.Join(nodeDir, "..", "lib", "node_modules", "npm", "bin", "npm-cli.js"),
	} {
		if fileExists(cliPath) {
			return nodePath, []string{filepath.Clean(cliPath)}, nil
		}
	}
	return "", nil, fmt.Errorf("npm is unavailable for Node runtime %s", nodePath)
}

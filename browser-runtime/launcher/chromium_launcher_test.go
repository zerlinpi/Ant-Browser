package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildArgsConfinementAndControlledArguments(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "chromium")
	if err := os.WriteFile(executable, []byte("placeholder"), 0o700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(dir, "profiles", "one")
	args, err := BuildArgs(Config{Executable: executable, ProfilePath: profile, Direct: true, RemoteDebugPort: 9222, Args: []string{"--headless=new"}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--user-data-dir="+profile) || !strings.Contains(joined, "--remote-debugging-address=127.0.0.1") || !strings.Contains(joined, "--no-proxy-server") {
		t.Fatalf("unexpected args: %#v", args)
	}
	if _, err := BuildArgs(Config{Executable: executable, ProfilePath: profile, RemoteDebugPort: 9222, Args: []string{"--remote-debugging-port=1"}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("controlled argument accepted: %v", err)
	}
	if _, err := BuildArgs(Config{Executable: executable, ProfilePath: profile, Proxy: "http://user:secret@127.0.0.1:8080", RemoteDebugPort: 9222}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("credentialed proxy accepted: %v", err)
	}
	if _, err := BuildArgs(Config{Executable: executable, ProfilePath: profile, RemoteDebugPort: 9222, Args: []string{"--remote-debugging-pipe"}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("alternate CDP transport accepted: %v", err)
	}
}

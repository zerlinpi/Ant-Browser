package fingerprintloader

import (
	"errors"
	"testing"
)

func TestPrepareRejectsUnsafeTemplateAndSortsArgs(t *testing.T) {
	l := NewLoader()
	runtime, err := l.Prepare(Template{ID: "fp-1", Config: map[string]interface{}{
		"chromium.zeta": "z", "chromium.alpha": "a", "notes": "kept",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.Args) != 2 || runtime.Args[0] != "--alpha=a" || runtime.Args[1] != "--zeta=z" {
		t.Fatalf("args are not deterministic: %#v", runtime.Args)
	}
	if _, err := l.Prepare(Template{ID: "../escape", Config: map[string]interface{}{"x": true}}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("unsafe ID accepted: %v", err)
	}
	if _, err := l.Prepare(Template{ID: "ok", Config: map[string]interface{}{"chromium.bad": "a\nb"}}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("unsafe argument accepted: %v", err)
	}
}

func TestLoadStoresIndependentRuntimeCopy(t *testing.T) {
	l := NewLoader()
	config := map[string]interface{}{"nested": map[string]interface{}{"value": "one"}}
	if err := l.Load(Template{ID: "copy", Config: config}); err != nil {
		t.Fatal(err)
	}
	config["nested"].(map[string]interface{})["value"] = "changed"
	runtime, ok := l.Runtime("copy")
	if !ok || runtime.Config["nested"].(map[string]interface{})["value"] != "one" {
		t.Fatalf("runtime was not isolated: %#v", runtime)
	}
}

func TestPrepareRejectsUnsafeAndLauncherOwnedChromiumArgs(t *testing.T) {
	l := NewLoader()
	for _, config := range []map[string]interface{}{
		{"chromium.remote-debugging-port": "9222"},
		{"chromium.user data dir": "profile"},
		{"chromium.proxy-server": "http://127.0.0.1:8080"},
		{"chromium.Foo": "one", "chromium.foo": "two"},
		{"chromium.headless": "yes\tno"},
	} {
		if _, err := l.Prepare(Template{ID: "strict", Config: config}); !errors.Is(err, ErrInvalidTemplate) {
			t.Fatalf("unsafe Chromium config accepted (%#v): %v", config, err)
		}
	}
}

func TestPrepareRejectsDeepNestedConfig(t *testing.T) {
	value := interface{}("leaf")
	for i := 0; i < 10; i++ {
		value = map[string]interface{}{"nested": value}
	}
	if _, err := NewLoader().Prepare(Template{ID: "deep", Config: map[string]interface{}{"root": value}}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("deep config accepted: %v", err)
	}
}

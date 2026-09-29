package fingerprintruntime

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeScriptOnlyOverridesExistingSurfaces(t *testing.T) {
	script, err := buildRuntimeScript(validExtensionTemplate())
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, forbidden := range []string{
		// getBattery must never be installed where Chrome hides it.
		"defineProperty(navigatorPrototype, 'getBattery'",
		"new EventTarget()",
		// A missing descriptor must not fall back to defining a new property.
		"descriptor ? descriptor.enumerable : true",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("runtime script still contains %q", forbidden)
		}
	}
	for _, required := range []string{
		"typeof descriptor.get !== 'function'",
		"BatteryManager.prototype",
		"const value = Reflect.apply(original, this, arguments);",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("runtime script does not contain %q", required)
		}
	}
}

// runtimeScriptHarness evaluates the generated script in fresh node realms
// that mimic Chrome's WebIDL shapes for a secure context (with and without
// WEBGL_debug_renderer_info) and an insecure context, then reports what a
// page would observe.
const runtimeScriptHarness = `'use strict';
const fs = require('fs');
const vm = require('vm');

if (!vm.constants || vm.constants.DONT_CONTEXTIFY === undefined) {
  process.stdout.write(JSON.stringify({ unsupported: true }));
  process.exit(0);
}
const runtime = fs.readFileSync(process.argv[2], 'utf8');

const setup = String.raw` + "`" + `
(() => {
  const flags = globalThis.__flags;
  delete globalThis.__flags;
  const brand = (receiver, Type) => {
    if (!(receiver instanceof Type)) throw new TypeError('Illegal invocation');
  };
  const attribute = (target, name, get, set) => Object.defineProperty(target, name, { get, set, enumerable: true, configurable: true });
  const operation = (target, name, value) => Object.defineProperty(target, name, { value, writable: true, enumerable: true, configurable: true });
  const illegal = function () { throw new TypeError('Illegal constructor'); };

  const Navigator = function Navigator() { illegal(); };
  attribute(Navigator.prototype, 'maxTouchPoints', function () { brand(this, Navigator); return 0; });
  attribute(Navigator.prototype, 'doNotTrack', function () { brand(this, Navigator); return null; });
  if (flags.secure) {
    attribute(Navigator.prototype, 'deviceMemory', function () { brand(this, Navigator); return 2; });
    const BatteryManager = function BatteryManager() { illegal(); };
    attribute(BatteryManager.prototype, 'charging', function () { brand(this, BatteryManager); return false; });
    attribute(BatteryManager.prototype, 'level', function () { brand(this, BatteryManager); return 0.1; });
    attribute(BatteryManager.prototype, 'chargingTime', function () { brand(this, BatteryManager); return 1; });
    attribute(BatteryManager.prototype, 'dischargingTime', function () { brand(this, BatteryManager); return 2; });
    operation(Navigator.prototype, 'getBattery', function getBattery() {
      brand(this, Navigator);
      return Promise.resolve(Object.create(BatteryManager.prototype));
    });
    globalThis.BatteryManager = BatteryManager;
  }
  globalThis.Navigator = Navigator;
  globalThis.navigator = Object.create(Navigator.prototype);

  const Screen = function Screen() { illegal(); };
  for (const name of ['width', 'height', 'availWidth', 'availHeight', 'colorDepth', 'pixelDepth']) {
    attribute(Screen.prototype, name, function () { brand(this, Screen); return 100; });
  }
  globalThis.Screen = Screen;
  globalThis.screen = Object.create(Screen.prototype);

  attribute(globalThis, 'devicePixelRatio', function () { return 1; }, function (value) {
    Object.defineProperty(globalThis, 'devicePixelRatio', { value, writable: true, enumerable: true, configurable: true });
  });

  const WebGLRenderingContext = function WebGLRenderingContext() { illegal(); };
  operation(WebGLRenderingContext.prototype, 'getExtension', function getExtension(name) {
    brand(this, WebGLRenderingContext);
    if (name !== 'WEBGL_debug_renderer_info' || !flags.debugInfo) return null;
    this.debugInfoEnabled = true;
    return { UNMASKED_VENDOR_WEBGL: 37445, UNMASKED_RENDERER_WEBGL: 37446 };
  });
  operation(WebGLRenderingContext.prototype, 'getError', function getError() {
    brand(this, WebGLRenderingContext);
    const error = this.lastError || 0;
    this.lastError = 0;
    return error;
  });
  operation(WebGLRenderingContext.prototype, 'getParameter', function getParameter(pname) {
    brand(this, WebGLRenderingContext);
    if (arguments.length < 1) throw new TypeError('1 argument required');
    const name = Number(pname) >>> 0;
    if (name === 37445 || name === 37446) {
      if (!this.debugInfoEnabled) { this.lastError = 0x0500; return null; }
      return name === 37445 ? 'Host Vendor' : 'Host Renderer';
    }
    return name === 7937 ? 'WebKit WebGL' : 0;
  });
  globalThis.WebGLRenderingContext = WebGLRenderingContext;
  globalThis.createContext = () => Object.create(WebGLRenderingContext.prototype);
})();
` + "`" + `;

const snapshotSources = String.raw` + "`" + `
(() => {
  const describe = (target, name) => Object.getOwnPropertyDescriptor(target, name);
  const source = (fn) => Function.prototype.toString.call(fn);
  return {
    getParameter: source(WebGLRenderingContext.prototype.getParameter),
    maxTouchPoints: source(describe(Navigator.prototype, 'maxTouchPoints').get),
    deviceMemory: describe(Navigator.prototype, 'deviceMemory') ? source(describe(Navigator.prototype, 'deviceMemory').get) : '',
    getBattery: typeof Navigator.prototype.getBattery === 'function' ? source(Navigator.prototype.getBattery) : '',
    toString: source(Function.prototype.toString),
  };
})()
` + "`" + `;

const observe = String.raw` + "`" + `
(async () => {
  const source = (fn) => Function.prototype.toString.call(fn);
  const throwsTypeError = (read) => { try { read(); return false; } catch (error) { return error instanceof TypeError; } };
  const describe = (target, name) => Object.getOwnPropertyDescriptor(target, name);
  const gl = createContext();
  const result = {
    deviceMemoryDefined: describe(Navigator.prototype, 'deviceMemory') !== undefined,
    deviceMemoryInNavigator: 'deviceMemory' in navigator,
    deviceMemory: navigator.deviceMemory === undefined ? null : navigator.deviceMemory,
    maxTouchPoints: navigator.maxTouchPoints,
    doNotTrack: navigator.doNotTrack,
    prototypeReadThrows: throwsTypeError(() => Navigator.prototype.maxTouchPoints),
    maxTouchPointsEnumerable: describe(Navigator.prototype, 'maxTouchPoints').enumerable,
    screenWidth: screen.width,
    getBatteryType: typeof navigator.getBattery,
    batteryManagerDefined: typeof globalThis.BatteryManager !== 'undefined',
    rendererBeforeExtension: gl.getParameter(37446),
    errorBeforeExtension: gl.getError(),
    extensionAvailable: gl.getExtension('WEBGL_debug_renderer_info') !== null,
    vendor: gl.getParameter(37445),
    renderer: gl.getParameter(37446),
    rendererAsString: gl.getParameter('37446'),
    plainRenderer: gl.getParameter(7937),
    getParameterMasked: source(WebGLRenderingContext.prototype.getParameter) === __sources.getParameter,
    maxTouchPointsMasked: source(describe(Navigator.prototype, 'maxTouchPoints').get) === __sources.maxTouchPoints,
    toStringMasked: source(Function.prototype.toString) === __sources.toString,
    devicePixelRatio: devicePixelRatio,
  };
  globalThis.devicePixelRatio = 3;
  result.devicePixelRatioAfterAssignment = devicePixelRatio;
  if (typeof navigator.getBattery === 'function') {
    result.getBatteryNative = source(navigator.getBattery) === __sources.getBattery;
    const battery = await navigator.getBattery();
    result.batteryLevel = battery.level;
    result.batteryCharging = battery.charging;
    result.batteryChargingTime = battery.chargingTime;
    result.batteryPrototypeReadThrows = throwsTypeError(() => BatteryManager.prototype.level);
  }
  if (result.deviceMemoryDefined) {
    result.deviceMemoryMasked = source(describe(Navigator.prototype, 'deviceMemory').get) === __sources.deviceMemory;
  }
  return JSON.stringify(result);
})()
` + "`" + `;

async function evaluate(flags) {
  const context = vm.createContext(vm.constants.DONT_CONTEXTIFY);
  context.__flags = flags;
  vm.runInContext(setup, context);
  context.__sources = vm.runInContext(snapshotSources, context);
  vm.runInContext(runtime, context);
  return JSON.parse(await vm.runInContext(observe, context));
}

(async () => {
  process.stdout.write(JSON.stringify({
    secure: await evaluate({ secure: true, debugInfo: true }),
    secureWithoutDebugInfo: await evaluate({ secure: true, debugInfo: false }),
    insecure: await evaluate({ secure: false, debugInfo: true }),
  }));
})().catch((error) => { console.error((error && error.stack) || error); process.exit(1); });
`

type runtimeScriptObservation struct {
	DeviceMemoryDefined             bool     `json:"deviceMemoryDefined"`
	DeviceMemoryInNavigator         bool     `json:"deviceMemoryInNavigator"`
	DeviceMemory                    *float64 `json:"deviceMemory"`
	MaxTouchPoints                  float64  `json:"maxTouchPoints"`
	DoNotTrack                      *string  `json:"doNotTrack"`
	PrototypeReadThrows             bool     `json:"prototypeReadThrows"`
	MaxTouchPointsEnumerable        bool     `json:"maxTouchPointsEnumerable"`
	ScreenWidth                     float64  `json:"screenWidth"`
	GetBatteryType                  string   `json:"getBatteryType"`
	BatteryManagerDefined           bool     `json:"batteryManagerDefined"`
	RendererBeforeExtension         *string  `json:"rendererBeforeExtension"`
	ErrorBeforeExtension            float64  `json:"errorBeforeExtension"`
	ExtensionAvailable              bool     `json:"extensionAvailable"`
	Vendor                          *string  `json:"vendor"`
	Renderer                        *string  `json:"renderer"`
	RendererAsString                *string  `json:"rendererAsString"`
	PlainRenderer                   string   `json:"plainRenderer"`
	GetParameterMasked              bool     `json:"getParameterMasked"`
	MaxTouchPointsMasked            bool     `json:"maxTouchPointsMasked"`
	ToStringMasked                  bool     `json:"toStringMasked"`
	DevicePixelRatio                float64  `json:"devicePixelRatio"`
	DevicePixelRatioAfterAssignment float64  `json:"devicePixelRatioAfterAssignment"`
	GetBatteryNative                bool     `json:"getBatteryNative"`
	BatteryLevel                    float64  `json:"batteryLevel"`
	BatteryCharging                 bool     `json:"batteryCharging"`
	BatteryChargingTime             float64  `json:"batteryChargingTime"`
	BatteryPrototypeReadThrows      bool     `json:"batteryPrototypeReadThrows"`
	DeviceMemoryMasked              bool     `json:"deviceMemoryMasked"`
}

func TestGeneratedRuntimeScriptRespectsSecureContextSurfaces(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	template := validExtensionTemplate()
	artifact, err := WriteMV3Extension(t.TempDir(), template)
	if err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(t.TempDir(), "harness.js")
	if err := os.WriteFile(harness, []byte(runtimeScriptHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, harness, artifact.ScriptPath).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("runtime script harness failed: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("runtime script harness failed: %v", err)
	}
	var observed struct {
		Unsupported            bool                     `json:"unsupported"`
		Secure                 runtimeScriptObservation `json:"secure"`
		SecureWithoutDebugInfo runtimeScriptObservation `json:"secureWithoutDebugInfo"`
		Insecure               runtimeScriptObservation `json:"insecure"`
	}
	if err := json.Unmarshal(output, &observed); err != nil {
		t.Fatalf("decode harness output: %v\n%s", err, output)
	}
	if observed.Unsupported {
		t.Skip("node lacks vm.constants.DONT_CONTEXTIFY")
	}
	configuration := template.Configuration

	for name, page := range map[string]runtimeScriptObservation{
		"secure": observed.Secure, "secureWithoutDebugInfo": observed.SecureWithoutDebugInfo, "insecure": observed.Insecure,
	} {
		if page.MaxTouchPoints != float64(*configuration.MaxTouchPoints) || page.DoNotTrack == nil || *page.DoNotTrack != configuration.DoNotTrack {
			t.Errorf("%s: always-present navigator surfaces were not overridden: %+v", name, page)
		}
		if !page.PrototypeReadThrows || !page.MaxTouchPointsEnumerable {
			t.Errorf("%s: overridden getter lost its native receiver check or attributes: %+v", name, page)
		}
		if page.ScreenWidth != float64(configuration.ScreenWidth) || page.DevicePixelRatio != configuration.DeviceScaleFactor {
			t.Errorf("%s: screen overrides missing: %+v", name, page)
		}
		if page.DevicePixelRatioAfterAssignment != 3 {
			t.Errorf("%s: devicePixelRatio lost its native [Replaceable] setter: %+v", name, page)
		}
		if !page.GetParameterMasked || !page.MaxTouchPointsMasked || !page.ToStringMasked {
			t.Errorf("%s: Function.prototype.toString masking failed: %+v", name, page)
		}
		if page.RendererBeforeExtension != nil || page.ErrorBeforeExtension != 0x0500 {
			t.Errorf("%s: UNMASKED_RENDERER_WEBGL was answered before WEBGL_debug_renderer_info was enabled: %+v", name, page)
		}
		if page.PlainRenderer != "WebKit WebGL" {
			t.Errorf("%s: an unrelated WebGL parameter changed: %+v", name, page)
		}
	}

	secure := observed.Secure
	if !secure.DeviceMemoryDefined || secure.DeviceMemory == nil || *secure.DeviceMemory != float64(configuration.DeviceMemory) || !secure.DeviceMemoryMasked {
		t.Errorf("secure context deviceMemory override failed: %+v", secure)
	}
	if !secure.ExtensionAvailable || secure.Vendor == nil || *secure.Vendor != configuration.WebGLVendor ||
		secure.Renderer == nil || *secure.Renderer != configuration.WebGLRenderer ||
		secure.RendererAsString == nil || *secure.RendererAsString != configuration.WebGLRenderer {
		t.Errorf("secure context WebGL identity override failed: %+v", secure)
	}
	if secure.GetBatteryType != "function" || !secure.GetBatteryNative || !secure.BatteryPrototypeReadThrows ||
		secure.BatteryLevel != configuration.Battery.Level || secure.BatteryCharging != configuration.Battery.Charging ||
		secure.BatteryChargingTime != float64(configuration.Battery.ChargingTimeSeconds) {
		t.Errorf("secure context battery override failed: %+v", secure)
	}

	withoutDebugInfo := observed.SecureWithoutDebugInfo
	if withoutDebugInfo.ExtensionAvailable || withoutDebugInfo.Vendor != nil || withoutDebugInfo.Renderer != nil {
		t.Errorf("WebGL identity was answered without WEBGL_debug_renderer_info: %+v", withoutDebugInfo)
	}

	insecure := observed.Insecure
	if insecure.DeviceMemoryDefined || insecure.DeviceMemoryInNavigator || insecure.DeviceMemory != nil {
		t.Errorf("insecure context gained navigator.deviceMemory: %+v", insecure)
	}
	if insecure.GetBatteryType != "undefined" || insecure.BatteryManagerDefined {
		t.Errorf("insecure context gained the Battery Status API: %+v", insecure)
	}
}

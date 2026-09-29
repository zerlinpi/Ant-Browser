package fingerprintruntime

const runtimeScriptPrefix = `(() => {
  'use strict';
  const config = Object.freeze(`

const runtimeScriptSuffix = `);
  const nativeSources = new WeakMap();
  const nativeToString = Function.prototype.toString;

  function rememberNative(replacement, original) {
    try { nativeSources.set(replacement, nativeToString.call(original)); } catch (_) {}
    return replacement;
  }

  // Only accessors that this context really exposes are overridden. Chrome
  // hides secure-context APIs such as navigator.deviceMemory on insecure
  // origins, and defining them anyway would itself be a fingerprint. The
  // native getter still runs first so receiver checks ("Illegal invocation")
  // behave as before, and the native setter and attributes are preserved.
  function replaceGetter(target, property, valueFactory) {
    if (!target) return false;
    try {
      const descriptor = Object.getOwnPropertyDescriptor(target, property);
      if (!descriptor || typeof descriptor.get !== 'function' || descriptor.configurable === false) return false;
      const nativeGetter = descriptor.get;
      const getter = rememberNative(function () {
        Reflect.apply(nativeGetter, this, []);
        return valueFactory();
      }, nativeGetter);
      Object.defineProperty(target, property, {
        configurable: descriptor.configurable,
        enumerable: descriptor.enumerable,
        get: getter,
        set: descriptor.set
      });
      return true;
    } catch (_) {
      return false;
    }
  }

  function replaceMethod(target, property, makeReplacement) {
    if (!target) return null;
    try {
      const descriptor = Object.getOwnPropertyDescriptor(target, property);
      const original = descriptor && descriptor.value;
      if (typeof original !== 'function' || descriptor.configurable === false) return null;
      const replacement = rememberNative(makeReplacement(original), original);
      Object.defineProperty(target, property, {
        configurable: descriptor.configurable,
        enumerable: descriptor.enumerable,
        writable: descriptor.writable,
        value: replacement
      });
      return original;
    } catch (_) {
      return null;
    }
  }

  const navigatorPrototype = globalThis.Navigator && Navigator.prototype;
  const screenPrototype = globalThis.Screen && Screen.prototype;
  if (config.deviceMemory) replaceGetter(navigatorPrototype, 'deviceMemory', () => config.deviceMemory);
  if (config.maxTouchPoints !== undefined) replaceGetter(navigatorPrototype, 'maxTouchPoints', () => config.maxTouchPoints);
  if (config.doNotTrack) replaceGetter(navigatorPrototype, 'doNotTrack', () => config.doNotTrack === 'unspecified' ? null : config.doNotTrack);
  if (config.colorDepth) {
    replaceGetter(screenPrototype, 'colorDepth', () => config.colorDepth);
    replaceGetter(screenPrototype, 'pixelDepth', () => config.colorDepth);
  }
  if (config.screenWidth && config.screenHeight) {
    replaceGetter(screenPrototype, 'width', () => config.screenWidth);
    replaceGetter(screenPrototype, 'height', () => config.screenHeight);
    replaceGetter(screenPrototype, 'availWidth', () => config.screenWidth);
    replaceGetter(screenPrototype, 'availHeight', () => config.screenHeight);
  }
  if (config.deviceScaleFactor) replaceGetter(globalThis, 'devicePixelRatio', () => config.deviceScaleFactor);

  if (config.webglVendor && config.webglRenderer) {
    // UNMASKED_VENDOR_WEBGL (37445) and UNMASKED_RENDERER_WEBGL (37446) are
    // answered only when the native call answers them, i.e. when
    // WEBGL_debug_renderer_info is available and enabled on this context.
    // Otherwise the native result (null plus INVALID_ENUM) is left intact.
    const patchWebGL = (prototype) => replaceMethod(prototype, 'getParameter', (original) => function (parameter) {
      const value = Reflect.apply(original, this, arguments);
      if (typeof value !== 'string') return value;
      const name = typeof parameter === 'number' || typeof parameter === 'string' ? Number(parameter) >>> 0 : -1;
      if (name === 37445) return config.webglVendor;
      if (name === 37446) return config.webglRenderer;
      return value;
    });
    if (globalThis.WebGLRenderingContext) patchWebGL(WebGLRenderingContext.prototype);
    if (globalThis.WebGL2RenderingContext) patchWebGL(WebGL2RenderingContext.prototype);
  }

  function seed32(text) {
    let value = 2166136261;
    for (let index = 0; index < text.length; index += 1) {
      value ^= text.charCodeAt(index);
      value = Math.imul(value, 16777619);
    }
    return value >>> 0;
  }

  function mix32(value) {
    value ^= value >>> 16;
    value = Math.imul(value, 0x7feb352d);
    value ^= value >>> 15;
    value = Math.imul(value, 0x846ca68b);
    value ^= value >>> 16;
    return value >>> 0;
  }

  const templateSeed = seed32(config.seed);
  if (config.audioNoise && globalThis.AudioBuffer) {
    const prototype = AudioBuffer.prototype;
    const originalGetChannelData = prototype.getChannelData;
    const noisedChannels = new WeakMap();
    function ensureAudioNoise(buffer, channel) {
      let channels = noisedChannels.get(buffer);
      if (!channels) {
        channels = new Set();
        noisedChannels.set(buffer, channels);
      }
      if (channels.has(channel)) return Reflect.apply(originalGetChannelData, buffer, [channel]);
      const samples = Reflect.apply(originalGetChannelData, buffer, [channel]);
      for (let index = 0; index < samples.length; index += 1) {
        const unit = mix32(templateSeed ^ Math.imul(channel + 1, 0x9e3779b1) ^ index) / 4294967295;
        const value = samples[index] + (unit - 0.5) * 2e-7;
        samples[index] = Math.max(-1, Math.min(1, value));
      }
      channels.add(channel);
      return samples;
    }
    replaceMethod(prototype, 'getChannelData', () => function (channel) {
      return ensureAudioNoise(this, Number(channel) || 0);
    });
    replaceMethod(prototype, 'copyFromChannel', (original) => function (destination, channelNumber, startInChannel) {
      ensureAudioNoise(this, Number(channelNumber) || 0);
      return Reflect.apply(original, this, arguments);
    });
  }

  function stableIdentifier(kind, index) {
    let output = '';
    let state = seed32(config.seed + ':' + kind + ':' + index);
    for (let part = 0; part < 4; part += 1) {
      state = mix32(state ^ part);
      output += state.toString(16).padStart(8, '0');
    }
    return output;
  }

  if (config.mediaDevices && globalThis.MediaDevices) {
    const mediaPrototype = MediaDevices.prototype;
    replaceMethod(mediaPrototype, 'enumerateDevices', () => async function () {
      const devices = [];
      const append = (kind, count) => {
        for (let index = 0; index < count; index += 1) {
          const values = Object.freeze({
            deviceId: stableIdentifier(kind, index),
            groupId: stableIdentifier(kind + ':group', index),
            kind: kind,
            label: ''
          });
          const prototype = globalThis.MediaDeviceInfo ? MediaDeviceInfo.prototype : Object.prototype;
          const device = Object.create(prototype);
          for (const property of ['deviceId', 'groupId', 'kind', 'label']) {
            Object.defineProperty(device, property, { configurable: true, enumerable: true, get: () => values[property] });
          }
          Object.defineProperty(device, 'toJSON', { configurable: true, value: () => ({ ...values }) });
          devices.push(device);
        }
      };
      append('audioinput', config.mediaDevices.audioInputs);
      append('videoinput', config.mediaDevices.videoInputs);
      append('audiooutput', config.mediaDevices.audioOutputs);
      return devices;
    });
  }

  if (config.battery) {
    // navigator.getBattery and BatteryManager exist only in secure contexts
    // and are never added where Chrome hides them. getBattery itself stays
    // native (including its permission-policy rejections); only the manager
    // accessors report the configured values.
    const getBatteryDescriptor = navigatorPrototype && Object.getOwnPropertyDescriptor(navigatorPrototype, 'getBattery');
    if (getBatteryDescriptor && typeof getBatteryDescriptor.value === 'function' && globalThis.BatteryManager) {
      const batteryPrototype = BatteryManager.prototype;
      replaceGetter(batteryPrototype, 'charging', () => config.battery.charging);
      replaceGetter(batteryPrototype, 'level', () => config.battery.level);
      replaceGetter(batteryPrototype, 'chargingTime', () => config.battery.chargingTimeSeconds);
      replaceGetter(batteryPrototype, 'dischargingTime', () => config.battery.dischargingTimeSeconds);
    }
  }

  if (config.fonts && config.fonts.length) {
    const allowedFonts = new Set(config.fonts.map((font) => font.toLocaleLowerCase('en-US')));
    const fontSpecContainsAllowedFamily = (fontSpec) => {
      const normalized = String(fontSpec || '').toLocaleLowerCase('en-US');
      for (const family of allowedFonts) {
        const quotedDouble = '"' + family + '"';
        const quotedSingle = "'" + family + "'";
        if (normalized.includes(quotedDouble) || normalized.includes(quotedSingle) ||
            normalized.split(',').some((part) => part.trim().endsWith(family))) return true;
      }
      return false;
    };

    if (globalThis.FontFaceSet) {
      replaceMethod(FontFaceSet.prototype, 'check', () => function (font) {
        return fontSpecContainsAllowedFamily(font);
      });
    }

    const patchMeasureText = (prototype) => replaceMethod(prototype, 'measureText', (original) => function () {
      const currentFont = String(this.font || '');
      if (!fontSpecContainsAllowedFamily(currentFont)) {
        const match = currentFont.match(/^(.*?\b\d+(?:\.\d+)?(?:px|pt|pc|in|cm|mm|em|rem|%)(?:\/[^\s]+)?)(?:\s+).*$/i);
        if (match) {
          try {
            this.font = match[1] + ' sans-serif';
            const result = Reflect.apply(original, this, arguments);
            this.font = currentFont;
            return result;
          } catch (_) {
            try { this.font = currentFont; } catch (_) {}
          }
        }
      }
      return Reflect.apply(original, this, arguments);
    });
    if (globalThis.CanvasRenderingContext2D) patchMeasureText(CanvasRenderingContext2D.prototype);
    if (globalThis.OffscreenCanvasRenderingContext2D) patchMeasureText(OffscreenCanvasRenderingContext2D.prototype);

    if (typeof globalThis.queryLocalFonts === 'function') {
      const originalQueryLocalFonts = globalThis.queryLocalFonts;
      const replacement = rememberNative(async function () {
        const fonts = await Reflect.apply(originalQueryLocalFonts, this, arguments);
        return Array.from(fonts || []).filter((font) => {
          const family = String(font && font.family || '').toLocaleLowerCase('en-US');
          const fullName = String(font && font.fullName || '').toLocaleLowerCase('en-US');
          return allowedFonts.has(family) || allowedFonts.has(fullName);
        });
      }, originalQueryLocalFonts);
      try { Object.defineProperty(globalThis, 'queryLocalFonts', { configurable: true, writable: true, value: replacement }); } catch (_) {}
    }
  }

  const replacementToString = function () {
    if (nativeSources.has(this)) return nativeSources.get(this);
    return Reflect.apply(nativeToString, this, arguments);
  };
  rememberNative(replacementToString, nativeToString);
  try {
    Object.defineProperty(Function.prototype, 'toString', {
      configurable: true,
      enumerable: false,
      writable: true,
      value: replacementToString
    });
  } catch (_) {}
})();
`

# Browser Runtime

Enterprise browser execution layer.

Architecture:

```
Instance
  |
Profile
  |
Fingerprint Template
  |
Runtime Injector
  |
Chromium
```

Modules:

- launcher
- profile-loader
- fingerprint-loader
- cdp
- session

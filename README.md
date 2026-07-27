# Foilen Box

> **⚠ Warning:** This is a prototype and experimental. APIs, features, and stability are not guaranteed.

A single tool that bundles many features instead of having multiple separate tools — a box of useful utilities.

All business logic is plain Go. The UI is a small vanilla HTML/CSS/JS app (no npm, no build step) served by
an embedded HTTP+WebSocket server bound to `127.0.0.1` only:

- **Desktop** — a system tray-only Go binary (Linux/macOS/Windows). "Open" launches the default browser at
  the local UI; "Quit" stops the server and exits.
- **Android** — a native Android Studio app whose `MainActivity` starts the same Go server in-process (built
  as an AAR via `gomobile bind`) and shows the UI in a `WebView`. Real-time GPS uses the standard
  `navigator.geolocation` Web API in JS, backed by WebView's native geolocation permission plumbing — no
  custom native location bridge required.

# Build

## Requirements

- Go 1.23+
- For the desktop tray icon (`github.com/getlantern/systray`), on Linux:
  `sudo apt-get install pkg-config libgtk-3-dev libayatana-appindicator3-dev` (or `libappindicator3-dev` on
  older distros)
- For the Android build: Android Studio or a standalone Android SDK/NDK (`ANDROID_HOME` /
  `ANDROID_NDK_HOME`), a JDK 17, and `gomobile` (`go install golang.org/x/mobile/cmd/gomobile@latest`,
  installed automatically by `step-package.sh` if missing). NDK r24+ dropped support for API level 16, so
  `step-package.sh` builds with `-androidapi 21`; use an NDK release whose `meta/platforms.json` covers API
  21 (e.g. r24 through r27). `android/` has no committed Gradle wrapper — either run `gradle wrapper` inside
  it once (needs a system Gradle matching the Android Gradle Plugin version in `android/build.gradle.kts`)
  or have a system `gradle` on `PATH`.

## Local build (with tests)

```bash
./create-local-release.sh
```

Optionally pass a version:

```bash
./create-local-release.sh 1.2.3
```

## Local build (skip tests)

```bash
./create-local-release-no-tests.sh
```

## Individual steps

```bash
./step-clean.sh              # go clean + remove dist/
./step-compile.sh            # go build + go test
./step-compile-no-tests.sh   # go build only
./step-clean-compile.sh      # clean + compile (with tests)
./step-package.sh            # package desktop binary/archive + Android APK
```

# Desktop

After building, the binary is at:

```
dist/desktop/foilen-box
```

Run it directly:

```bash
./dist/desktop/foilen-box
```

A system tray icon labeled **"Box"** appears if a graphical environment is available. Its "Open" menu item
launches the default browser at the local UI (`http://127.0.0.1:<random port>/`); "Quit" stops the server
and exits.

A distributable archive (`.tar.xz`) is also written to `dist/desktop/`.

# Android

After building, the APK is at:

```
dist/android/Foilen_Box.apk
```

Install it on a connected device or emulator:

```bash
adb install dist/android/Foilen_Box.apk
```

The app requests the `ACCESS_FINE_LOCATION`/`ACCESS_COARSE_LOCATION` permissions on first launch; the GPS
tab uses the browser `navigator.geolocation.watchPosition` API for continuous, real-time updates (device
location services must be enabled).

## Install a signed release APK on a physical device

The APK produced by `step-package.sh` is unsigned and must be signed before it can be installed. Make sure
`adb` and the Android build tools are on your `PATH`.

**1. Enable USB debugging on your phone**

- Go to **Settings → About phone** and tap **Build number** 7 times to unlock Developer options.
- Go to **Settings → Developer options** and enable **USB debugging**.
- Connect the phone via USB and accept the RSA key fingerprint prompt on the device.

**2. Verify adb sees your device**

```bash
adb devices
```

You should see your device listed as `device` (not `unauthorized`).

**3. Sign and install**

```bash
./install-dev-apk.sh
```

The script generates a local signing keystore under `_local_keys/` on the first run (skipped on subsequent
runs), aligns and signs the APK, then installs it on the connected device.

# Project structure

| Path                        | Description                                                                          |
|------------------------------|--------------------------------------------------------------------------------------|
| `cmd/foilenbox`              | Desktop entry point: starts `internal/webserver` and the systray                     |
| `cmd/mobile`                 | `gomobile bind` entry point wrapping `internal/webserver` for the Android AAR        |
| `internal/webserver`         | Embedded HTTP+WebSocket server serving `web/` and dispatching UI API calls           |
| `internal/early`             | Early.co time-tracking API client, config persistence, and aggregation               |
| `internal/troubleshooting`   | DNS subdomain crawl and WHOIS client/parser                                          |
| `internal/spec`              | System information report (OS/CPU/RAM/disk)                                          |
| `internal/browseropen`       | Opens the OS default browser (desktop only)                                          |
| `internal/webserver/web`     | Vanilla HTML/CSS/JS UI shared by desktop (browser tab) and Android (WebView)          |
| `android`                    | Android Studio/Gradle project (`MainActivity` hosts the WebView + Go AAR server)     |
| `assets`                     | App icon and tray icon                                                               |

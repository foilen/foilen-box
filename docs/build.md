# Build notes

## Vendor assets

Fonts and JS libraries are mirrored to local files at build time so the web UI never fetches from a CDN at
runtime. They aren't committed.

- Nix: `vendorFonts` and `vendorJs` are fixed-output derivations. The sandbox allows network access for them
  since the output is verified against `outputHash`. To update one, set its `outputHash` to a wrong value, run
  `nix build`, and copy the "got:" hash back.
- Without Nix: `scripts/fetch-vendor-assets.sh`, which skips assets already present (delete the `vendor-*`
  directory to refetch).

## Go

- The repo is a Go workspace (root module + `realm/`). `go mod vendor` doesn't know about workspaces, so Nix
  vendors with `go work vendor`.
- Nix sets `internal/camera.ffmpegPath` to a store path so the package doesn't depend on a system ffmpeg.

## Android

- `-checklinkname=0`: `github.com/wlynxg/anet` uses `//go:linkname` into unexported `net` internals for
  cgo-free interface lookups on Android. Since Go 1.23 the linker validates these, and its reference no longer
  matches Go 1.26.
- Gradle: uses `android/gradlew` if present (`gradle wrapper` once to generate it), otherwise a system
  `gradle`.
- `network_security_config.xml` allows cleartext to `127.0.0.1`/`localhost` since the WebView loads the UI
  from the local Go server, which Android 9+ blocks by default.

## Dev scripts

`start-dev-desktop*.sh` unset GTK/GIO/locale variables leaked by the VS Code snap: they point at snap libs
whose libpthread is incompatible with the system glibc and crash the GTK binary.

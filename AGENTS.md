# Documentation

Depending on the needs for the requested task, read the following documentation:
- Writing a Realm feature (`realm/`, `internal/`): check `docs/features.md`
- Code review:
  - Check the respective documentations based on the nature of changes and ensure the respect of the docs.
  - Check more info in `docs/Code Review.md`

# Instructions

- If you are unsure about what path to take when there are multiple choices:
  - Check if you can find a similar feature in this project
  - Ask the requester
- Creating a new encrypted-realmmap feature (web UI + storage): see `docs/pattern-encrypted-realmmap-feature.md` for design guidelines extracted from SMS.

- You can create/update tests and compile them, but do not run them unless explicitly asked to
- Keep comments in the code to minimum. If it just repeat in english what the code is doing, it is not needed. Keep it short.
- When updating `go.mod` (e.g. Go version, dependencies), also update `flake.nix` (module version, `vendorHash`, etc.) to match

# Techno pointers

- Go (see `go.mod` for the version), using `go build`/`go test` (wrapped by `step-compile.sh` and friends)
- `realm/` is a standalone libp2p library (peer identity, discovery, permissions); `internal/` holds the
  foilen-box-specific application code (webserver, features, etc.)
- Desktop: a system tray Go binary (Linux/macOS/Windows) built with `github.com/getlantern/systray`
- Android: a native Android Studio/Gradle app whose `MainActivity` starts the same Go server in-process,
  built as an AAR via `gomobile bind`
- UI: vanilla HTML/CSS/JS (no npm, no build step) served by an embedded HTTP+WebSocket server, shared
  between the desktop browser tab and the Android WebView
- State is persisted to local files/leveldb

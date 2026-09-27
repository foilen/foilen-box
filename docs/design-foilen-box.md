# Foilen Box design notes

Design rationale for `cmd/` and `internal/`. For the Realm library, see `design-realm.md`.

## Entry points (`cmd/`)

- `time.Local` is forced to UTC, matching the original Java app.
- Desktop: on Linux without a display, the systray is skipped since GTK calls `exit()` itself (not
  recoverable) when no display is available. macOS/Windows don't need this check.
- Mobile (`cmd/mobile`): the gomobile entry point used by Android.
  - `StartServer` can be called by `RealmForegroundService` on boot before `MainActivity` exists. The device
    name only applies on the first call; bridges (state sink, battery, SMS, camera) are re-applied on every
    call.
  - Android provides the device name (the OS hostname is always `localhost`) and the OS version (no
    `/etc/os-release`).
  - Each bridge interface is declared three times (in `cmd/mobile`, `internal/webserver` and the consuming
    package) to keep gomobile types out of the internal packages; they are structurally identical.
  - gomobile only supports one non-error return value and `int32` rather than `int`, hence
    `ConnectedPeersCount`/`PeersTotalCount` as two calls and the `int32` parameters.

## Web server (`internal/webserver`)

- Bound to `127.0.0.1` only. A random per-process token is embedded in the served `index.html` and must be
  the first WebSocket message. This stops other local pages or processes from opening a cross-origin
  WebSocket and reading data such as the Early API secret. Because of that token, the origin check is
  permissive.
- `webui.json` (`uiConfig`):
  - Random free port by default; unchecking "Random" pins the port. If the pinned port can't be bound, it
    falls back to a random one.
  - The config API reports the bound port when none is pinned, so the textbox is pre-filled.
  - `ClearLogsOnStartup` is a pointer so a file saved before the field existed is distinguished from an
    explicit `false`; it defaults to `true`.
  - Tab/subtab activation counts let the UI order tabs most-used first.
- The Realm engine is auto-started when a peer id exists; a failure doesn't block the UI.
- The Realm listen port is assigned once and persisted so the advertised addresses stay stable, unless the
  user picked a specific port.
- Pushed identities and groups are imported without confirmation, renamed on a name collision.
- The camera "expose as Realm Service" toggle adds or removes a `camera` entry in `Config.Services`; being in
  that list is what makes the RTSP port reachable through `common/services`.
- `Version`/`CommitDate` are injected with `-ldflags` (see `step-package*.sh`, `flake.nix`).

## Camera (`internal/camera`)

- Exposes the local camera as an RTSP stream.
- Desktop: capture starts when the first RTSP viewer plays and stops `stopCaptureDebounce` after the last one
  leaves (absorbs SETUP-then-PLAY sequencing and quick reconnects).
- Android (`ManualControl`): Android 14+ refuses to start a camera foreground service unless the app is in
  the foreground, which a background RTSP client can't guarantee. So capture only starts from a button press
  in the app.
- Changing the device while someone watches stops the capture; it resumes on the next viewer instead of being
  hot-swapped (simpler, rare case).
- Desktop capture uses ffmpeg (`v4l2`/`avfoundation`/`dshow`) writing Annex-B H.264 to stdout, or MPEG-TS
  with AAC when a microphone is selected.
  - `sliced-threads=0`: x264 defaults to one slice per thread, and the Annex-B reader treats a VCL NAL as the
    end of an access unit, so multi-slice frames would be split.
  - `ffmpegPath` is overridden by Nix builds with `-ldflags -X` so the package doesn't depend on a system
    ffmpeg.
  - When ffmpeg fails, the bare `EOF` is replaced with its last stderr lines.
  - Device listing commands always exit non-zero after printing the list, so the error is ignored.
  - ALSA `default` is not listed: the bundled ffmpeg can't load the system PipeWire/PulseAudio ALSA plugin, so
    only `hw:` devices work.
  - Linux devices are listed from `/dev/video*` and sysfs (no ioctl). UVC cameras may expose metadata nodes;
    all are listed and a wrong pick fails with a clear error.
- Android capture (`bridgeCapturer`): the bridge only carries lifecycle calls since gomobile call overhead is
  unsuitable for per-frame data. Go opens a loopback listener and the Android side connects to it with
  Annex-B H.264. With a microphone, it opens two connections, each starting with a tag byte (`V` video,
  `A` audio) and audio is a stream of 4-byte big-endian length-prefixed AAC-LC access units.
  - Audio problems never tear down the video.
  - AAC access units larger than 8KB mean a desynced or hostile stream.
- RTP timestamps come from wall-clock elapsed time since the raw streams carry no usable timestamps.
- SPS/PPS are stored in the stream description as they're seen, because a MediaCodec encoder only emits them
  once, and a viewer can connect after capture started.
- Audio is AAC-LC at a fixed rate/layout so the session description, built before capture starts, matches.
- The RTSP session-close callback runs on a goroutine that `Server.Close()` waits for while `Manager.mu` is
  held, so it notifies the manager asynchronously.

## SMS (`internal/sms`)

See `pattern-encrypted-realmmap-feature.md` for the general pattern.

- One Android device is the authority for its own SMS, synced through an encrypted map `SMS-<suffix>`.
  `Manager` registers as a Realm feature only for the periodic tick.
- The local config (which store this device manages) is never synced.
- Keys:
  - Message: `<peerId>/<unixMillis>/<hash>`, the hash avoiding collisions in the same millisecond.
  - Send request: `<peerId>/create/<uniqueId>`, fulfilled by that device, then removed.
  - Presence marker: `<peerId>/enabled`, rewritten every tick so it survives auto-delete; used to only offer
    "Send from peer" for peers that can fulfill a request.
- The device store is reconciled every tick (adds missing messages, removes deleted ones), which also retries
  a failed first import (e.g. missing `READ_SMS` permission). Pending send requests are retried too.
- Polls maps every 5s (no push notification for map changes), same cadence as the web UI.
- Notifications only for messages newer than 10 minutes, so an import or first sync doesn't notify every old
  message.
- MMS attachments aren't carried (map values are capped well below typical attachment sizes); a `(media)`
  placeholder is used.
- Deep links use the web UI hash format (`groupId|storeName|phoneNumber`). `encodeURIComponent` is
  reimplemented since `url.QueryEscape` encodes spaces as `+`.

## Group troubleshooting (`internal/grouptroubleshooting`)

- A 10-minute session visualizing which peers of a group are connected to each other (direct or relay).
- Uses the group's `common` map under `groupTroubleshooting/` instead of a dedicated map: every member already
  subscribes to it, starting a session is just writing a future expiration, and entries stay afterwards as a
  record of the last run.
- Each member responds to a new start with a `started` entry; the UI uses the gap between both timestamps as
  that peer's map propagation latency.
- The connections entry is refreshed every 15s while the session is active and only written when it changed.

## Speed test (`internal/speedtest`)

- App-specific, so it lives here rather than in `realm/features`.
- Protocol and timing follow github.com/foilen/LANSpeedTest: 1-byte mode, ack, then 5s of data per
  direction; the initiator closes the stream to stop. Chunks are 64KB (instead of 1KB) so yamux framing
  overhead doesn't cap throughput.
- Tests are serialized so concurrent runs don't skew each other.

## System spec (`internal/spec`)

- GPU detection shells out to OS tools since gopsutil has no GPU support. On Linux, AMD marketing names come
  from `amdgpu.ids` since lspci can't disambiguate SKUs sharing a device id. On Windows, `AdapterRAM` is
  32-bit and overflows above 4GB, so VRAM comes from the registry and `AdapterRAM` is only a fallback.
- Battery: on Android, sysfs is usually SELinux-blocked, so Kotlin's `BatteryManager` provides it. That bridge
  call is occasionally flaky (a single read can transiently come back empty), so `GetSpec` reads it once per
  call and reuses the result for both the report text and the summary — reading it twice (once per output) let
  the two disagree, e.g. the periodic announce's summary showing no battery while the report text it was
  bundled with did.
- Disk usage falls back to the app storage path on Android, where the sandbox often can't stat system mounts.

## Logging (`internal/logging`)

- Desktop tray and Android have no console, so the standard logger goes to a file rotated at 100MB or 1 day.
- The creation time is kept in a sidecar file so age-based rotation survives restarts.
- The Logs tab only reads the last 512KB.

## Notifications (`internal/notify`)

beeep has no portable click callback, so `NotifyClick` is per OS:

- Linux: a dbus "default" action.
- macOS: `terminal-notifier -open`, falling back to a plain notification.
- Windows: toast with protocol activation, which avoids COM activator/AUMID registration.

## Early (`internal/early`) and troubleshooting (`internal/troubleshooting`)

Ported from the original Java app and kept behavior-compatible: config read errors are swallowed, the Early
client is single-session (not safe for concurrent use), the DNS crawl uses the same subdomain seed list, the
WHOIS client is hand-rolled over TCP port 43 and supports the same 5 TLDs, and DNS entries sort like the Java
`compareTo`.

## Browser/connect helpers (`internal/browseropen`)

- Launchers use `Run` since they can exit non-zero after opening fine.
- OpenVPN: the real addresses used by the libp2p connection to the peer are excluded from
  `redirect-gateway`, otherwise the tunnel carrying the VPN traffic would be cut.

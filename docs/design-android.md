# Android design notes

Design rationale for `android/`. The Go side of the bridges is described in `design-foilen-box.md`.

## Build (`app/build.gradle.kts`)

- `libs/foilenbox.aar` is built by `gomobile bind` (see `step-package.sh`) and not committed. `minSdk` must stay
  high enough for gomobile's toolchain.
- Release lint is disabled: AGP 8.7's lint fails with compileSdk 36 (opaque "25.0.4" error).
- CameraX: only core/camera2/lifecycle are needed since the preview is wired straight into a MediaCodec input
  surface.
- `lifecycle-service` makes `CameraForegroundService` a `LifecycleOwner` so CameraX can bind to it without an
  Activity.
- WorkManager is the service watchdog (see below).

## `MainActivity`

- Hosts the Go server (`Mobile.startServer`) in a WebView. GPS uses the standard `navigator.geolocation` API,
  which works once the geolocation prompt is granted; there is no native location bridge.
- WebView cache is disabled: the UI is embedded in the APK and changes on each update, while the WebView HTTP
  cache survives reinstalls and would keep serving old JS/CSS.
- `onResume` re-checks the server URL since the WebView can go stale after a long backgrounding (e.g. the
  server restarted on another port).
- No `onDestroy` override: `RealmForegroundService` owns the engine.
- Asks for the battery optimization exemption: without it, the OS kills the service process under Doze or
  memory pressure and doesn't reliably restart it.
- The device name is used as the peer hostname since the OS hostname is always `localhost`.
- Implements the gomobile bridges: `RealmStateSink`, `BatteryProvider` (sysfs is SELinux-blocked), and
  `SmsBridge`.
- SMS notifications open `MainActivity` with `FLAG_ACTIVITY_CLEAR_TASK`, which forces a fresh `onCreate`, so the
  deep link is handled in one place for both cold and warm starts.

### SMS/MMS import

- Reading `content://sms` and `content://mms` only needs `READ_SMS`, not being the default SMS app. Likewise
  `RECEIVE_SMS` for `SmsReceivedReceiver`.
- SMS permissions are requested on demand (`SmsPermissionBridge`) since most users never enable the feature.
- MMS covers media messages, group texts and RCS/long-SMS fallbacks. `Mms.DATE` is in seconds (`Sms.DATE` is in
  milliseconds).
- For an MMS, the address is the sender (incoming) or the first recipient (outgoing), matching the
  per-phone-number conversation model. Address types 137/151 are `PduHeaders.FROM`/`TO`.
- The body joins the `text/plain` parts and appends `(media)` when another part exists; `application/smil` is
  the layout descriptor and is ignored. Some OEMs don't fill the inline `text` column, so the part stream is
  read instead.
- A `raw` dump of every column is included temporarily to find an undocumented column for the SMS app's
  "Trash" state.
- A multipart SMS arrives as several PDUs in one intent; they are concatenated into one message.

## `RealmForegroundService`

- Keeps the process at foreground priority so Android doesn't freeze or kill the engine's goroutines.
- Also starts the server itself since `BootCompletedReceiver` can start it without `MainActivity`;
  `Mobile.startServer` is idempotent.
- The engine is **not** stopped in `onTaskRemoved`: that callback also fires when the OS trims a backgrounded
  task, and stopping there left the box offline for hours. Only the Realm on/off toggle stops it.
- When Realm is turned off, the foreground notification is dropped and the service is no longer "expected", so
  neither the watchdog nor a `START_STICKY` restart brings it back. The service stays alive to restore
  everything if Realm is re-enabled in the same process.
- `startForeground` must be called promptly after `startForegroundService`, even when Realm is off.
- The notification shows peer counts, refreshed by polling every 5 minutes since Go doesn't push them.
- Multicast lock: UDP broadcast discovery needs it (broadcast frames fall under the Wi-Fi non-unicast filter),
  but holding it permanently wakes the radio for every LAN broadcast. It is only needed to discover *new*
  peers, so it is duty-cycled: held one tick out of 6 (~5 minutes on, ~25 off).
- `onTimeout` is a safety net in case the OS starts enforcing a time limit on this FGS type.
- `setRealmEnabled` uses `startService` since the service is already running at that point.

## `ServiceWatchdog`

`START_STICKY` alone doesn't reliably restart the service after a SIGKILL, and an `AlarmManager` inexact alarm
stopped firing once the process was cached-empty (Doze defers it and the freezer withholds delivery).
WorkManager periodic work runs in a fresh process, survives reboots and fires in Doze maintenance windows.
Every ~15 minutes it restarts the service when it's expected to run (`AndroidConfigPrefs.isServiceExpected`)
but isn't (`RealmForegroundService.isRunning`, a static reset with the process).

## `BootCompletedReceiver`

Starts the service at boot when enabled from the Android tab. Android allows starting a foreground service
from `BOOT_COMPLETED`, so no permission beyond `RECEIVE_BOOT_COMPLETED` is needed.

## Camera (`CameraCaptureBridge`, `CameraForegroundService`)

- Capture runs in a foreground `LifecycleService` rather than `MainActivity` so it keeps streaming with the
  screen off. It is foreground only while capturing, so the camera-in-use indicator is accurate.
- Transport to Go: loopback TCP, see "Camera" in `design-foilen-box.md`. The socket is connected before the
  encoder starts so the first buffers (SPS/PPS/IDR) have somewhere to go, and off the main thread since
  StrictMode forbids blocking I/O there.
- MediaCodec callbacks run on the main thread (the executor thread has no Looper), so buffers are copied and
  released there and the socket write happens on the single-threaded encoder executor, keeping frame order.
- The preview resolution is pinned to the encoder surface size; otherwise CameraX picks its own and the frames
  are scaled/pillarboxed.
- Audio: a single logical input (`AudioSource.MIC`). A synchronous read/encode/write loop, independent from the
  surface-driven video encoder. Under back-pressure a PCM chunk is dropped. Audio failures leave the socket
  open so the Go side's accept still succeeds and video is unaffected. Sample rate and channels must match
  `internal/camera/mpegts.go`.

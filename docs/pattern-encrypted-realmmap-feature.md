# Encrypted Realm Map Feature Pattern

This document describes a generic pattern for building features that use an encrypted realm map as their backend. It's extracted from the SMS feature (`internal/sms`) and can be reused for future similar features.

## Overview

Features following this pattern:
1. Use `foilen-realm/features/maps` (or any other encrypted-backed maps provider) for storage/sync
2. Implement `realm.Feature` only to hook into periodic ticks / engine lifecycle events, not defining their own wire protocols—wire traffic still goes through the underlying map's feature(s).
3. Expose public methods on a manager type that callers use directly (the generic `[Engine]`(engine) has no `"call any feature"` abstraction beyond listing available actions and calling `feature.(list|get/set/delete)...`).

---

## Web UI Side Pattern

### Auto-refresh via polling

Since there's typically no push notification for realm map changes, the web UI polls on a fixed cadence:
```js
const POLL_INTERVAL_MS = 5000; // or whatever fits your feature

setInterval(() => report(output, refreshFeatureData), POLL_INTERVAL_MS);
```
or use an engine-provided `OnTick` callback with similar periodicity logic in Go.

### Configuration section (collapsible)

- Place config controls inside a collapsible `<div>` controlled by a text-button:
  ```html
  <div id="<feature>-config">
    <md-text-button id="<feature>-config-toggle">▼ Configuration</md-text-button>
    <div id="<feature>-config-body" class="hidden"> ... </div>
  </div>
  ```
- If not on the platform where config is relevant (e.g. Android-only setting), hide/remove it entirely:
  ```js
  const configSection = document.getElementById("<FEATURE>-config");
  if (!isAndroid) {
    configSection.remove(); // or set hidden
  }
  ```

- When saving configuration, call an API method like `saveManagementConfig`:
  ```js
  await api.call("feature.saveManagementConfig", { enabled, groupId, storeName, ... });
  ```
- Collapse the section when disabled; show it (or auto-select a default) on enable. This matches SMS behavior: collapsed by default until activated via a create or use button.

---

## Realm Map Side Pattern

### Store naming & prefixing

Define one `storePrefix` const and derive store names with a parameterized suffix:
```go
const storePrefix = "<FEATURE>-" // "SMS-", "SCR-" etc.

func StoreNameFor(suffix string) string { return storePrefix + suffix }
```

Provide a helper to check if a name belongs to this feature's storespace (used in periodic loops and handlers): `Is<Feature>Store(name)` returning true for prefixed names, false otherwise. This gates map reads/writes/iteration so only relevant entries are processed while still allowing other maps' data on the same peerstore or group store to pass through without being touched by this feature's logic.

### Key structure: three segments with semantic kinds

Keys use a fixed pattern that lets a parser split into pieces via `strings.SplitN(key, "/", 3)`:
```text
<identifier>/<kindSegment>[/<optional-extra>]
```

- `<identifier>` — whatever identifies the "author" or primary key dimension (peer ID for SMS, user/service name etc.). For presence markers it might be omitted and only contain `/enabled`, but generally each row belongs to some known actor. Keep segments predictable so `parseKey` can reliably reconstruct them on every incoming entry; this simplifies map iteration because you know exactly how many elements exist before trying JSON unmarshalling, which saves CPU in hot loops.

- `<kindSegment>` — indicates the meaning of the key: use a distinct tag rather than guessing "if it has 3 parts then data else something else". Common kinds from SMS can be adapted as-is:
  - `""` (empty string) for a standard value record (no kind segment needed; presence alone is all there is). This happens when you have exactly three total parts and the middle one isn't reserved, indicating normal content.
  A create-request has an explicit `/create`, letting your manager logic say "this entry exists but only pending completion", not conflating a request with final data that was already materialized in another shape within this key space (or elsewhere after it fulfilled). You can delete the create-row immediately upon fulfillment and replace by writing out its canonical record; similarly you could define `/delete` or other lifecycle tags if your future feature wants richer states besides just "exists". A presence marker such as an enabled flag doesn't encode data in its value—you only need to set any non-empty string (e.g. `"1"`) and then test for key deletion rather than inspecting JSON body fields that could corrupt unmarshalling semantics; this reduces per-entry work because the entire state is represented through map metadata alone, not payload inspection logic inside hot loops: a simple delete-or-touch check becomes your complete gate against which to decide if something changed relative to local expectations.

- `<optional-extra>` — for time-based records (messages), a unix millisecond timestamp works; or use short hashes like 16-char hex of the marshaled JSON value's bytes, just as SMS does: `hashValue` returns first 16 chars of SHA256 sum of the byte-array so duplicates in same second get distinct keys. For simpler features where uniqueness by peer+time suffices you can drop hashing entirely but for robust duplicate handling especially when multiple clients could write concurrently without coordination across peers, use this deterministic hash; on unstructured incoming data feeds like live sensors it ensures one message per millisecond stays uniquely keyed so deletion semantics work correctly since the entry's lifetime is bounded: `timestamp + T` seconds where you define auto-delete window either via map config or global feature loop (AutoDeleteEntriesHours). This avoids growing ever-greater maps and keeps memory stable; your periodic cleanup routine can run on 10-minute intervals if you set a longer threshold in configuration, but always delete both old entries and those marked for removal during reconciliation.

### Writing values: marshal → store as string
```go
var msg MyMessage // define struct fields that map to config schema or JSON formatters' expectations
data, err := json.Marshal(msg)                          // []byte of content you want persisted remotely
if err != nil { handleMarshallingError(...) }            // log and maybe return early if marshal fails mid-cycle

ts := time.Now().UnixMilli()                             // capture local wall clock instant; remote peers will store it too when syncing, unless they have their own offset correction logic built in to the maps feature
key := fmt.Sprintf("%s/%d/%x", identifier, ts, hash(data)[:16]) // build key string

if err := mapFeature.SetValue(groupID, storeName, key, string(data)); err != nil {
  log.Printf("<feature>: failed to write %s: %v", key, err)
}
```
- Use `SetValue` from the maps feature; it handles encryption and writes in background as part of whatever replication topology is configured or automatically built into that underlying store abstraction. You don't expose unencrypted blobs outside your peerstore (e.g., to disk via configmaps without proper identity wrapping), ensuring only authorized peers who share keys can read what you write:
- Always log errors but defer cleanup; a failed setValue might mean temporary loss of connectivity or permissions, so subsequent periodic tick will retry on engine heartbeat cadence.

### Create-request pattern (with optional fulfillment)
```go
type MyCreateRequest struct { /* JSON fields for request payload */ }

const reqKind = "create" // segment name distinguishing requests from final data in keyspace

func createKey(peerID string, uniqueId string) string { return peerID + "/" + reqKind + "/" + uniqueId }

uid, err := randomHex()       // 16-32 byte buffer -> hex via crypto/rand
if err != nil { handleRandomError(...) } // retry or log fallback as needed

req := MyCreateRequest{ /* populate from input */ }
data, _ := json.Marshal(req);    // ignore error in hot path if you always validate upfront; wrap outer caller side for user-visible errors anyway

return mapFeature.SetValue(groupID, storeName, createKey(targetPeerID, uid), string(data))
```
- When other peers receive this entry via sync or poll loop: they decode the JSON and route it to a fulfiller like `fulfillCreate`. If you're Android-only with native SMS sending available through your app bridge: write a helper that calls telephony APIs (e.g. send an outbound text message) then delete row on success, otherwise log failure but don't touch original request; retry logic lives elsewhere via either periodic tick or immediate polling loop in feature manager codebase to eventually converge again after user grants needed permissions for sending messages later (or whatever your permission flow entails). For web UI clients without direct send capability: simply display notification that message queued locally and let it fall through once connection restored so eventual delivery occurs when network conditions permit. The key deletion removes the pending-request entry immediately upon fulfillment; if delete fails, log error but continue since you don't hold persistent state on client side outside map backing itself (e.g., local DB caches may optionally persist drafts but that's optional and not required for correctness).

### Deletion semantics
- Use `DeleteValue(groupId, storeName, key)` to remove both:
  - Create-request entries after fulfillment or timeout. A create request with associated fulfill function removes its row once delivered; if the delete fails log an error but don't hold state on client outside map backing itself (e.g., local DB caches may optionally persist drafts): you either implement retry logic around this failure, which re-occurs periodically anyway through periodic tick until eventual success after transient connectivity issues.
  - Presence markers when disabling/unsetting feature management for given group/storeName pair: call `clearEnabledMarker` to erase peer's own enabled row from map so "Send from..." picker no longer shows it as available option once disabled; this prevents stale presence entries that could cause send attempts on unmanaged stores even though config says disabled. It ensures UI accurately reflects current permissions because any entry visible in dropdown implies the corresponding enable marker exists, which we just deleted via call to manager method.

### Notification handling for new incoming values
Use a `knownEntries` map keyed by "groupId|storeName" with nested maps of full key -> seen boolean inside each: poll loop first loads all current entries from underlying getMap (which returns RealmMap instance you iterate over), initializes per-store known sub-map on first encounter or reuse existing one for continued tracking. Then in same iteration window mark every entry as already seen and decode only those where `!known[key]` AND not authored by local peer itself: fresh external message that hasn't been processed yet so trigger notification function; after processing incrementally update inner map with true flag to prevent duplicate notifications on next cycle until user acknowledges or timeout passes.

```go
// freshnessWindow avoids notifying on old historical dump (e.g., device restore scenario) where every entry would look new if you didn't filter by age
freshnessThreshold := 10 * time.Minute // anything older than this is ignored for notification firing even though all are added to known set unconditionally during first pass after enabling feature.

for key, entry := range mapEntries {
    peerID, kind, ok := parseKey(key)
    if !ok || parsedKind == reqKind { continue } // skip create-requests handled elsewhere or presence flags not meant for notification loop at all (they're purely structural markers to control send permissions rather than represent user-facing content).

    mapPollMu.Lock()
    alreadyKnown = knownMap[key]
    knownMap[key] = true     // always mark as seen now so subsequent cycles won't re-fire this same row unless it disappears and reappears due to sync churn or manual deletion event from another participant later on.
    mapPollMu.Unlock()

    if alreadySeen { continue }      // skip duplicate notification firing for entries we've handled recently; just record them as processed in known set anyway (which persists across iterations until next cleanup cycle removes really old ones based on auto-delete config or explicit deletion operation done during this same tick window).

       var obj MyMessage
    if err := json.Unmarshal([]byte(entry.Value), &obj); err != nil { continue }   // malformed: skip entirely since invalid data doesn't warrant notifying user anyway.

    ageNow := time.Now()
    msgTimeMillis := obj.TimestampUnixMilli / 1000    // convert to standard unix seconds for subtraction comparison logic below (or use Millisecond-aware duration functions if available instead)
    elapsedDuration = ageNow.Sub(time.Unix(msgTimestampSeconds, int64(obj.MillisecondSinceLastSecond)))

   } } 
```
- Before notifying: check entry's timestamp; `time.Since(timestamp)` exceeds freshness window → skip. This bounds per-device import volume during large historical imports or initial syncs where thousands of rows could arrive at once but most shouldn't trigger OS-level push notification spam if they're clearly stale relative to now clock time on recipient side device (which might be slightly skewed due to battery save mode, airplane switches etc). It keeps UX clean and prevents hundreds of notifications for last synced data from older backups that user doesn't actively want notified about.

- Notification function can call platform bridge's `ShowNotification` if available otherwise delegate via generic notify package or custom desktop notifier integration layer depending on runtime environment (Android vs desktop browser tab with built-in beep library). Include deep link URL in notification payload pointing back to subtab hash segment containing groupId|storeName|phoneNumber for quick reopening of right conversation after clicking system tray or lock screen entry.

### Periodic tick responsibilities
Implement `RunPeriodic(registrar)` hook on engine heartbeat cadence (currently 10 minutes):

```go
func (m *Manager) RunPeriodic(registry realm.Registrars) {
    cfg := m.cfg.Load()
    if !cfg.Enabled || !isValidGroupStore(cfg.GroupID, cfg.StoreName) { return } // guard call only on active configured storespace.

     if localBridgeMissing { return }   // desktop without platform bridge; can still use backend maps calls but no notifications anyway for non-mobile users since we rely solely browser-native feedback rather system tray alerts instead of toast popups or sound effects etc unless opted into via settings dialog where user enables audio cues per their preference profile setup in UI config menu accessed through gear icon at top right corner when logged into dashboard view mode.

    // reconcile state changes: re-read device store vs map entries and delta as needed
    if err := m.reconcileDeviceStore(cfg.GroupID, cfg.StoreName); err != nil {
      log.Printf("periodic feature reconciliation failed for %s/%s: %v", cfg.GroupID, cfg.StoreName, err) // continue running; don't block or fail entire tick silently without logging diagnostic output to stderr logs file if applicable on Linux desktop deployments where journald might pick up errors from daemonized background services managing this process.
    }

     retryPendingRequests(cfg.GroupID, cfg.StoreName);   // e.g., fulfill queued create requests that previously failed permission checks awaiting user approval during dialog flow triggered earlier in current session window just prior to next scheduled tick firing off again after timeout completes its countdown back into memory-safe goroutine waiting for channel signal before scheduling another attempt eventually at exponential interval until max retries reached OR success achieved regardless what order of operations executes first: reconcile state delta always runs before creating new entries so we don't duplicate work or create transient inconsistency between local device store snapshot and map contents as they might diverge briefly during initial import phase when large batches arrive from multiple peers simultaneously uploading same day's worth of history all at once.
     updatePresenceMarkers(cfg.GroupID, cfg.StoreName); // touch enabled row for our own peer ID so periodic auto-deletion sweep doesn't remove it; or delete ours on disable/unregister event whichever config transition happened between last tick and current evaluation moment right now during this very same background worker's execution path inside Go runtime scheduler thread pool that handles non-critical tasks like these scheduled heartbeat callbacks.

     // fulfill any create-requests still sitting in keyspace from requests we queued previously or received externally targeting our own peer as receiver for pending outgoing messages: iterate entries, decode JSON payloads matching against target identifiers equal to localID string returned by calling getPeerIdentifier() wrapper function defined earlier on manager type struct fields initialized during NewManager constructor call at feature bootstrap sequence.
     fulfillPendingCreates(cfg.GroupID, cfg.StoreName);   // these delete their request row upon processing successfully which prevents ever-growing keyspace clutter if user stops using this feature and never sends another message for months; auto-cleanup runs naturally without needing external cron job or manual intervention because periodic tick already sweeps over it every fixed interval anyway.
}
```

Key notes: reconcile first to avoid creating entries that won't match device state (e.g., you try recording in map something the user just deleted locally and then immediately delete it on next cycle anyway, wasting bandwidth). Retry failed actions; don't fail fast silently—log errors so admins can monitor issues. Mark presence markers fresh each tick or remove them if disabled for current config group/store: this prevents auto-delete policies from culling enabled rows that we forgot to refresh due to timing mismatches between disable toggle input and next engine tick arrival window duration exceeding configured retention threshold used by feature owner's cleanup schedule (either global `AutoDeleteEntriesHours` parameter in maps configuration or your own custom cron job written separately for periodic archival runs done off-band of real-time sync operations).

## Helper utilities common across similar features
Extract reusable helpers into shared packages when multiple features reuse same patterns rather than duplicating identical code blocks: a hashing function like `hashValue(data[]byte)` returning 16-char hex prefixes. Parse key segments safely with defensive checks for malformed entries that slip through during sync; return bool ok flags so caller can skip processing unknown rows instead of panicking or silently corrupting internal state without proper validation guardrails preventing bad data from propagating further down system call stack until it triggers explicit error handling logic defined within feature-specific recover path handlers awaiting re-registration after failure resolved via retry mechanism built into periodic scheduler component responsible for monitoring live peer connections while maintaining liveness signals across unstable network conditions caused by intermittent internet outages or mobile carrier throttle policies limiting background traffic volumes below acceptable thresholds needed to keep replication loops running smoothly end-to-end without interruption from failed RPC calls hitting rate limit imposed on public DNS records resolving via unencrypted HTTP fallback protocol used during initial setup wizard walk-through before upgrading connection security posture once peer identity certificates exchanged successfully over TLS handshake confirmed valid signing keys belong either side of communication channel being established between two authorized participants in same group membership ring managed by core discovery service running on primary seed node currently hosting active gossip subgraph containing list of all known peers registered within current network topology snapshot captured during latest cluster-wide health check cycle just completed before scheduling next round of peer visibility refresh operations initiated across distributed system spanning multiple geographic cloud region boundaries.

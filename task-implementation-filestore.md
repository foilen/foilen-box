# Task: FileStore

## Design summary

Add a new **core** Realm feature `filestore` (`realm/features/filestore`, `common/filestore`), following the
step-by-step in `docs/features.md` (not the encrypted-realmmap pattern doc, since this feature owns its own
wire protocol for fetching file bytes — unlike SMS, which rides entirely on `common/maps`).

- Shared metadata (which files exist, which peers have them) is stored in a `common/maps` RealmMap, one per
  file store, named by the caller (e.g. `SMS-<suffix>-fs`). A reserved `_fileStores` map (system store, like
  `_realmMaps`) holds one `RealmMapConfig`-shaped entry per file store name, keyed by that name, carrying only
  `Encryption` for now.
- Local (per-device, not synced) config — store type (`metadata`/`full`), local path, transient-file handling —
  is persisted by the `filestore` feature itself, keyed by `groupId+storeName`, analogous to how
  `realm/features/maps.NewStore` persists its own state (not via a `internal/<feature>/config.go`-style
  per-app JSON file like `sms.Config`).
- A new request/response libp2p protocol (`GetFileProtocolID`) fetches raw file bytes from a peer that has
  them; for an encrypted store, the request is signed with the target identity's key over
  `{storeName, sha256, "<localPeerId>:<remotePeerId>"}`, verified the same way `maps` verifies
  `IdentitySignature` (see `realm/features/maps/crypto.go`).
- Two local store types, both in scope this pass:
    - `full` — the peer holds the actual file bytes at `localPath/<sha256>`. Exercised by `internal/sms` (MMS
      binaries).
    - `metadata` — the peer records only `{sha256, fileName, size}` plus the **absolute path** where the file
      already lives on that peer; the file is never copied. On a `GetFile` served from a `metadata` store, bytes
      are streamed from that recorded absolute path. Exercised only through the `RealmFileStores` UI this pass
      (no non-UI consumer).
- First non-UI consumer: `internal/sms` creates one `full`, encrypted-to-the-SMS-identity file store per SMS
  store, uploads MMS attachment bytes into it on import, and references them by sha256 from `SmsMessage`
  instead of the current "(media)" placeholder text.
- `RealmFileStores` web UI tab (see UI section) is in scope: list / create / delete stores, browse a store's
  files and holding peers, add a file, remove a file.

## Files — core `filestore` feature

- `realm/features/filestore/feature.go` (new) — `Feature` struct implementing `realm.Feature` +
  `realm.PeriodicHook`; constructor takes `*maps.Feature` (to read/write the backing RealmMap and reserved
  `_fileStores` store) and its own `*Store`. Registers `GetFileProtocolID` stream handler. `RunPeriodic` runs
  the per-file orphan sweep on a random 10–20 minute cadence per process, mirroring `maps.Feature`'s
  `sweepMinute` pattern. Orphan sweep semantics (confirmed): on each tick, delete a file's top-level
  `sha256:<file>` metadata entry from the RealmMap when **no** peer currently has a `sha256:<file>/<peerId>`
  sub-entry — i.e. garbage-collect files no peer holds anymore. Same tick, for every `metadata`-type store
  this peer holds, reconcile each local `sha256 → absolutePath` row against the RealmMap:
    - `absolutePath` no longer resolves but this peer still has a `sha256:<file>/<peerId>` entry → remove
      **only** that shared entry (the local row stays, so the user can fix an unmounted drive / unplugged USB
      stick).
    - `absolutePath` resolves again but this peer has no `sha256:<file>/<peerId>` entry → automatically
      re-publish it (and re-create the top-level `sha256:<file>` metadata if the orphan sweep already removed
      it). Recovery needs no re-add from the UI.
- `_fileStores` reconcile — mirroring `maps.Feature`'s `reconcileDesiredStores` (which locally `DeleteMap`s a
  store once its `_realmMaps` entry disappears): when a `_fileStores` entry vanishes (store deleted from
  another peer), this peer drops its own `LocalConfig` and, for a `full` store, deletes the on-disk files
  under `localPath/` **iff** that `LocalConfig.DeleteFilesOnStoreDelete` is set. This is the flag's primary
  purpose — deciding what happens to local bytes when the store is deleted from outside.
- `realm/features/filestore/store.go` (new) — local persisted state: per `groupId+storeName` `LocalConfig`
  (`Type`, `LocalPath`, `DeleteFilesOnStoreDelete bool` (`full` only, default `false`),
  `Transient{Mode, Path, MaxSizeBytes, MaxAgeSeconds}`); `full`-type file I/O
  (`localPath/<sha256>`); `metadata`-type `sha256 → absolutePath` table. Modeled on
  `realm/features/maps/store.go`'s load/persist shape. The `metadata` table keeps the recorded
  `absolutePath` even after it stops resolving (unplugged drive, moved file); only the shared `/peerId` entry
  is dropped, and the periodic sweep re-publishes it automatically once the path resolves again (see `GetFile`
  / orphan sweep above).
- `realm/features/filestore/keys.go` (new) — key helpers for the shared RealmMap: `fileKey(sha256)`,
  `peerKey(sha256, peerID)`, `parseKey`, mirroring `internal/sms/model.go`'s `messageKey`/`parseKey` style.
- `realm/features/filestore/protocol.go` (new) — `GetFileProtocolID` request/response types and the
  identity-signature build/verify helpers for the encrypted case (reuses `keypair.PrivateKey`; calls the
  `identityPubKeyFromID` helper now moved to `realm/keypair` — see shared-crypto section).
- `realm/features/filestore/feature.go` public API:
    - `CreateStore(groupID, storeName string, local LocalConfig, encryptToIdentityID string) error`
      (`encryptToIdentityID == ""` → unencrypted store).
    - `EditStore(groupID, storeName string, deleteFilesOnStoreDelete bool) error` — `full` stores only;
      updates this peer's `LocalConfig.DeleteFilesOnStoreDelete` (consulted by the `_fileStores` reconcile when
      the store is deleted from outside). No other `LocalConfig` field is editable post-create.
    - `PutFile(groupID, storeName string, data []byte, fileName string) (sha256 string, err error)` — `full`
      stores only.
    - `PutFileRef(groupID, storeName, absolutePath string) (sha256 string, err error)` — `metadata` stores
      only; hashes the file at `absolutePath`, records `{sha256, fileName, size, absolutePath}` locally and the
      `{sha256, fileName, size}` metadata + this peer's `/peerId` entry in the RealmMap.
    - `GetFile(groupID, storeName, sha256 string) (data []byte, fileName string, err error)` — local-first
      (`full`: read `localPath/<sha256>`; `metadata`: read the recorded absolute path), else a random connected
      peer that has it, else a random known peer that has it. For a `metadata` store whose recorded
      `absolutePath` no longer resolves: remove this peer's `sha256:<file>/<peerId>` shared entry (keep the
      local row), then fall through to fetching from another holding peer, failing only if none has it.
    - `ListStores(groupID string) []StoreInfo` / `ListFiles(groupID, storeName string) []FileInfo` (name, type,
      local path, encryption identity ID; per file: sha256, fileName, size, holding peer IDs, and for a local
      `metadata` row whether its `absolutePath` currently resolves) — for the UI.
    - `RemoveFile(groupID, storeName, sha256 string) error` — removes only this peer's `sha256:<file>/<peerId>`
      entry from the RealmMap; for a `full` store also deletes `localPath/<sha256>`; for a `metadata` store
      drops the local `sha256 → absolutePath` entry (never touches the file on disk). The file's top-level
      metadata entry is left for the orphan sweep to collect once no peer holds it.
    - `DeleteStore(groupID, storeName string, deleteLocalFiles *bool) error` — like `maps.DeleteMap`: removes
      the reserved `_fileStores` entry, the backing RealmMap, and this peer's `LocalConfig`. For a `full` store
      it also deletes the on-disk files under `localPath/`: when `deleteLocalFiles != nil` (UI checkbox) that
      value wins, otherwise it falls back to `LocalConfig.DeleteFilesOnStoreDelete` (default `false` → files
      left on disk). `metadata` stores never touch files on disk. The propagated external-deletion path (the
      `_fileStores` reconcile above) always uses the stored `DeleteFilesOnStoreDelete`.
- `realm/features/filestore/feature_test.go` (new) — unit tests for key parsing, local `full`-store put/get,
  local `metadata`-store ref put/get, `metadata` stale-path handling (shared `/peerId` entry dropped, local
  row kept) and recovery (path resolves again → `/peerId` re-published by the sweep), `EditStore` updating
  the flag, `DeleteStore` with `deleteLocalFiles` nil/true/false vs. the stored `DeleteFilesOnStoreDelete`,
  and signature verification. Created but not run, per `AGENTS.md`.

## Files to touch — shared crypto

- `realm/features/maps/crypto.go` — extract `identityPubKeyFromID` (and `ed25519PubToX25519`) into
  `realm/keypair` and have `maps` import the moved copy, so `filestore` reuses one implementation.
- `realm/keypair/*.go` (update) — add the moved helper(s), exported.

## Files — `internal/sms` integration

- `internal/sms/model.go` — add `Attachment{Sha256, FileName, MimeType string; SizeBytes int64}` and an
  `Attachments []Attachment` field on `SmsMessage`; add `FileStoreNameFor(smsStoreName string) string`
  (`smsStoreName + "-fs"`).
- `internal/sms/bridge.go` — extend `PlatformBridge`: `ReadAllSms`'s JSON gains a per-message `attachments`
  array of `{partId, contentType, fileName, size}` (metadata only, no bytes); add
  `ReadMmsAttachment(partID string) ([]byte, error)` returning just the raw bytes (mime/name already carried
  in the `attachments` metadata). Update `ReadAllSms`'s doc comment (attachments are now imported, not
  dropped).
- `internal/sms/manager.go` — constructor gains a `*filestore.Feature` param; on enable (wherever the SMS
  store is first `CreateMap`'d) also `filestore.CreateStore` the `-fs` store as `full`, encrypted to the same
  identity as the SMS store (read via `mapsFeature.EncryptionIdentityID(groupID, storeName)`), with
  `Transient{Mode: cache, Path: <same LocalPath>, MaxSizeBytes: 0, MaxAgeSeconds: 0}` (keep forever).
  `reconcileDeviceStore` calls `bridge.ReadMmsAttachment(partId)` for each new message's attachment, uploads
  the bytes via `filestore.PutFile` into `FileStoreNameFor(storeName)`, and stores the resulting
  `Attachment` metadata on the `SmsMessage` (only the sha256 + metadata cross the map, never the bytes).
- `internal/sms/config.go` — no change (the file store's local config lives in `filestore`'s own store).

## Files — Android bridge (MMS attachment bytes)

- `android/app/src/main/java/com/foilen/box/android/MainActivity.kt` — extend `mmsBody`'s part-scanning loop
  to also collect each non-text part's `(partId, contentType, fileName, size)` into the `SmsMessage` JSON as
  an `attachments` array (parallel to the existing `readMmsPartText`); add `readMmsAttachment(partId: String):
  ByteArray` reading `content://mms/part/<partId>` fully, implementing `SmsBridge.ReadMmsAttachment`.
- `cmd/mobile/mobile.go` — extend the `SmsBridge` interface with `ReadMmsAttachment(partID string) ([]byte,
  error)` (gomobile marshals `[]byte` ↔ Kotlin `ByteArray` directly; this is a one-shot per-attachment call
  on import, not a per-frame data plane, so the camera-bridge caveat doesn't apply) and thread it through to
  `boxsms.Manager` at construction.

## Files — webserver wiring

- `internal/webserver/api.go` — construct `realmfilestore.NewStore(realmConfigSvc.Dir())` and
  `realmfilestore.New(mapsFeature, filestoreStore)`; `realmEng.Register(filestoreFeature)`; add
  `realmFileStore *realmfilestore.Feature` field to `api`; pass it into `boxsms.NewManager(...)`.
- `internal/webserver/api_filestore.go` (new) — WebSocket actions for the `RealmFileStores` tab, scoped per
  group like the maps actions: `filestore.list`, `filestore.create` (name, type, local path, encryption
  identity ID, and `deleteFilesOnStoreDelete` for `full` stores), `filestore.edit` (`full` stores only:
  new `deleteFilesOnStoreDelete` → `EditStore`), `filestore.delete` (optional `deleteLocalFiles` bool
  override → `DeleteStore(..., *bool)`), `filestore.files` (list files + holding peers; per file a
  `pathResolves` flag for `metadata` stores), `filestore.addFile` (`full`: upload base64 bytes + fileName →
  `PutFile`; `metadata`: absolute path → `PutFileRef`), `filestore.removeFile`, `filestore.getFile` (base64
  bytes in the JSON response, for download / image preview).
- `internal/webserver/api_sms.go` — add a WebSocket action to fetch one attachment's bytes for
  display/download (base64 in the JSON response) via `a.realmFileStore.GetFile(...)`.
- `internal/webserver/web/js/realm-filestore.js` (new) — the `RealmFileStores` tab (see UI section).
- `internal/webserver/web/js/realm-sms.js` — render attachments in a conversation (thumbnail for images,
  fileName + download link otherwise), calling the new `api_sms.go` action.
- `internal/webserver/web/js/realm.js` + the tab bar HTML — register the `RealmFileStores` tab alongside the
  existing `RealmMaps` / `RealmServices` / `RealmSMS` tabs.

## UI — `RealmFileStores` tab

Per-group, mirroring the `RealmMaps` tab's structure.

- **Store list**: each file store shows name, type (`full`/`metadata`), local path, encryption identity ID
  (blank = unencrypted).
- **Create store**: form with name, type, local path, encryption identity ID (optional → unencrypted). For a
  `full` store the local path is where bytes are stored, plus a "delete local files when the store is
  deleted" checkbox (default off); for a `metadata` store the local path and the checkbox are unused (files
  stay at their own absolute paths and are never touched).
- **Edit store**: `full` stores only — toggle the "delete local files when the store is deleted" flag
  (`filestore.edit` → `EditStore`). This is the value applied when the store is deleted from another peer.
  Name / type / local path / encryption are not editable (matching there being no edit UI for maps).
- **Delete store**: with confirmation. For a `full` store the dialog shows a "also delete local files on
  this device" checkbox, pre-checked to the store's stored `deleteFilesOnStoreDelete` value; the user's
  choice is sent as the `deleteLocalFiles` override. `metadata` stores show no checkbox.
- **Browse store**: list of files (fileName, sha256, size, peer IDs that have it). For a `metadata` store,
  a file whose local `absolutePath` no longer resolves is flagged (e.g. "path unavailable") so the user can
  reconnect the drive and re-add it; this peer's shared `/peerId` entry has already been dropped.
- **Add file to store**:
    - `full` store → pick/upload a file; bytes are copied into the store.
    - `metadata` store → enter an absolute path to a file already present on this peer; only
      `{sha256, fileName, size}` is recorded, the file is not copied.
- **Remove file from store**: with confirmation. Removes only this peer's `/peerId` entry (the file itself
  stays in the store as long as another peer has it; once nobody does, the orphan sweep deletes the top-level
  entry). For a `full` store the local `localPath/<sha256>` file is also deleted; for a `metadata` store the
  local metadata entry is deleted but the on-disk file is left untouched.

## Auth — `GetFileProtocolID`

- **Unencrypted store**: requester must be a confirmed member of the group — same check
  `maps.handleSubscribeStream` uses (like maps replication, services forwarding, etc.). No separate
  `PermissionAction`.
- **Encrypted store**: the group-membership check **plus** a valid identity signature over
  `{storeName, sha256, "<localPeerId>:<remotePeerId>"}`, verified like `maps`'s `IdentitySignature`
  (`realm/features/maps/crypto.go`).

## Files — docs

- `docs/features.md` — add `filestore` (`common/filestore`) to the **Core** features list, one line, matching
  the existing `maps` / `scripts` / `services` entries' style.

## Questions

None outstanding — both prior questions are resolved and folded into the sections above:
- `DeleteFilesOnStoreDelete` gets a dedicated **Edit store** UI (its purpose is external deletion), and the
  delete-store dialog also offers a one-off `deleteLocalFiles` override checkbox defaulting to the stored
  value.
- A recovered `metadata` path is re-published automatically by the periodic sweep; no UI re-add needed.
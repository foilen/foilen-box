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
- Only the `full` local store type is exercised in this pass (MMS binaries); `metadata`-type stores are part of
  the generic design but not implemented/tested here — see Questions.
- First (only) consumer: `internal/sms` creates one `full`, encrypted-to-the-SMS-identity file store per SMS
  store, uploads MMS attachment bytes into it on import, and references them by sha256 from `SmsMessage`
  instead of the current "(media)" placeholder text.

## Files — core `filestore` feature

- `realm/features/filestore/feature.go` (new) — `Feature` struct implementing `realm.Feature` +
  `realm.PeriodicHook`; constructor takes `*maps.Feature` (to read/write the backing RealmMap and reserved
  `_fileStores` store) and its own `*Store`. Registers `GetFileProtocolID` stream handler. `RunPeriodic` runs
  the per-file orphan sweep (see Questions) on a random 10–20 minute cadence per process, mirroring
  `maps.Feature`'s `sweepMinute` pattern.
- `realm/features/filestore/store.go` (new) — local persisted state: per `groupId+storeName` `LocalConfig`
  (`Type`, `LocalPath`, `Transient{Mode, Path, MaxSizeBytes, MaxAgeSeconds}`); `full`-type file I/O
  (`localPath/<sha256>`); `metadata`-type key→path table. Modeled on `realm/features/maps/store.go`'s
  load/persist shape.
- `realm/features/filestore/keys.go` (new) — key helpers for the shared RealmMap: `fileKey(sha256)`,
  `peerKey(sha256, peerID)`, `parseKey`, mirroring `internal/sms/model.go`'s `messageKey`/`parseKey` style.
- `realm/features/filestore/protocol.go` (new) — `GetFileProtocolID` request/response types and the
  identity-signature build/verify helpers for the encrypted case (reuses `keypair.PrivateKey`; needs
  `identityPubKeyFromID`, currently private to `maps/crypto.go` — see Files to touch below).
- `realm/features/filestore/feature.go` public API: `CreateStore(groupID, storeName string, local LocalConfig,
  encryptToIdentityID string) error`; `PutFile(groupID, storeName string, data []byte, fileName string)
  (sha256 string, err error)`; `GetFile(groupID, storeName, sha256 string) (data []byte, fileName string, err
  error)` (local-first, else a random connected peer that has it, else a random known peer that has it — per
  spec); `DeleteStore(...)` for symmetry with `maps.Feature.DeleteMap`.
- `realm/features/filestore/feature_test.go` (new) — unit tests for key parsing, local full-store put/get, and
  signature verification. Created but not run, per `AGENTS.md`.

## Files to touch — shared crypto

- `realm/features/maps/crypto.go` — no change if we duplicate the ~10-line `identityPubKeyFromID` helper in
  `filestore/protocol.go`; **or** extract it (and `ed25519PubToX25519`) into `realm/keypair` so both features
  import one copy. Recommend the extraction; flagged as a question below since it touches an existing package.
- `realm/keypair/*.go` (update, if extraction chosen) — add the moved helper(s).

## Files — `internal/sms` integration

- `internal/sms/model.go` — add `Attachment{Sha256, FileName, MimeType string; SizeBytes int64}` and an
  `Attachments []Attachment` field on `SmsMessage`; add `FileStoreNameFor(smsStoreName string) string`
  (`smsStoreName + "-fs"`).
- `internal/sms/bridge.go` — extend `PlatformBridge` with `ReadMmsAttachment(partID string) (data []byte,
  mimeType string, fileName string, err error)`; update `ReadAllSms`'s doc comment (attachments are now
  fetchable, not dropped).
- `internal/sms/manager.go` — constructor gains a `*filestore.Feature` param; `reconcileDeviceStore` uploads
  each new message's attachments via `filestore.PutFile` into `FileStoreNameFor(storeName)` and stores the
  resulting `Attachment` metadata instead of relying on bridge-side placeholder text; on enable (wherever the
  SMS store is first created/`CreateMap`'d), also `filestore.CreateStore` the `-fs` store as `full`, encrypted
  to the same identity as the SMS store (read via `mapsFeature.EncryptionIdentityID(groupID, storeName)`),
  with `Transient{Mode: cache, Path: <same LocalPath>, MaxSizeBytes: 0, MaxAgeSeconds: 0}` (keep forever, per
  spec).
- `internal/sms/config.go` — no change expected (local SMS config stays as-is; the file store's own local
  config lives in `filestore`'s own store, not here).

## Files — Android bridge (MMS attachment bytes)

- `android/app/src/main/java/com/foilen/box/android/MainActivity.kt` — extend `mmsBody`'s part-scanning loop to
  also collect each non-text part's `(partId, contentType, filename)` into the returned `SmsMessage` JSON as an
  `attachments` array (parallel to the existing `readMmsPartText`); add `readMmsAttachment(partId: String):
  ByteArray` (or a base64-returning variant, matching however `gomobile bind`'s Go↔Kotlin bridge marshals
  binary — see Questions) reading `content://mms/part/<partId>` fully, implementing the new
  `SmsBridge.ReadMmsAttachment` method.
- `cmd/mobile/mobile.go` — extend the `SmsBridge` interface with `ReadMmsAttachment(partID string) (data
  []byte, mimeType string, fileName string, err error)` (or JSON-string-wrapped, matching `ReadAllSms`'s
  existing convention) and thread it through to `boxsms.Manager` at construction.

## Files — webserver wiring

- `internal/webserver/api.go` — construct `realmfilestore.NewStore(realmConfigSvc.Dir())` and
  `realmfilestore.New(mapsFeature, filestoreStore)`; `realmEng.Register(filestoreFeature)`; add
  `realmFileStore *realmfilestore.Feature` field to `api`; pass it into `boxsms.NewManager(...)`.
- `internal/webserver/api_sms.go` — add a WebSocket action to fetch one attachment's bytes for display/download
  (base64 in the JSON response, same convention likely used elsewhere for binary — confirm against an existing
  binary-returning action if one exists) via `a.realmFileStore.GetFile(...)`.
- `internal/webserver/web/js/realm-sms.js` — render attachments in a conversation (thumbnail for images,
  filename+download link otherwise), calling the new action. **Scope question** — see below.

## Files — docs

- `docs/features.md` — add `filestore` (`common/filestore`) to the Core features list, one line, matching the
  existing `maps`/`scripts`/`services` entries' style.

## Questions

1. **UI scope**: should this pass include the web UI attachment viewer/download
   (`internal/webserver/web/js/realm-sms.js` + a new `api_sms.go` action), or is displaying "(media)" as
   plain text still fine for now, with the viewer left for a follow-up? This changes whether those two files
   are touched at all.

Yes add a `RealmFileStores` tab that:
- lists all the file stores (name, type, local path, encryption identity ID)
- allows creating a new file store (name, type, local path, encryption identity ID)
- allows deleting a file store (with confirmation)
- allows viewing the contents of a file store (list of files, size, peers that have it)
- allows deleting a file from a file store (with confirmation)
  - that removes only the `/peerId` entry for the current peer, not the file itself (unless no peers have it anymore, in which case the orphan sweep will delete it)
  - if that is a `full` store, the local file is also deleted
  - if that is a `metadata` store, the local metadata entry is deleted, but the file itself is not deleted (since it may be stored elsewhere)

2. **Local store path**: where should the `full` store's `LocalPath` live — a fixed subdirectory under the
   existing config dir (e.g. `<configDir>/filestore/<storeName>/`), or something env-var-overridable like
   `FOILEN_BOX_CONFIG_DIR`? Auto-derived from `groupId+storeName`, or does the caller (`sms` manager) pick it
   explicitly?







3. **Orphan sweep semantics**: I read "delete all the entries `sha256:{file}` for which the peer has no
   `/peerId` entry" as: on each tick, delete a file's top-level metadata entry when *no* peer (not just "this
   peer") currently has a `/peerId` sub-entry for it — i.e. garbage-collect files nobody holds anymore. Is
   that the intended behavior, or something else (e.g. each peer purging its *own* local copy for files it no
   longer wants to keep)?
4. **Auth for unencrypted stores' `GetFile` request**: the spec only defines a signed token for the encrypted
   case. For an unencrypted store, is "requester is a confirmed member of the group" (same check
   `maps.handleSubscribeStream` uses) sufficient, or should `filestore` declare its own
   `PermissionAction`/`Actions()` entry so it's gate-able independently like `speedtest.ActionRun`?
5. **Android↔Go byte transfer**: `gomobile bind`'s bridge — does it marshal `[]byte` directly, or does the
   existing bridge convention (`ReadAllSms` returns a JSON string) mean attachment bytes should also be
   base64-wrapped in a JSON/string return rather than a raw `[]byte` return? Confirms the exact signature for
   `ReadMmsAttachment`/`SmsBridge.ReadMmsAttachment`.
6. **`metadata`-type store**: confirmed out of scope for this pass (no test/consumer) — implement the type in
   `LocalConfig`/`store.go` for shape-completeness, or omit entirely and add it when a real consumer shows up?
7. **Extracting `identityPubKeyFromID`**: OK to move it (and its two ed25519↔X25519 helpers) out of
   `realm/features/maps/crypto.go` into `realm/keypair` so `filestore` can reuse it, or prefer a small
   duplicated copy in `filestore/protocol.go` to keep `maps` untouched?

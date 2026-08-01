package webserver

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	earlyaggregate "foilen-box/internal/early/aggregate"
	earlyclient "foilen-box/internal/early/client"
	earlyconfig "foilen-box/internal/early/config"
	appspec "foilen-box/internal/spec"
	boxspeedtest "foilen-box/internal/speedtest"

	realm "foilen-realm"
	realmconfig "foilen-realm/config"
	realmidentity "foilen-realm/features/identity"
	realmmaps "foilen-realm/features/maps"
	realmscripts "foilen-realm/features/scripts"
	realmservices "foilen-realm/features/services"
	realmmodel "foilen-realm/model"
	realmpeers "foilen-realm/peers"
)

// request is a single WebSocket call from the UI.
type request struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
}

// response is the reply to a request, matched by ID on the client side.
type response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// api holds the backend services the dispatcher calls into.
type api struct {
	configDir          string
	logDir             string
	hostnameOverride   string
	earlyConfig        *earlyconfig.Service
	earlyAggregate     *earlyaggregate.Service
	realmConfig        *realmconfig.Service
	realmPeers         *realmpeers.Store
	realmEngine        *realm.Engine
	realmScripts       *realmscripts.Feature
	realmServices      *realmservices.Feature
	realmServicesStore *realmservices.Store
	realmMapsFeature   *realmmaps.Feature
	realmSpeedTest     *boxspeedtest.Feature
	realmIdentity      *realmidentity.Feature
	realmStateSink     RealmStateSink
}

func newAPI(configDir string, defaultDhtMode string, hostnameOverride string) (*api, error) {
	var (
		configService  *earlyconfig.Service
		realmConfigSvc *realmconfig.Service
		err            error
	)
	if configDir == "" {
		configService, err = earlyconfig.New()
		if err == nil {
			realmConfigSvc, err = realmconfig.New(defaultDhtMode)
		}
	} else {
		configService, err = earlyconfig.NewInDir(configDir)
		if err == nil {
			realmConfigSvc, err = realmconfig.NewInDir(configDir, defaultDhtMode)
		}
	}
	if err != nil {
		return nil, err
	}

	realmPeerStore, err := realmpeers.New(realmConfigSvc.Dir())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize realm peer store: %w", err)
	}
	realmMapsStore, err := realmmaps.NewStore(realmConfigSvc.Dir())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize realm maps store: %w", err)
	}
	realmServicesStore, err := realmservices.NewStore(realmConfigSvc.Dir())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize realm services store: %w", err)
	}

	realmEng := realm.New(realmConfigSvc.Dir(), realmPeerStore)
	realmEng.SetHostnameOverride(hostnameOverride)
	realmEng.SetAppVersion(appVersion())
	dataDir := realmConfigSvc.Dir()
	scriptsFeature := realmscripts.New()
	servicesFeature := realmservices.New(realmServicesStore)
	mapsFeature := realmmaps.New(realmMapsStore)
	announceFeature := newRealmAnnounce(mapsFeature, func() string { return appspec.Report(dataDir) }, func() appspec.Summary { return appspec.GetSummary(dataDir) }, func() string { return resolveHostname(hostnameOverride) })
	speedTestFeature := boxspeedtest.New()
	// a is assigned below, but the identity feature's onReceive callback
	// can only ever fire later (once a peer actually pushes an identity to
	// us), long after a is fully constructed, so it's safe for the closure
	// to capture this forward-declared pointer.
	var a *api
	identityFeature := realmidentity.New(func(name string, kp realmmodel.KeyPair) error {
		return a.importPushedIdentity(name, kp)
	})
	realmEng.Register(scriptsFeature)
	realmEng.Register(servicesFeature)
	realmEng.Register(mapsFeature)
	realmEng.Register(announceFeature)
	realmEng.Register(speedTestFeature)
	realmEng.Register(identityFeature)

	a = &api{
		configDir:          configDir,
		hostnameOverride:   hostnameOverride,
		earlyConfig:        configService,
		earlyAggregate:     earlyaggregate.New(earlyclient.New(), configService),
		realmConfig:        realmConfigSvc,
		realmPeers:         realmPeerStore,
		realmEngine:        realmEng,
		realmScripts:       scriptsFeature,
		realmServices:      servicesFeature,
		realmServicesStore: realmServicesStore,
		realmMapsFeature:   mapsFeature,
		realmSpeedTest:     speedTestFeature,
		realmIdentity:      identityFeature,
	}

	// Auto-start the realm engine once a peer id already exists
	// (decision 5); a failure here shouldn't prevent the web UI itself
	// from starting.
	if cfg := realmConfigSvc.Load(); cfg.PeerID.ID != "" {
		cfg = ensureRealmListenPort(realmConfigSvc, cfg)
		if err := realmEng.Start(cfg); err != nil {
			log.Printf("realm: failed to auto-start engine: %v", err)
		} else {
			servicesFeature.RestoreAll()
		}
	}

	return a, nil
}

// shutdown stops the realm engine and flushes any pending peer-store writes.
func (a *api) shutdown() {
	a.realmEngine.Stop()
	a.realmServices.StopAll()
	if err := a.realmPeers.Flush(); err != nil {
		log.Printf("realm: failed to flush peer store: %v", err)
	}
	if err := a.realmServicesStore.Flush(); err != nil {
		log.Printf("realm: failed to flush services store: %v", err)
	}
}

// updateRealmConfig loads the current Realm config, applies fn, persists
// it, and restarts (or stops) the engine to reflect the change.
func (a *api) updateRealmConfig(fn func(cfg *realmmodel.Config)) (realmmodel.Config, error) {
	cfg := a.realmConfig.Load()
	fn(&cfg)
	if cfg.PeerID.ID != "" {
		cfg = ensureRealmListenPort(a.realmConfig, cfg)
	}
	if err := a.realmConfig.Save(cfg); err != nil {
		return realmmodel.Config{}, err
	}
	if err := a.realmEngine.Reconcile(cfg); err != nil {
		log.Printf("realm: failed to apply engine config: %v", err)
	}
	return cfg, nil
}

// importPushedIdentity is the identity feature's onReceive callback: it's
// invoked whenever another peer pushes an identity to us and permission
// allows it, and imports it automatically (no user confirmation), renaming
// on a name collision rather than rejecting the push outright.
func (a *api) importPushedIdentity(name string, kp realmmodel.KeyPair) error {
	unique := name
	for i := 2; a.identityExists(unique); i++ {
		unique = fmt.Sprintf("%s (%d)", name, i)
	}
	_, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Identities = append(c.Identities, realmmodel.Identity{Name: unique, KeyPair: kp})
	})
	return err
}

// resolveHostname returns override if set, falling back to the OS-reported
// hostname otherwise.
func resolveHostname(override string) string {
	if override != "" {
		return override
	}
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("realm: failed to read hostname: %v", err)
	}
	return hostname
}

// ensureRealmListenPort backfills cfg.RealmListenPort with a freshly picked free
// port and saves it, if it isn't set yet (a config created or last saved
// before this field existed). Once assigned, a peer keeps the same listen
// port forever, which is what keeps its advertised addresses stable across
// restarts. Returns cfg unchanged if a port was already assigned or a free
// one couldn't be picked (falls back to libp2p's random-port default).
func ensureRealmListenPort(svc *realmconfig.Service, cfg realmmodel.Config) realmmodel.Config {
	if cfg.RealmListenPort != 0 {
		return cfg
	}
	port, err := realm.PickFreeListenPort()
	if err != nil {
		log.Printf("realm: failed to assign a stable listen port, falling back to random: %v", err)
		return cfg
	}
	cfg.RealmListenPort = port
	if err := svc.Save(cfg); err != nil {
		log.Printf("realm: failed to persist assigned listen port: %v", err)
	}
	return cfg
}

// handlerFunc handles one action: unmarshal params (if any), validate, call
// a service, and shape a response.
type handlerFunc func(a *api, params json.RawMessage) (any, error)

// handlers maps each action name to the function that handles it. Handlers
// are defined in api_realm.go, api_early.go, and api_misc.go, grouped by
// domain rather than all inlined here.
var handlers = map[string]handlerFunc{
	"spec.report":         handleSpecReport,
	"troubleshooting.run": handleTroubleshootingRun,
	"logs.read":           handleLogsRead,
	"logs.clear":          handleLogsClear,

	"early.loadConfig": handleEarlyLoadConfig,
	"early.saveConfig": handleEarlySaveConfig,
	"early.aggregate":  handleEarlyAggregate,
	"early.delete":     handleEarlyDelete,

	"realm.loadConfig":            handleRealmLoadConfig,
	"realm.generatePeerId":        handleRealmGeneratePeerID,
	"realm.addGroup":              handleRealmAddGroup,
	"realm.importGroup":           handleRealmImportGroup,
	"realm.deleteGroup":           handleRealmDeleteGroup,
	"realm.addPermission":         handleRealmAddPermission,
	"realm.deletePermission":      handleRealmDeletePermission,
	"realm.exportGroup":           handleRealmExportGroup,
	"realm.addIdentity":           handleRealmAddIdentity,
	"realm.importIdentity":        handleRealmImportIdentity,
	"realm.deleteIdentity":        handleRealmDeleteIdentity,
	"realm.exportIdentity":        handleRealmExportIdentity,
	"realm.pushIdentity":          handleRealmPushIdentity,
	"realm.setDescription":        handleRealmSetDescription,
	"realm.setEnabled":            handleRealmSetEnabled,
	"realm.setDhtMode":            handleRealmSetDhtMode,
	"realm.setDiscoveryOptions":   handleRealmSetDiscoveryOptions,
	"realm.setEnableRelayService": handleRealmSetEnableRelayService,
	"realm.setPeerRetentionDays":  handleRealmSetPeerRetentionDays,
	"realm.setExposeWeb":          handleRealmSetExposeWeb,
	"realm.listPeers":             handleRealmListPeers,
	"realm.listSwarmPeers":        handleRealmListSwarmPeers,
	"realm.addScript":             handleRealmAddScript,
	"realm.updateScript":          handleRealmUpdateScript,
	"realm.deleteScript":          handleRealmDeleteScript,
	"realm.runPeerScript":         handleRealmRunPeerScript,
	"realm.listScriptRuns":        handleRealmListScriptRuns,

	"realm.addService":        handleRealmAddService,
	"realm.updateService":     handleRealmUpdateService,
	"realm.deleteService":     handleRealmDeleteService,
	"realm.scanLocalPorts":    handleRealmScanLocalPorts,
	"realm.startServiceProxy": handleRealmStartServiceProxy,
	"realm.stopServiceProxy":  handleRealmStopServiceProxy,
	"realm.listActiveProxies": handleRealmListActiveProxies,
	"realm.connectService":    handleRealmConnectService,

	"realm.listMaps":       handleRealmListMaps,
	"realm.getMap":         handleRealmGetMap,
	"realm.createMap":      handleRealmCreateMap,
	"realm.setMapValue":    handleRealmSetMapValue,
	"realm.deleteMapValue": handleRealmDeleteMapValue,
	"realm.deleteMap":      handleRealmDeleteMap,

	"realm.runSpeedTest": handleRealmRunSpeedTest,
}

func (a *api) dispatch(req request) response {
	result, err := a.call(req.Action, req.Params)
	if err != nil {
		return response{ID: req.ID, Error: err.Error()}
	}
	return response{ID: req.ID, Result: result}
}

func (a *api) call(action string, params json.RawMessage) (any, error) {
	h, ok := handlers[action]
	if !ok {
		return nil, fmt.Errorf("unknown action: %s", action)
	}
	return h(a, params)
}

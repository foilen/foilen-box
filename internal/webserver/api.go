package webserver

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	earlyaggregate "foilen-box/internal/early/aggregate"
	earlyclient "foilen-box/internal/early/client"
	earlyconfig "foilen-box/internal/early/config"
	boxsms "foilen-box/internal/sms"
	appspec "foilen-box/internal/spec"
	boxspeedtest "foilen-box/internal/speedtest"

	realm "foilen-realm"
	realmconfig "foilen-realm/config"
	realmannounce "foilen-realm/features/announce"
	realmgroup "foilen-realm/features/group"
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
	uiConfig           *uiConfigService
	currentPort        int
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
	realmGroup         *realmgroup.Feature
	realmStateSink     RealmStateSink
	realmSms           *boxsms.Manager
	smsConfig          *boxsms.Service
}

func newAPI(configDir string, defaultDhtMode string, hostnameOverride string) (*api, error) {
	var (
		configService  *earlyconfig.Service
		realmConfigSvc *realmconfig.Service
		smsConfigSvc   *boxsms.Service
		err            error
	)
	if configDir == "" {
		configService, err = earlyconfig.New()
		if err == nil {
			realmConfigSvc, err = realmconfig.New(defaultDhtMode)
		}
		if err == nil {
			smsConfigSvc, err = boxsms.New()
		}
	} else {
		configService, err = earlyconfig.NewInDir(configDir)
		if err == nil {
			realmConfigSvc, err = realmconfig.NewInDir(configDir, defaultDhtMode)
		}
		if err == nil {
			smsConfigSvc, err = boxsms.NewInDir(configDir)
		}
	}
	if err != nil {
		return nil, err
	}

	uiDir, err := resolveConfigDir(configDir)
	if err != nil {
		return nil, err
	}
	uiConfigSvc, err := newUIConfigService(uiDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize web UI config: %w", err)
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
	announceFeature := realmannounce.New(mapsFeature,
		func() string { return appspec.Report(dataDir) },
		func() realmannounce.SpecSummary {
			s := appspec.GetSummary(dataDir)
			return realmannounce.SpecSummary{OS: s.OS, CPU: s.CPU, Mem: s.Mem, Battery: s.Battery, GPU: s.GPU, Disk: s.Disk}
		},
		func() string { return resolveHostname(hostnameOverride) },
		appVersion,
	)
	speedTestFeature := boxspeedtest.New()
	smsManager := boxsms.NewManager(mapsFeature, smsConfigSvc, func() string { return realmConfigSvc.Load().PeerID.ID })
	// Forward-declared: onReceive only fires after a is fully constructed.
	var a *api
	identityFeature := realmidentity.New(func(name string, kp realmmodel.KeyPair) error {
		return a.importPushedIdentity(name, kp)
	})
	groupFeature := realmgroup.New(func(name string, kp realmmodel.KeyPair) error {
		return a.importPushedGroup(name, kp)
	})
	realmEng.Register(scriptsFeature)
	realmEng.Register(servicesFeature)
	realmEng.Register(mapsFeature)
	realmEng.Register(announceFeature)
	realmEng.Register(speedTestFeature)
	realmEng.Register(identityFeature)
	realmEng.Register(groupFeature)
	realmEng.Register(smsManager)

	a = &api{
		configDir:          configDir,
		hostnameOverride:   hostnameOverride,
		uiConfig:           uiConfigSvc,
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
		realmGroup:         groupFeature,
		realmSms:           smsManager,
		smsConfig:          smsConfigSvc,
	}
	smsManager.Start()

	// Auto-start the realm engine if a peer id already exists; failure here
	// shouldn't block the web UI from starting.
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

// updateRealmConfig loads the current Realm config, applies fn, persists it,
// and reconciles the engine to reflect the change.
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

// importPushedIdentity is the identity feature's onReceive callback: auto-imports
// a pushed identity (no user confirmation), renaming on a name collision.
func (a *api) importPushedIdentity(name string, kp realmmodel.KeyPair) error {
	unique := name
	for i := 2; a.identityExists(unique); i++ {
		unique = fmt.Sprintf("%s (%d)", name, i)
	}
	_, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Identities = append(c.Identities, realmmodel.Identity{Name: unique, KeyPair: kp})
	})
	if err != nil {
		return err
	}
	if unique != name {
		log.Printf("realm identity: saved pushed identity %q as %q (name already in use)", name, unique)
	} else {
		log.Printf("realm identity: saved pushed identity %q", unique)
	}
	return nil
}

// importPushedGroup is the group feature's onReceive callback: auto-imports
// a pushed group (no user confirmation), renaming on a name collision. It
// arrives with no permissions granted — the receiving user assigns those
// locally via the Permissions subtab, same as a manually imported group.
func (a *api) importPushedGroup(name string, kp realmmodel.KeyPair) error {
	unique := name
	for i := 2; a.groupExists(unique); i++ {
		unique = fmt.Sprintf("%s (%d)", name, i)
	}
	_, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Groups = append(c.Groups, realmmodel.Group{Name: unique, KeyPair: kp})
	})
	if err != nil {
		return err
	}
	if unique != name {
		log.Printf("realm group: saved pushed group %q as %q (name already in use)", name, unique)
	} else {
		log.Printf("realm group: saved pushed group %q", unique)
	}
	return nil
}

// resolveHostname returns override if set, else the OS-reported hostname.
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

// ensureRealmListenPort backfills cfg.RealmListenPort with a free port, once,
// so the peer's advertised addresses stay stable across restarts. No-op if
// already assigned or if a free port couldn't be picked (falls back to
// libp2p's random-port default).
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

// handlers maps each action name to its handler, defined across api_realm.go,
// api_early.go, and api_misc.go by domain.
var handlers = map[string]handlerFunc{
	"spec.report":         handleSpecReport,
	"troubleshooting.run": handleTroubleshootingRun,
	"logs.read":           handleLogsRead,
	"logs.clear":          handleLogsClear,

	"config.loadConfig":       handleConfigLoadConfig,
	"config.saveConfig":       handleConfigSaveConfig,
	"config.loadTabStats":     handleConfigLoadTabStats,
	"config.recordTabLoad":    handleConfigRecordTabLoad,
	"config.recordSubtabLoad": handleConfigRecordSubtabLoad,

	"early.loadConfig": handleEarlyLoadConfig,
	"early.saveConfig": handleEarlySaveConfig,
	"early.aggregate":  handleEarlyAggregate,
	"early.delete":     handleEarlyDelete,

	"realm.loadConfig":            handleRealmLoadConfig,
	"realm.generatePeerId":        handleRealmGeneratePeerID,
	"realm.addGroup":              handleRealmAddGroup,
	"realm.importGroup":           handleRealmImportGroup,
	"realm.deleteGroup":           handleRealmDeleteGroup,
	"realm.pushGroup":             handleRealmPushGroup,
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
	"realm.clearPeerAddresses":    handleRealmClearPeerAddresses,
	"realm.clearAllPeerAddresses": handleRealmClearAllPeerAddresses,
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

	"sms.loadConfig":           handleSmsLoadConfig,
	"sms.saveManagementConfig": handleSmsSaveManagementConfig,
	"sms.listStores":           handleSmsListStores,
	"sms.listConversations":    handleSmsListConversations,
	"sms.listMessages":         handleSmsListMessages,
	"sms.sendMessage":          handleSmsSendMessage,
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

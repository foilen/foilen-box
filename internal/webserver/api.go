package webserver

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	boxcamera "foilen-box/internal/camera"
	earlyaggregate "foilen-box/internal/early/aggregate"
	earlyclient "foilen-box/internal/early/client"
	earlyconfig "foilen-box/internal/early/config"
	boxgrouptroubleshooting "foilen-box/internal/grouptroubleshooting"
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

type request struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
}

type response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type api struct {
	configDir                 string
	hostnameOverride          string
	uiConfig                  *uiConfigService
	currentPort               int
	earlyConfig               *earlyconfig.Service
	earlyAggregate            *earlyaggregate.Service
	realmConfig               *realmconfig.Service
	realmPeers                *realmpeers.Store
	realmEngine               *realm.Engine
	realmScripts              *realmscripts.Feature
	realmServices             *realmservices.Feature
	realmServicesStore        *realmservices.Store
	realmMapsStore            *realmmaps.Store
	realmMapsFeature          *realmmaps.Feature
	realmSpeedTest            *boxspeedtest.Feature
	realmIdentity             *realmidentity.Feature
	realmGroup                *realmgroup.Feature
	realmStateSink            RealmStateSink
	realmSms                  *boxsms.Manager
	smsConfig                 *boxsms.Service
	realmGroupTroubleshooting *boxgrouptroubleshooting.Manager
	camera                    *boxcamera.Manager
}

func newAPI(configDir string, defaultDhtMode string, hostnameOverride string) (*api, error) {
	configService, err := earlyconfig.New(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Early config: %w", err)
	}
	realmConfigSvc, err := realmconfig.New(configDir, defaultDhtMode)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize realm config: %w", err)
	}
	smsConfigSvc, err := boxsms.NewConfigService(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize SMS config: %w", err)
	}
	uiConfigSvc, err := newUIConfigService(configDir)
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
	cameraManager, err := boxcamera.NewManager(realmConfigSvc.Dir())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize camera manager: %w", err)
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
	smsManager := boxsms.NewManager(mapsFeature, smsConfigSvc,
		func() string { return realmConfigSvc.Load().PeerID.ID },
		func() []realmmodel.Group { return realmConfigSvc.Load().Groups },
	)
	groupTroubleshootingManager := boxgrouptroubleshooting.NewManager(mapsFeature, realmEng, realmPeerStore,
		func() string { return realmConfigSvc.Load().PeerID.ID },
		func() []realmmodel.Group { return realmConfigSvc.Load().Groups },
	)

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
		configDir:                 configDir,
		hostnameOverride:          hostnameOverride,
		uiConfig:                  uiConfigSvc,
		earlyConfig:               configService,
		earlyAggregate:            earlyaggregate.New(earlyclient.New(), configService),
		realmConfig:               realmConfigSvc,
		realmPeers:                realmPeerStore,
		realmEngine:               realmEng,
		realmScripts:              scriptsFeature,
		realmServices:             servicesFeature,
		realmServicesStore:        realmServicesStore,
		realmMapsStore:            realmMapsStore,
		realmMapsFeature:          mapsFeature,
		realmSpeedTest:            speedTestFeature,
		realmIdentity:             identityFeature,
		realmGroup:                groupFeature,
		realmSms:                  smsManager,
		smsConfig:                 smsConfigSvc,
		realmGroupTroubleshooting: groupTroubleshootingManager,
		camera:                    cameraManager,
	}
	smsManager.Start()
	groupTroubleshootingManager.Start()
	if err := cameraManager.Start(); err != nil {
		log.Printf("camera: failed to auto-start RTSP server: %v", err)
	}

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

func (a *api) shutdown() {
	a.realmEngine.Stop()
	a.realmServices.StopAll()
	a.camera.Stop()
	if err := a.realmPeers.Flush(); err != nil {
		log.Printf("realm: failed to flush peer store: %v", err)
	}
	if err := a.realmServicesStore.Flush(); err != nil {
		log.Printf("realm: failed to flush services store: %v", err)
	}
	if err := a.realmMapsStore.Close(); err != nil {
		log.Printf("realm: failed to close maps store: %v", err)
	}
}

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

func ensureRealmListenPort(svc *realmconfig.Service, cfg realmmodel.Config) realmmodel.Config {
	if cfg.RealmListenPortMode == realmmodel.ListenPortModeSpecific {
		return cfg
	}
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

type handlerFunc func(a *api, params json.RawMessage) (any, error)

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
	"realm.setDescription":        handleRealmSetDescription,
	"realm.setEnabled":            handleRealmSetEnabled,
	"realm.setDhtMode":            handleRealmSetDhtMode,
	"realm.setDiscoveryOptions":   handleRealmSetDiscoveryOptions,
	"realm.setEnableRelayService": handleRealmSetEnableRelayService,
	"realm.setPeerRetentionDays":  handleRealmSetPeerRetentionDays,
	"realm.setListenPort":         handleRealmSetListenPort,
	"realm.setExposeWeb":          handleRealmSetExposeWeb,

	"realm.addGroup":    handleRealmAddGroup,
	"realm.importGroup": handleRealmImportGroup,
	"realm.deleteGroup": handleRealmDeleteGroup,
	"realm.exportGroup": handleRealmExportGroup,
	"realm.pushGroup":   handleRealmPushGroup,

	"realm.addIdentity":    handleRealmAddIdentity,
	"realm.importIdentity": handleRealmImportIdentity,
	"realm.deleteIdentity": handleRealmDeleteIdentity,
	"realm.exportIdentity": handleRealmExportIdentity,
	"realm.pushIdentity":   handleRealmPushIdentity,

	"realm.addPermission":    handleRealmAddPermission,
	"realm.deletePermission": handleRealmDeletePermission,

	"realm.listPeers":             handleRealmListPeers,
	"realm.listSwarmPeers":        handleRealmListSwarmPeers,
	"realm.clearPeerAddresses":    handleRealmClearPeerAddresses,
	"realm.clearAllPeerAddresses": handleRealmClearAllPeerAddresses,
	"realm.forcePeriodicTick":     handleRealmForcePeriodicTick,
	"realm.deletePeer":            handleRealmDeletePeer,

	"realm.addScript":      handleRealmAddScript,
	"realm.updateScript":   handleRealmUpdateScript,
	"realm.deleteScript":   handleRealmDeleteScript,
	"realm.runPeerScript":  handleRealmRunPeerScript,
	"realm.listScriptRuns": handleRealmListScriptRuns,

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

	"camera.getStatus":        handleCameraGetStatus,
	"camera.listDevices":      handleCameraListDevices,
	"camera.listAudioDevices": handleCameraListAudioDevices,
	"camera.saveConfig":       handleCameraSaveConfig,
	"camera.startCapture":     handleCameraStartCapture,
	"camera.stopCapture":      handleCameraStopCapture,

	"sms.loadConfig":           handleSmsLoadConfig,
	"sms.saveManagementConfig": handleSmsSaveManagementConfig,
	"sms.listStores":           handleSmsListStores,
	"sms.listConversations":    handleSmsListConversations,
	"sms.listMessages":         handleSmsListMessages,
	"sms.sendMessage":          handleSmsSendMessage,

	"groupTroubleshooting.start": handleGroupTroubleshootingStart,
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

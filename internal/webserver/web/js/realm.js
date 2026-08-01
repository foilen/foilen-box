import { report } from "./util.js";
import { initRealmGroups } from "./realm-groups.js";
import { initRealmIdentities } from "./realm-identities.js";
import { initRealmPermissions } from "./realm-permissions.js";
import { initRealmPeers } from "./realm-peers.js";
import { initRealmSpecs } from "./realm-specs.js";
import { initRealmScripts } from "./realm-scripts.js";
import { initRealmServices } from "./realm-services.js";
import { initRealmSpeedtest } from "./realm-speedtest.js";
import { initRealmMaps } from "./realm-maps.js";
import { parseHash, updateHash } from "./hash.js";

// initRealmSubtabs wires subtab switching; onActivate, if given, is called
// with the activated button's data-subtab value every time a subtab is
// switched to (used by Services to refresh stale peers on activation).
function initRealmSubtabs(onActivate) {
	const buttons = document.querySelectorAll("#realm-subtabs .subtab-button");
	function activate(button) {
		buttons.forEach((b) => b.classList.remove("active"));
		document.querySelectorAll("#realm .subtab-panel").forEach((p) => p.classList.remove("active"));
		button.classList.add("active");
		document.getElementById(button.dataset.subtab).classList.add("active");
		if (onActivate) onActivate(button.dataset.subtab);
	}
	buttons.forEach((button) => {
		button.addEventListener("click", () => {
			console.log("[action] switch realm subtab", { subtab: button.dataset.subtab });
			activate(button);
			updateHash();
		});
	});
	const { tab, subtab } = parseHash();
	const fromHash = tab === "realm" && subtab && [...buttons].find((b) => b.dataset.subtab === subtab);
	activate(fromHash || buttons[0]);
}

export function initRealmTab(api, isAndroid) {
	const enabledCheckbox = document.getElementById("realm-enabled");
	const peerIdEl = document.getElementById("realm-peer-id");
	const generatePeerIdButton = document.getElementById("realm-generate-peer-id-button");
	const hostnameEl = document.getElementById("realm-hostname");
	const descriptionInput = document.getElementById("realm-description");
	const saveDescriptionButton = document.getElementById("realm-save-description-button");
	const enableMdnsCheckbox = document.getElementById("realm-enable-mdns");
	// mDNS (LAN discovery) isn't supported on Android — see
	// realm.mdnsSupported in the Go backend — so don't offer a toggle that
	// would silently do nothing; DHT discovery is unaffected. The detached
	// enableMdnsCheckbox reference stays valid for the rest of this module
	// (setting .checked on it below is a harmless no-op), so nothing else
	// needs to change.
	if (isAndroid) {
		document.getElementById("realm-mdns-row").remove();
	}
	const enableDhtCheckbox = document.getElementById("realm-enable-dht");
	const dhtModeSelect = document.getElementById("realm-dht-mode");
	const peerRetentionDaysInput = document.getElementById("realm-peer-retention-days");
	const enableRelayServiceCheckbox = document.getElementById("realm-enable-relay-service");
	const exposeWebEnabledCheckbox = document.getElementById("realm-expose-web-enabled");
	const exposeWebFields = document.getElementById("realm-expose-web-fields");
	const exposeWebListenProtocolSelect = document.getElementById("realm-expose-web-listen-protocol");
	const exposeWebListenPortInput = document.getElementById("realm-expose-web-listen-port");
	const exposeWebAnnounceHostInput = document.getElementById("realm-expose-web-announce-host");
	const exposeWebAnnouncePortInput = document.getElementById("realm-expose-web-announce-port");
	const exposeWebAnnounceProtocolSelect = document.getElementById("realm-expose-web-announce-protocol");
	const output = document.getElementById("realm-output");

	// renderConfig is the fan-out for the single "full config" response the
	// backend returns from every realm.* config mutation: it updates the
	// identity/discovery fields owned by this module directly, and defers
	// to the groups/permissions subtabs for their own tables. Forward
	// declared so it can be handed to initRealmGroups/initRealmPermissions
	// before their own render functions exist.
	let renderGroups = () => {};
	let renderIdentities = () => {};
	let onIdentitiesConfigUpdate = () => {};
	let renderPermissions = () => {};
	let updatePeersForPermissions = () => {};
	let renderMyScripts = () => {};
	let renderMyServices = () => {};
	let onMapsConfigUpdate = () => {};
	let onServicesConfigUpdate = () => {};
	let onSpecsConfigUpdate = () => {};
	let onScriptsConfigUpdate = () => {};
	let onSpeedtestConfigUpdate = () => {};

	// ownPeer/latestPeers/pushPeers: the local peer's own hostname/description
	// come from realm.loadConfig, not from the discovered-peers list (a node
	// never discovers itself via mDNS/DHT), but its id shows up in maps
	// (specs/scripts/services entries the peer posts about itself). So we
	// synthesize a pseudo-peer entry for it and merge it into the peers list
	// handed to every subtab, letting formatKnownPeerLabel resolve it to a
	// proper "hostname (description)" label instead of falling back to a
	// bare shortened id.
	let ownPeer = null;
	let latestPeers = [];

	function pushPeers() {
		const peers = ownPeer ? [ownPeer, ...latestPeers.filter((p) => p.id !== ownPeer.id)] : latestPeers;
		updatePeersForPermissions(peers);
		servicesModule.onPeersUpdate(peers);
		specsModule.onPeersUpdate(peers);
		scriptsModule.onPeersUpdate(peers);
		speedtestModule.onPeersUpdate(peers);
		identitiesModule.onPeersUpdate(peers);
	}

	function renderConfig(cfg) {
		enabledCheckbox.checked = cfg.enabled;
		peerIdEl.textContent = cfg.peerId || "(none)";
		generatePeerIdButton.classList.toggle("hidden", !!cfg.peerId);
		hostnameEl.textContent = cfg.hostname || "(unknown)";
		if (document.activeElement !== descriptionInput) {
			descriptionInput.value = cfg.description || "";
		}
		enableMdnsCheckbox.checked = cfg.enableMdns;
		enableDhtCheckbox.checked = cfg.enableDht;
		dhtModeSelect.value = cfg.dhtMode || "client";
		if (document.activeElement !== peerRetentionDaysInput) {
			peerRetentionDaysInput.value = cfg.peerRetentionDays || 0;
		}
		enableRelayServiceCheckbox.checked = cfg.enableRelayService;

		exposeWebEnabledCheckbox.checked = cfg.exposeWebEnabled;
		exposeWebFields.classList.toggle("hidden", !cfg.exposeWebEnabled);
		exposeWebListenProtocolSelect.value = cfg.exposeWebListenProtocol || "wss";
		if (document.activeElement !== exposeWebListenPortInput) {
			exposeWebListenPortInput.value = cfg.exposeWebListenPort || 443;
		}
		if (document.activeElement !== exposeWebAnnounceHostInput) {
			exposeWebAnnounceHostInput.value = cfg.exposeWebAnnounceHost || "";
		}
		if (document.activeElement !== exposeWebAnnouncePortInput) {
			exposeWebAnnouncePortInput.value = cfg.exposeWebAnnouncePort || "";
		}
		exposeWebAnnounceProtocolSelect.value = cfg.exposeWebAnnounceProtocol || "";

		renderGroups(cfg);
		renderIdentities(cfg);
		onIdentitiesConfigUpdate(cfg);
		renderPermissions(cfg);
		renderMyScripts(cfg);
		renderMyServices(cfg);
		onMapsConfigUpdate(cfg);
		onServicesConfigUpdate(cfg);
		onSpecsConfigUpdate(cfg);
		onScriptsConfigUpdate(cfg);
		onSpeedtestConfigUpdate(cfg);

		ownPeer = cfg.peerId ? { id: cfg.peerId, hostname: cfg.hostname, description: cfg.description } : null;
		pushPeers();
	}

	renderGroups = initRealmGroups(api, output, renderConfig).renderGroups;
	const identitiesModule = initRealmIdentities(api, output, renderConfig);
	renderIdentities = identitiesModule.renderIdentities;
	onIdentitiesConfigUpdate = identitiesModule.onConfigUpdate;
	const permissionsModule = initRealmPermissions(api, output, renderConfig);
	renderPermissions = permissionsModule.renderPermissions;
	updatePeersForPermissions = permissionsModule.updatePeers;
	const scriptsModule = initRealmScripts(api, output, renderConfig);
	renderMyScripts = scriptsModule.renderMyScripts;
	onScriptsConfigUpdate = scriptsModule.onConfigUpdate;
	const servicesModule = initRealmServices(api, output, renderConfig);
	renderMyServices = servicesModule.renderMyServices;
	onServicesConfigUpdate = servicesModule.onConfigUpdate;
	const speedtestModule = initRealmSpeedtest(api, output);
	onSpeedtestConfigUpdate = speedtestModule.onConfigUpdate;
	const mapsModule = initRealmMaps(api, output, renderConfig);
	onMapsConfigUpdate = mapsModule.onConfigUpdate;
	const specsModule = initRealmSpecs(api);
	onSpecsConfigUpdate = specsModule.onConfigUpdate;
	initRealmPeers(api, (peers) => {
		latestPeers = peers;
		pushPeers();
	});
	initRealmSubtabs((subtab) => {
		if (subtab === "realm-maps-subtab") mapsModule.onSubtabActivated();
	});

	api.call("realm.loadConfig").then(renderConfig);

	enabledCheckbox.addEventListener("change", () =>
		report(output, async () => {
			console.log("[action] change realm enabled", { enabled: enabledCheckbox.checked });
			renderConfig(await api.call("realm.setEnabled", { enabled: enabledCheckbox.checked }));
		})
	);

	generatePeerIdButton.addEventListener("click", () =>
		report(output, async () => {
			console.log("[action] generate peer id");
			renderConfig(await api.call("realm.generatePeerId"));
			output.textContent = "Peer ID generated.";
		})
	);

	saveDescriptionButton.addEventListener("click", () =>
		report(output, async () => {
			const description = descriptionInput.value.trim();
			console.log("[action] save realm description", { description });
			renderConfig(await api.call("realm.setDescription", { description }));
			output.textContent = "Description saved.";
		})
	);

	function saveDiscoveryOptions() {
		return report(output, async () => {
			console.log("[action] change discovery options", { enableMdns: enableMdnsCheckbox.checked, enableDht: enableDhtCheckbox.checked });
			renderConfig(
				await api.call("realm.setDiscoveryOptions", {
					enableMdns: enableMdnsCheckbox.checked,
					enableDht: enableDhtCheckbox.checked,
				})
			);
		});
	}
	enableMdnsCheckbox.addEventListener("change", saveDiscoveryOptions);
	enableDhtCheckbox.addEventListener("change", saveDiscoveryOptions);

	dhtModeSelect.addEventListener("change", () =>
		report(output, async () => {
			console.log("[action] change dht mode", { mode: dhtModeSelect.value });
			renderConfig(await api.call("realm.setDhtMode", { mode: dhtModeSelect.value }));
		})
	);

	peerRetentionDaysInput.addEventListener("change", () =>
		report(output, async () => {
			const peerRetentionDays = parseInt(peerRetentionDaysInput.value, 10) || 0;
			console.log("[action] change peer retention days", { peerRetentionDays });
			renderConfig(await api.call("realm.setPeerRetentionDays", { peerRetentionDays }));
		})
	);

	enableRelayServiceCheckbox.addEventListener("change", () =>
		report(output, async () => {
			console.log("[action] change enable relay service", { enableRelayService: enableRelayServiceCheckbox.checked });
			renderConfig(
				await api.call("realm.setEnableRelayService", { enableRelayService: enableRelayServiceCheckbox.checked })
			);
		})
	);

	function saveExposeWeb() {
		return report(output, async () => {
			const params = {
				enabled: exposeWebEnabledCheckbox.checked,
				listenProtocol: exposeWebListenProtocolSelect.value,
				listenPort: parseInt(exposeWebListenPortInput.value, 10) || 0,
				announceHost: exposeWebAnnounceHostInput.value.trim(),
				announcePort: parseInt(exposeWebAnnouncePortInput.value, 10) || 0,
				announceProtocol: exposeWebAnnounceProtocolSelect.value,
			};
			console.log("[action] change expose web settings", params);
			renderConfig(await api.call("realm.setExposeWeb", params));
		});
	}
	exposeWebEnabledCheckbox.addEventListener("change", () => {
		exposeWebFields.classList.toggle("hidden", !exposeWebEnabledCheckbox.checked);
		saveExposeWeb();
	});
	exposeWebListenProtocolSelect.addEventListener("change", saveExposeWeb);
	exposeWebListenPortInput.addEventListener("change", saveExposeWeb);
	exposeWebAnnounceHostInput.addEventListener("change", saveExposeWeb);
	exposeWebAnnouncePortInput.addEventListener("change", saveExposeWeb);
	exposeWebAnnounceProtocolSelect.addEventListener("change", saveExposeWeb);
}

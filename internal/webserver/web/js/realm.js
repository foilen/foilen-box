import { report } from "./util.js";
import { initRealmGroups } from "./realm-groups.js";
import { initRealmIdentities } from "./realm-identities.js";
import { initRealmPermissions } from "./realm-permissions.js";
import { initRealmPeers } from "./realm-peers.js";
import { initRealmSpecs } from "./realm-specs.js";
import { initRealmScripts } from "./realm-scripts.js";
import { initRealmServices } from "./realm-services.js";
import { initRealmSpeedtest } from "./realm-speedtest.js";
import { initRealmSms } from "./realm-sms.js";
import { initRealmMaps } from "./realm-maps.js";
import { parseHash, updateHash } from "./hash.js";

// Wires subtab switching. onActivate (optional) is called with the
// activated subtab on every switch — used by Services to refresh stale peers.
function initRealmSubtabs(api, onActivate) {
	const buttons = document.querySelectorAll("#realm-subtabs .subtab-button");
	function activate(button, extra) {
		buttons.forEach((b) => b.classList.remove("active"));
		document.querySelectorAll("#realm .subtab-panel").forEach((p) => p.classList.remove("active"));
		button.classList.add("active");
		document.getElementById(button.dataset.subtab).classList.add("active");
		api.call("config.recordSubtabLoad", { subtabId: button.dataset.subtab }).catch(() => {});
		if (onActivate) onActivate(button.dataset.subtab, extra);
	}
	buttons.forEach((button) => {
		button.addEventListener("click", (event) => {
			if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
			event.preventDefault();
			console.log("[action] switch realm subtab", { subtab: button.dataset.subtab });
			activate(button);
			updateHash();
		});
	});
	const { tab, subtab, extra } = parseHash();
	const fromHash = tab === "realm" && subtab && [...buttons].find((b) => b.dataset.subtab === subtab);
	activate(fromHash || buttons[0], fromHash ? extra : null);
}

export function initRealmTab(api, isAndroid) {
	const enabledCheckbox = document.getElementById("realm-enabled");
	const peerIdEl = document.getElementById("realm-peer-id");
	const generatePeerIdButton = document.getElementById("realm-generate-peer-id-button");
	const hostnameEl = document.getElementById("realm-hostname");
	const descriptionInput = document.getElementById("realm-description");
	const saveDescriptionButton = document.getElementById("realm-save-description-button");
	const enableMdnsCheckbox = document.getElementById("realm-enable-mdns");
	// mDNS isn't supported on Android (see realm.mdnsSupported in the Go
	// backend); remove the toggle rather than offer one that does nothing.
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

	// renderConfig fans out the "full config" response every realm.* mutation
	// returns, updating this module's fields and deferring to each subtab for
	// its own tables. Forward-declared so it can be handed to
	// initRealmGroups/initRealmPermissions before their render functions exist.
	let renderGroups = () => {};
	let onGroupsConfigUpdate = () => {};
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
	let onSmsConfigUpdate = () => {};

	// A node never discovers itself via mDNS/DHT, but its id shows up in maps
	// (specs/scripts/services it posts about itself). Synthesize a pseudo-peer
	// entry from realm.loadConfig and merge it into the peers list handed to
	// every subtab, so formatKnownPeerLabel can resolve it to a proper label.
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
		groupsModule.onPeersUpdate(peers);
		smsModule.onPeersUpdate(peers);
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
		onGroupsConfigUpdate(cfg);
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
		onSmsConfigUpdate(cfg);

		ownPeer = cfg.peerId ? { id: cfg.peerId, hostname: cfg.hostname, description: cfg.description } : null;
		pushPeers();
	}

	const groupsModule = initRealmGroups(api, output, renderConfig);
	renderGroups = groupsModule.renderGroups;
	onGroupsConfigUpdate = groupsModule.onConfigUpdate;
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
	const smsModule = initRealmSms(api, output, isAndroid);
	onSmsConfigUpdate = smsModule.onConfigUpdate;
	const mapsModule = initRealmMaps(api, output, renderConfig);
	onMapsConfigUpdate = mapsModule.onConfigUpdate;
	const specsModule = initRealmSpecs(api);
	onSpecsConfigUpdate = specsModule.onConfigUpdate;
	initRealmPeers(api, (peers) => {
		latestPeers = peers;
		pushPeers();
	});
	initRealmSubtabs(api, (subtab, extra) => {
		if (subtab === "realm-maps-subtab") mapsModule.onSubtabActivated();
		if (subtab === "realm-sms-subtab") smsModule.onSubtabActivated(extra);
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

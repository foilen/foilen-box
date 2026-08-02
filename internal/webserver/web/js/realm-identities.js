import { report, formatPeerLabel, formatIdentityLabel, syncList, syncCells } from "./util.js";
import { initQrModal, initScanModal } from "./realm-qr.js";

const IDENTITIES_POLL_INTERVAL_MS = 5000;

// initRealmIdentities wires the Identities subtab: listing, generating,
// importing, exporting, deleting, and pushing standalone identities.
// renderConfig is the top-level fan-out (see realm.js) called after any
// mutation, since the backend returns the full config on every change. It's
// also polled here (like realm-peers.js/realm-maps.js poll their own state)
// so an identity another peer pushes to us shows up without a manual page
// reload, since there's no server->client push channel in this app.
export function initRealmIdentities(api, output, renderConfig) {
	const identitiesBody = document.getElementById("realm-identities-tbody");
	const identitiesCount = document.getElementById("realm-identities-count");
	const newIdentityNameInput = document.getElementById("realm-new-identity-name");
	const addIdentityButton = document.getElementById("realm-add-identity-button");
	const showImportButton = document.getElementById("realm-import-identity-button");
	const importForm = document.getElementById("realm-identity-import-form");
	const importFileInput = document.getElementById("realm-identity-import-file");
	const importJsonInput = document.getElementById("realm-identity-import-json");
	const importConfirmButton = document.getElementById("realm-identity-import-confirm-button");
	const importScanButton = document.getElementById("realm-identity-import-scan-button");
	const importCancelButton = document.getElementById("realm-identity-import-cancel-button");

	const pushIdentitySelect = document.getElementById("realm-push-identity-select");
	const pushPeerSelect = document.getElementById("realm-push-identity-peer-select");
	const pushButton = document.getElementById("realm-push-identity-button");

	const showQrCode = initQrModal();
	const startScan = initScanModal((data) => {
		importJsonInput.value = data;
		importForm.classList.remove("hidden");
	});

	let identities = [];
	let knownPeers = [];
	let ownPeerId = "";

	// syncOptions reconciles an <md-outlined-select>'s <md-select-option>
	// children in place (keyed by option value) instead of clearing and
	// rebuilding them. This is what lets both selects be re-rendered freely
	// on every relevant refresh (the identities list changing, or the peers
	// poll every few seconds — see realm-peers.js) without desyncing the
	// select's shown value: the currently-selected option's node is never
	// torn down, so there's nothing for md-outlined-select's async
	// re-processing of a fresh option list to get out of sync with.
	function syncOptions(select, entries) {
		syncList(
			select,
			entries,
			([value]) => value,
			([value, label]) => {
				const option = document.createElement("md-select-option");
				option.value = value;
				option.innerHTML = `<div slot="headline">${label}</div>`;
				return option;
			},
			(option, [, label]) => {
				const headline = option.querySelector('[slot="headline"]');
				if (headline.textContent !== label) headline.textContent = label;
			}
		);
	}

	function renderPushIdentityOptions() {
		syncOptions(pushIdentitySelect, identities.map((identity) => [identity.name, formatIdentityLabel(identity)]));
	}

	function renderPushPeerOptions() {
		syncOptions(
			pushPeerSelect,
			knownPeers.filter((peer) => peer.id !== ownPeerId).map((peer) => [peer.id, formatPeerLabel(peer)])
		);
	}

	function renderIdentities(cfg) {
		identities = cfg.identities || [];
		identitiesCount.textContent = identities.length;
		syncList(
			identitiesBody,
			identities,
			(identity) => identity.name,
			(identity) => {
				const row = document.createElement("tr");
				syncCells(row, [["ID", formatIdentityLabel(identity)]]);

				const exportCell = document.createElement("td");
				exportCell.dataset.label = "Export";
				const exportButton = document.createElement("md-text-button");
				exportButton.textContent = "Export";
				exportButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] export identity", { identity: identity.name });
						const data = await api.call("realm.exportIdentity", { name: identity.name });
						const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
						const url = URL.createObjectURL(blob);
						const link = document.createElement("a");
						link.href = url;
						link.download = `${identity.name}.json`;
						link.click();
						URL.revokeObjectURL(url);
						output.textContent = `Exported identity "${identity.name}".`;
					})
				);
				exportCell.appendChild(exportButton);
				row.appendChild(exportCell);

				const qrCell = document.createElement("td");
				qrCell.dataset.label = "QR";
				const qrButton = document.createElement("md-text-button");
				qrButton.textContent = "Show QR";
				qrButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] show identity qr", { identity: identity.name });
						const data = await api.call("realm.exportIdentity", { name: identity.name });
						showQrCode(`Identity "${identity.name}"`, { name: data.name, privateKeyBase64: data.privateKeyBase64 });
					})
				);
				qrCell.appendChild(qrButton);
				row.appendChild(qrCell);

				const deleteCell = document.createElement("td");
				deleteCell.dataset.label = "Delete";
				const deleteButton = document.createElement("md-text-button");
				deleteButton.textContent = "Delete";
				deleteButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] delete identity", { identity: identity.name });
						if (!confirm(`Delete identity "${identity.name}"?`)) return;
						renderConfig(await api.call("realm.deleteIdentity", { name: identity.name }));
					})
				);
				deleteCell.appendChild(deleteButton);
				row.appendChild(deleteCell);

				return row;
			},
			(row, identity) => syncCells(row, [["ID", formatIdentityLabel(identity)]])
		);
		renderPushIdentityOptions();
	}

	addIdentityButton.addEventListener("click", () =>
		report(output, async () => {
			const name = newIdentityNameInput.value.trim();
			console.log("[action] add identity", { name });
			if (!name) {
				output.textContent = "Please enter an identity name.";
				return;
			}
			renderConfig(await api.call("realm.addIdentity", { name }));
			newIdentityNameInput.value = "";
			output.textContent = `Identity "${name}" created.`;
		})
	);

	showImportButton.addEventListener("click", () => {
		console.log("[action] show import identity form");
		importForm.classList.remove("hidden");
	});

	importCancelButton.addEventListener("click", () => {
		console.log("[action] cancel import identity");
		importForm.classList.add("hidden");
		importFileInput.value = "";
		importJsonInput.value = "";
	});

	importFileInput.addEventListener("change", () => {
		const file = importFileInput.files[0];
		if (!file) return;
		console.log("[action] select import identity file", { fileName: file.name });
		const reader = new FileReader();
		reader.onload = () => {
			importJsonInput.value = reader.result;
		};
		reader.readAsText(file);
	});

	importScanButton.addEventListener("click", () => startScan("Scan Identity QR Code"));

	importConfirmButton.addEventListener("click", () =>
		report(output, async () => {
			let name, privateKeyBase64;
			try {
				const parsed = JSON.parse(importJsonInput.value);
				name = (parsed.name || "").trim();
				privateKeyBase64 = (parsed.privateKeyBase64 || "").trim();
			} catch (err) {
				output.textContent = "Invalid JSON: " + err.message;
				return;
			}
			console.log("[action] import identity", { name });
			if (!name || !privateKeyBase64) {
				output.textContent = "The JSON must contain an identity name and a private key.";
				return;
			}
			renderConfig(await api.call("realm.importIdentity", { name, privateKeyBase64 }));
			importForm.classList.add("hidden");
			importFileInput.value = "";
			importJsonInput.value = "";
			output.textContent = `Identity "${name}" imported.`;
		})
	);

	pushButton.addEventListener("click", () =>
		report(output, async () => {
			const name = pushIdentitySelect.value;
			const peerId = pushPeerSelect.value;
			console.log("[action] push identity", { name, peerId });
			if (!name || !peerId) {
				output.textContent = "Please select both an identity and a peer.";
				return;
			}
			await api.call("realm.pushIdentity", { name, peerId });
			output.textContent = `Pushed identity "${name}" to ${peerId}.`;
		})
	);

	setInterval(() => api.call("realm.loadConfig").then(renderConfig), IDENTITIES_POLL_INTERVAL_MS);

	return {
		renderIdentities,
		onPeersUpdate: (peers) => {
			knownPeers = peers;
			renderPushPeerOptions();
		},
		onConfigUpdate: (cfg) => {
			ownPeerId = cfg.peerId || "";
		},
	};
}

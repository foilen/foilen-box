import { report, formatGroupLabel, formatPeerLabel, syncList, syncCells } from "./util.js";
import { renderActionCheckboxes, checkedActions } from "./realm-actions.js";
import { initQrModal, initScanModal } from "./realm-qr.js";

const GROUPS_POLL_INTERVAL_MS = 5000;

// Wires the Groups section: list/generate/import/export/delete/push.
// renderConfig is the top-level fan-out (see realm.js), called after any
// mutation since the backend returns the full config on every change. Also
// polled here so a group pushed by another peer shows up without a manual
// reload, since there's no server->client push channel in this app.
export function initRealmGroups(api, output, renderConfig) {
	const groupsBody = document.getElementById("realm-groups-tbody");
	const groupsCount = document.getElementById("realm-groups-count");
	const newGroupNameInput = document.getElementById("realm-new-group-name");
	const addGroupButton = document.getElementById("realm-add-group-button");
	const showImportButton = document.getElementById("realm-import-group-button");
	const importForm = document.getElementById("realm-import-form");
	const importFileInput = document.getElementById("realm-import-file");
	const importJsonInput = document.getElementById("realm-import-json");
	const importConfirmButton = document.getElementById("realm-import-confirm-button");
	const importScanButton = document.getElementById("realm-import-scan-button");
	const importCancelButton = document.getElementById("realm-import-cancel-button");
	const newGroupActions = document.getElementById("realm-new-group-actions");
	const importActions = document.getElementById("realm-import-actions");

	const pushGroupSelect = document.getElementById("realm-push-group-select");
	const pushPeerSelect = document.getElementById("realm-push-group-peer-select");
	const pushButton = document.getElementById("realm-push-group-button");

	const showQrCode = initQrModal();
	const startScan = initScanModal((data) => {
		importJsonInput.value = data;
		importForm.classList.remove("hidden");
	});

	let groups = [];
	let knownPeers = [];
	let ownPeerId = "";

	// Patches an <md-outlined-select>'s options in place (keyed by value)
	// instead of rebuilding them, so re-rendering on every refresh doesn't tear
	// down the selected option and desync the select's shown value.
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

	function renderPushGroupOptions() {
		syncOptions(pushGroupSelect, groups.map((group) => [group.name, formatGroupLabel(group)]));
	}

	function renderPushPeerOptions() {
		syncOptions(
			pushPeerSelect,
			knownPeers.filter((peer) => peer.id !== ownPeerId).map((peer) => [peer.id, formatPeerLabel(peer)])
		);
	}

	function renderGroups(cfg) {
		const availableActions = cfg.availableActions || [];
		renderActionCheckboxes(newGroupActions, availableActions);
		renderActionCheckboxes(importActions, availableActions);

		groups = cfg.groups || [];
		groupsCount.textContent = cfg.groups.length;
		syncList(
			groupsBody,
			cfg.groups,
			(group) => group.name,
			(group) => {
				const row = document.createElement("tr");
				syncCells(row, [["ID", formatGroupLabel(group)]]);

				const keyCell = document.createElement("td");
				keyCell.dataset.label = "Private Key";
				const keyButton = document.createElement("md-text-button");
				keyButton.textContent = "Copy Key";
				keyButton.addEventListener("click", () => {
					console.log("[action] copy group private key", { group: group.name });
					navigator.clipboard.writeText(group.privateKeyBase64);
					output.textContent = `Copied the private key for group "${group.name}" — share it with other peers to join.`;
				});
				keyCell.appendChild(keyButton);
				row.appendChild(keyCell);

				const exportCell = document.createElement("td");
				exportCell.dataset.label = "Export";
				const exportButton = document.createElement("md-text-button");
				exportButton.textContent = "Export";
				exportButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] export group", { group: group.name });
						const data = await api.call("realm.exportGroup", { name: group.name });
						const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
						const url = URL.createObjectURL(blob);
						const link = document.createElement("a");
						link.href = url;
						link.download = `${group.name}.json`;
						link.click();
						URL.revokeObjectURL(url);
						output.textContent = `Exported group "${group.name}".`;
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
						console.log("[action] show group qr", { group: group.name });
						const data = await api.call("realm.exportGroup", { name: group.name });
						showQrCode(`Group "${group.name}"`, { name: data.name, privateKeyBase64: data.privateKeyBase64 });
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
						console.log("[action] delete group", { group: group.name });
						if (!confirm(`Delete group "${group.name}"?`)) return;
						renderConfig(await api.call("realm.deleteGroup", { name: group.name }));
					})
				);
				deleteCell.appendChild(deleteButton);
				row.appendChild(deleteCell);

				return row;
			},
			(row, group) => syncCells(row, [["ID", formatGroupLabel(group)]])
		);
		renderPushGroupOptions();
	}

	addGroupButton.addEventListener("click", () =>
		report(output, async () => {
			const name = newGroupNameInput.value.trim();
			console.log("[action] add group", { name });
			if (!name) {
				output.textContent = "Please enter a group name.";
				return;
			}
			renderConfig(await api.call("realm.addGroup", { name, actions: checkedActions(newGroupActions) }));
			newGroupNameInput.value = "";
			output.textContent = `Group "${name}" created.`;
		})
	);

	showImportButton.addEventListener("click", () => {
		console.log("[action] show import group form");
		importForm.classList.remove("hidden");
	});

	importCancelButton.addEventListener("click", () => {
		console.log("[action] cancel import group");
		importForm.classList.add("hidden");
		importFileInput.value = "";
		importJsonInput.value = "";
	});

	importFileInput.addEventListener("change", () => {
		const file = importFileInput.files[0];
		if (!file) return;
		console.log("[action] select import group file", { fileName: file.name });
		const reader = new FileReader();
		reader.onload = () => {
			importJsonInput.value = reader.result;
		};
		reader.readAsText(file);
	});

	importScanButton.addEventListener("click", () => startScan("Scan Group QR Code"));

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
			console.log("[action] import group", { name });
			if (!name || !privateKeyBase64) {
				output.textContent = "The JSON must contain a group name and a private key.";
				return;
			}
			renderConfig(
				await api.call("realm.importGroup", { name, privateKeyBase64, actions: checkedActions(importActions) })
			);
			importForm.classList.add("hidden");
			importFileInput.value = "";
			importJsonInput.value = "";
			output.textContent = `Group "${name}" imported.`;
		})
	);

	pushButton.addEventListener("click", () =>
		report(output, async () => {
			const name = pushGroupSelect.value;
			const peerId = pushPeerSelect.value;
			console.log("[action] push group", { name, peerId });
			if (!name || !peerId) {
				output.textContent = "Please select both a group and a peer.";
				return;
			}
			await api.call("realm.pushGroup", { name, peerId });
			output.textContent = `Pushed group "${name}" to ${peerId}.`;
		})
	);

	setInterval(() => api.call("realm.loadConfig").then(renderConfig), GROUPS_POLL_INTERVAL_MS);

	return {
		renderGroups,
		onPeersUpdate: (peers) => {
			knownPeers = peers;
			renderPushPeerOptions();
		},
		onConfigUpdate: (cfg) => {
			ownPeerId = cfg.peerId || "";
		},
	};
}

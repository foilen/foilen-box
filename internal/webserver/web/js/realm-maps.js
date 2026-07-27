import { report, formatGroupLabel } from "./util.js";

const MAPS_POLL_INTERVAL_MS = 5000;

// initRealmMaps wires the Maps subtab: the map list (create/select/delete)
// and the detail view for the currently-selected map's key-value pairs.
// Map data isn't part of the full-config response (same situation as
// Notifications), so it's fetched on its own via realm.listMaps rather than
// from renderConfig; renderConfig is only used here to read cfg.groups for
// the "create map" group picker.
export function initRealmMaps(api, output, renderConfig) {
	const mapsBody = document.getElementById("realm-maps-tbody");
	const mapsCount = document.getElementById("realm-maps-count");
	const groupSelect = document.getElementById("realm-new-map-group");
	const storeNameInput = document.getElementById("realm-new-map-store-name");
	const createButton = document.getElementById("realm-create-map-button");

	const detail = document.getElementById("realm-map-detail");
	const detailTitle = document.getElementById("realm-map-detail-title");
	const detailBody = document.getElementById("realm-map-detail-tbody");
	const newKeyInput = document.getElementById("realm-map-new-key");
	const newValueInput = document.getElementById("realm-map-new-value");
	const addValueButton = document.getElementById("realm-map-add-value-button");
	const closeDetailButton = document.getElementById("realm-map-close-detail-button");

	let groups = [];
	let selected = null; // { scopeId, storeName } | null

	function renderGroupOptions() {
		groupSelect.innerHTML = "";
		for (const group of groups) {
			const option = document.createElement("md-select-option");
			option.value = group.id;
			option.innerHTML = `<div slot="headline">${formatGroupLabel(group)}</div>`;
			groupSelect.appendChild(option);
		}
	}

	function renderMaps(maps) {
		mapsCount.textContent = maps.length;
		mapsBody.innerHTML = "";
		for (const m of maps) {
			const row = document.createElement("tr");
			const cells = [
				["Group", formatGroupLabel({ id: m.scopeId, name: m.groupName })],
				["Store Name", m.storeName],
				["Entries", m.entryCount],
				["Updated", m.updatedAtUnixMillis ? new Date(m.updatedAtUnixMillis).toLocaleString() : "never"],
			];
			for (const [label, value] of cells) {
				const cell = document.createElement("td");
				cell.textContent = value;
				cell.dataset.label = label;
				row.appendChild(cell);
			}

			const actionsCell = document.createElement("td");
			const selectButton = document.createElement("md-text-button");
			selectButton.textContent = "Open";
			selectButton.addEventListener("click", () =>
				report(output, async () => {
					console.log("[action] open map", { scopeId: m.scopeId, storeName: m.storeName });
					await openMap(m.scopeId, m.storeName);
				})
			);
			actionsCell.appendChild(selectButton);

			const deleteButton = document.createElement("md-text-button");
			deleteButton.textContent = "Delete";
			deleteButton.addEventListener("click", () =>
				report(output, async () => {
					console.log("[action] delete map", { scopeId: m.scopeId, storeName: m.storeName });
					if (!confirm(`Delete map "${m.storeName}"?`)) return;
					const result = await api.call("realm.deleteMap", { scopeId: m.scopeId, storeName: m.storeName });
					renderMaps(result.maps);
					if (selected && selected.scopeId === m.scopeId && selected.storeName === m.storeName) {
						closeDetail();
					}
				})
			);
			actionsCell.appendChild(deleteButton);

			row.appendChild(actionsCell);
			mapsBody.appendChild(row);
		}
	}

	function renderDetail(map) {
		detailTitle.textContent = `Map: ${map.storeName}`;
		detailBody.innerHTML = "";
		const keys = Object.keys(map.entries).sort();
		for (const key of keys) {
			const entry = map.entries[key];
			const row = document.createElement("tr");

			const keyCell = document.createElement("td");
			keyCell.textContent = key;
			keyCell.dataset.label = "Key";
			row.appendChild(keyCell);

			const valueCell = document.createElement("td");
			valueCell.textContent = entry.value;
			valueCell.dataset.label = "Value";
			row.appendChild(valueCell);

			const actionsCell = document.createElement("td");
			const editButton = document.createElement("md-text-button");
			editButton.textContent = "Edit";
			editButton.addEventListener("click", () => {
				newKeyInput.value = key;
				newValueInput.value = entry.value;
			});
			actionsCell.appendChild(editButton);

			const deleteButton = document.createElement("md-text-button");
			deleteButton.textContent = "Delete";
			deleteButton.addEventListener("click", () =>
				report(output, async () => {
					console.log("[action] delete map value", { scopeId: map.scopeId, storeName: map.storeName, key });
					const result = await api.call("realm.deleteMapValue", {
						scopeId: map.scopeId,
						storeName: map.storeName,
						key,
					});
					renderDetail(result);
				})
			);
			actionsCell.appendChild(deleteButton);

			row.appendChild(actionsCell);
			detailBody.appendChild(row);
		}
	}

	async function openMap(scopeId, storeName) {
		selected = { scopeId, storeName };
		const map = await api.call("realm.getMap", { scopeId, storeName });
		renderDetail(map);
		detail.classList.remove("hidden");
	}

	function closeDetail() {
		selected = null;
		detail.classList.add("hidden");
	}

	async function refreshMaps() {
		const result = await api.call("realm.listMaps");
		renderMaps(result.maps || []);
		if (selected) {
			const map = await api.call("realm.getMap", { scopeId: selected.scopeId, storeName: selected.storeName });
			renderDetail(map);
		}
	}

	createButton.addEventListener("click", () =>
		report(output, async () => {
			const scopeId = groupSelect.value;
			const storeName = storeNameInput.value.trim();
			console.log("[action] create map", { scopeId, storeName });
			if (!scopeId) {
				output.textContent = "No group selected — create a group first.";
				return;
			}
			if (!storeName) {
				output.textContent = "Please enter a store name.";
				return;
			}
			const result = await api.call("realm.createMap", { scopeId, storeName });
			renderMaps(result.maps);
			storeNameInput.value = "";
			output.textContent = `Map "${storeName}" created.`;
		})
	);

	addValueButton.addEventListener("click", () =>
		report(output, async () => {
			if (!selected) return;
			const key = newKeyInput.value.trim();
			const value = newValueInput.value;
			console.log("[action] set map value", { ...selected, key });
			if (!key) {
				output.textContent = "Please enter a key.";
				return;
			}
			const result = await api.call("realm.setMapValue", { ...selected, key, value });
			renderDetail(result);
			newKeyInput.value = "";
			newValueInput.value = "";
			refreshMaps();
		})
	);

	closeDetailButton.addEventListener("click", () => {
		console.log("[action] close map detail");
		closeDetail();
	});

	refreshMaps();
	setInterval(refreshMaps, MAPS_POLL_INTERVAL_MS);

	return {
		onConfigUpdate: (cfg) => {
			groups = cfg.groups || [];
			renderGroupOptions();
		},
		onSubtabActivated: refreshMaps,
	};
}

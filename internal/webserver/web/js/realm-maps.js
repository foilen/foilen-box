import { report, formatGroupLabel, formatIdentityLabel, syncList, syncCells } from "./util.js";

const MAPS_POLL_INTERVAL_MS = 5000;

// Wires the Maps subtab: the map list (create/select/delete) and the detail
// view for the selected map's key-value pairs. Map data isn't part of the
// full-config response, so it's fetched via realm.listMaps; renderConfig is
// only used here to read cfg.groups for the "create map" group picker.
export function initRealmMaps(api, output, renderConfig) {
	const mapsBody = document.getElementById("realm-maps-tbody");
	const mapsCount = document.getElementById("realm-maps-count");
	const groupSelect = document.getElementById("realm-new-map-group");
	const storeNameInput = document.getElementById("realm-new-map-store-name");
	const autoDeleteHoursInput = document.getElementById("realm-new-map-auto-delete-hours");
	const identitySelect = document.getElementById("realm-new-map-identity");
	const createButton = document.getElementById("realm-create-map-button");

	const detail = document.getElementById("realm-map-detail");
	const detailTitle = document.getElementById("realm-map-detail-title");
	const detailLocked = document.getElementById("realm-map-detail-locked");
	const detailUnlocked = document.getElementById("realm-map-detail-unlocked");
	const detailBody = document.getElementById("realm-map-detail-tbody");
	const newKeyInput = document.getElementById("realm-map-new-key");
	const newValueInput = document.getElementById("realm-map-new-value");
	const addValueButton = document.getElementById("realm-map-add-value-button");
	const closeDetailButton = document.getElementById("realm-map-close-detail-button");

	let groups = [];
	let identities = [];
	let selected = null; // { groupId, storeName } | null

	function identityLabel(identityId) {
		const identity = identities.find((i) => i.id === identityId);
		return identity ? formatIdentityLabel(identity) : identityId;
	}

	// Patches an <md-outlined-select>'s options in place (keyed by value) so
	// the selected option's node survives a refresh instead of desyncing.
	function syncOptions(select, entries) {
		const previousValue = select.value;
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
		select.value = previousValue;
	}

	function renderGroupOptions() {
		syncOptions(groupSelect, groups.map((group) => [group.id, formatGroupLabel(group)]));
	}

	function renderIdentityOptions() {
		syncOptions(identitySelect, [
			["", "None (unencrypted)"],
			...identities.map((identity) => [identity.id, formatIdentityLabel(identity)]),
		]);
	}

	function mapCells(m) {
		return [
			["Group", formatGroupLabel({ id: m.groupId, name: m.groupName })],
			["Store Name", m.storeName],
			["Entries", m.entryCount],
			["Updated", m.updatedAtUnixMillis ? new Date(m.updatedAtUnixMillis).toLocaleString() : "never"],
			["Auto-delete (hours)", m.autoDeleteEntriesHours || "never"],
			["Encrypted", m.encryptionIdentityId ? `🔒 ${identityLabel(m.encryptionIdentityId)}` : "-"],
		];
	}

	function renderMaps(maps) {
		mapsCount.textContent = maps.length;
		syncList(
			mapsBody,
			maps,
			(m) => `${m.groupId}|${m.storeName}`,
			(m) => {
				const row = document.createElement("tr");
				syncCells(row, mapCells(m));

				const actionsCell = document.createElement("td");
				const selectButton = document.createElement("md-text-button");
				selectButton.textContent = "Open";
				selectButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] open map", { groupId: m.groupId, storeName: m.storeName });
						await openMap(m.groupId, m.storeName);
					})
				);
				actionsCell.appendChild(selectButton);

				const deleteButton = document.createElement("md-text-button");
				deleteButton.textContent = "Delete";
				deleteButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] delete map", { groupId: m.groupId, storeName: m.storeName });
						if (!confirm(`Delete map "${m.storeName}"?`)) return;
						const result = await api.call("realm.deleteMap", { groupId: m.groupId, storeName: m.storeName });
						renderMaps(result.maps);
						if (selected && selected.groupId === m.groupId && selected.storeName === m.storeName) {
							closeDetail();
						}
					})
				);
				actionsCell.appendChild(deleteButton);

				row.appendChild(actionsCell);
				return row;
			},
			(row, m) => syncCells(row, mapCells(m))
		);
	}

	function renderDetail(map) {
		const locked = map.encrypted && !map.encryptionAvailable;
		detailTitle.textContent = locked
			? `Map: ${map.storeName} 🔒 (requires identity ${identityLabel(map.encryptionIdentityId)})`
			: map.encrypted
				? `Map: ${map.storeName} 🔒 ${identityLabel(map.encryptionIdentityId)}`
				: `Map: ${map.storeName}`;
		detailLocked.classList.toggle("hidden", !locked);
		detailUnlocked.classList.toggle("hidden", locked);
		if (locked) return;

		const keys = Object.keys(map.entries).sort();
		syncList(
			detailBody,
			keys,
			(key) => key,
			(key) => {
				const entry = map.entries[key];
				const row = document.createElement("tr");
				row.dataset.value = entry.value;
				syncCells(row, [
					["Key", key],
					["Value", entry.value],
				]);

				const actionsCell = document.createElement("td");
				const editButton = document.createElement("md-text-button");
				editButton.textContent = "Edit";
				editButton.addEventListener("click", () => {
					newKeyInput.value = key;
					newValueInput.value = row.dataset.value;
				});
				actionsCell.appendChild(editButton);

				const deleteButton = document.createElement("md-text-button");
				deleteButton.textContent = "Delete";
				deleteButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] delete map value", { groupId: map.groupId, storeName: map.storeName, key });
						const result = await api.call("realm.deleteMapValue", {
							groupId: map.groupId,
							storeName: map.storeName,
							key,
						});
						renderDetail(result);
					})
				);
				actionsCell.appendChild(deleteButton);

				row.appendChild(actionsCell);
				return row;
			},
			(row, key) => {
				const entry = map.entries[key];
				row.dataset.value = entry.value;
				syncCells(row, [
					["Key", key],
					["Value", entry.value],
				]);
			}
		);
	}

	async function openMap(groupId, storeName) {
		selected = { groupId, storeName };
		const map = await api.call("realm.getMap", { groupId, storeName });
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
			const map = await api.call("realm.getMap", { groupId: selected.groupId, storeName: selected.storeName });
			renderDetail(map);
		}
	}

	createButton.addEventListener("click", () =>
		report(output, async () => {
			const groupId = groupSelect.value;
			const storeName = storeNameInput.value.trim();
			const autoDeleteEntriesHoursRaw = autoDeleteHoursInput.value.trim();
			const autoDeleteEntriesHours = autoDeleteEntriesHoursRaw ? Number(autoDeleteEntriesHoursRaw) : 0;
			const encryptToIdentityId = identitySelect.value;
			console.log("[action] create map", { groupId, storeName, autoDeleteEntriesHours, encryptToIdentityId });
			if (!groupId) {
				output.textContent = "No group selected — create a group first.";
				return;
			}
			if (!storeName) {
				output.textContent = "Please enter a store name.";
				return;
			}
			const result = await api.call("realm.createMap", { groupId, storeName, autoDeleteEntriesHours, encryptToIdentityId });
			renderMaps(result.maps);
			storeNameInput.value = "";
			autoDeleteHoursInput.value = "";
			identitySelect.value = "";
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
			identities = cfg.identities || [];
			renderGroupOptions();
			renderIdentityOptions();
		},
		onSubtabActivated: refreshMaps,
	};
}

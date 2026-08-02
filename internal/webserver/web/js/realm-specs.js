import { formatKnownPeerLabel, syncList, syncCells } from "./util.js";

const SPECS_POLL_INTERVAL_MS = 5000;
const SPECS_STORE_NAME = "common";
const SPECS_KEY_PREFIX = "specs/";

// initRealmSpecs wires the (read-only) Specs subtab: every peer posts its own
// spec into the "common" store of each group it belongs to (see
// internal/webserver/realm_announce.go), so this just polls every currently
// configured group's map and aggregates the specs/{peerId} entries.
export function initRealmSpecs(api) {
	const output = document.getElementById("realm-output");
	const specsBody = document.getElementById("realm-specs-tbody");

	let knownPeers = [];
	let groups = [];

	function specCells(s) {
		return [
			["Peer", formatKnownPeerLabel(knownPeers, s.peerId)],
			["Fetched", s.fetchedAt ? new Date(s.fetchedAt).toLocaleString() : ""],
			["CPU", s.cpu || ""],
			["Mem", s.mem || ""],
			["Battery", s.battery || ""],
			["GPU", s.gpu || ""],
			["Disk", s.disk || ""],
		];
	}

	function renderSpecs(specs) {
		syncList(
			specsBody,
			specs,
			(s) => s.peerId,
			(s) => {
				const row = document.createElement("tr");
				row.dataset.text = s.text;
				syncCells(row, specCells(s));

				const viewCell = document.createElement("td");
				viewCell.dataset.label = "View";
				const viewButton = document.createElement("md-text-button");
				viewButton.textContent = "View";
				viewButton.addEventListener("click", () => {
					console.log("[action] view peer spec", { peer: s.peerId });
					output.textContent = row.dataset.text;
				});
				viewCell.appendChild(viewButton);
				row.appendChild(viewCell);

				return row;
			},
			(row, s) => {
				row.dataset.text = s.text;
				syncCells(row, specCells(s));
			}
		);
	}

	async function refreshSpecs() {
		const specsByPeer = new Map();
		for (const group of groups) {
			const map = await api.call("realm.getMap", { groupId: group.id, storeName: SPECS_STORE_NAME });
			for (const [key, entry] of Object.entries(map.entries || {})) {
				if (!key.startsWith(SPECS_KEY_PREFIX)) continue;
				const peerId = key.slice(SPECS_KEY_PREFIX.length);
				let parsed;
				try {
					parsed = JSON.parse(entry.value);
				} catch {
					continue;
				}
				const existing = specsByPeer.get(peerId);
				if (existing && existing.updatedAtUnixMillis >= entry.updatedAtUnixMillis) continue;
				specsByPeer.set(peerId, {
					peerId,
					text: parsed.text || "",
					cpu: parsed.cpu || "",
					mem: parsed.mem || "",
					battery: parsed.battery || "",
					gpu: parsed.gpu || "",
					disk: parsed.disk || "",
					fetchedAt: parsed.fetchedAt,
					updatedAtUnixMillis: entry.updatedAtUnixMillis,
				});
			}
		}
		renderSpecs(Array.from(specsByPeer.values()).sort((a, b) => a.peerId.localeCompare(b.peerId)));
	}

	refreshSpecs();
	setInterval(refreshSpecs, SPECS_POLL_INTERVAL_MS);

	return {
		onPeersUpdate: (peers) => {
			knownPeers = peers;
		},
		onConfigUpdate: (cfg) => {
			groups = cfg.groups || [];
			refreshSpecs();
		},
	};
}

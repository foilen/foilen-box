import { report, formatPeerLabel, syncList, syncCells } from "./util.js";

const PEERS_POLL_INTERVAL_MS = 5000;

// Wires the Peers/Swarm tables. onPeersUpdate (optional) gets the latest
// peers list on every refresh — used by the permissions subtab.
export function initRealmPeers(api, output, onPeersUpdate) {
	const peersBody = document.getElementById("realm-peers-tbody");
	const peersCount = document.getElementById("realm-peers-count");
	const swarmBody = document.getElementById("realm-swarm-tbody");
	const swarmCount = document.getElementById("realm-swarm-count");
	const clearAllButton = document.getElementById("realm-clear-all-peer-addresses-button");
	const forcePeriodicTickButton = document.getElementById("realm-force-periodic-tick-button");

	const addressesOpenState = new Map();
	const swarmAddressesOpenState = new Map();

	// Builds/patches the <details> "N address(es)" widget in place, preserving
	// open/closed state and diffing rows so scroll position survives re-renders.
	function syncAddressesCell(cell, rowId, addresses, openStateMap) {
		if (addresses.length === 0) {
			cell.replaceChildren();
			return;
		}
		let details = cell.querySelector("details");
		let summary, list;
		if (!details) {
			details = document.createElement("details");
			details.open = openStateMap.get(rowId) ?? false;
			details.addEventListener("toggle", () => openStateMap.set(rowId, details.open));
			summary = document.createElement("summary");
			details.appendChild(summary);
			list = document.createElement("ul");
			details.appendChild(list);
			cell.replaceChildren(details);
		} else {
			summary = details.querySelector("summary");
			list = details.querySelector("ul");
		}
		summary.textContent = `${addresses.length} address${addresses.length === 1 ? "" : "es"}`;
		syncList(
			list,
			addresses,
			(address) => address,
			(address) => {
				const item = document.createElement("li");
				item.textContent = address;
				return item;
			},
			() => {}
		);
	}

	function createAddressesCell(rowId, addresses, openStateMap) {
		const cell = document.createElement("td");
		cell.dataset.label = "Addresses";
		syncAddressesCell(cell, rowId, addresses, openStateMap);
		return cell;
	}

	// Inserted between the "Groups" and "Main" cells (indices 0-1), so it relies
	// on those already being in place and appends itself at index 2 on create.
	function syncConnectedCell(row, peer) {
		let cell = row.children[2];
		let dot;
		if (!cell) {
			cell = document.createElement("td");
			cell.dataset.label = "Connected";
			dot = document.createElement("span");
			cell.appendChild(dot);
			row.appendChild(cell);
		} else {
			dot = cell.querySelector("span");
		}
		dot.className = `status-dot${peer.connected ? " connected" : ""}`;
		dot.title = peer.connected ? "Connected" : "Not connected";
	}

	function createClearAddressesCell(peerId) {
		const cell = document.createElement("td");
		const button = document.createElement("md-text-button");
		button.textContent = "Clear discovered addresses";
		button.addEventListener("click", () =>
			report(output, async () => {
				await api.call("realm.clearPeerAddresses", { peerId });
				refreshPeers();
			})
		);
		cell.appendChild(button);
		return cell;
	}

	function createDeleteCell(peerId) {
		const cell = document.createElement("td");
		const button = document.createElement("md-text-button");
		button.textContent = "Delete";
		button.addEventListener("click", () =>
			report(output, async () => {
				if (!confirm(`Delete peer "${peerId}"?`)) return;
				await api.call("realm.deletePeer", { peerId });
				refreshPeers();
			})
		);
		cell.appendChild(button);
		return cell;
	}

	// Delete is only meaningful for disconnected peers (a connected one would
	// just get re-added on its next message), so hide it otherwise.
	function syncDeleteCell(cell, peer) {
		cell.style.display = peer.connected ? "none" : "";
	}

	function renderPeers(peers) {
		peersCount.textContent = peers.length;
		syncList(
			peersBody,
			peers,
			(peer) => peer.id,
			(peer) => {
				const row = document.createElement("tr");
				syncCells(row, peerLeadingCells(peer));
				syncConnectedCell(row, peer);
				syncCells(row, peerTrailingCells(peer), 3);
				row.appendChild(createAddressesCell(peer.id, peer.addresses || [], addressesOpenState));
				row.appendChild(createClearAddressesCell(peer.id));
				const deleteCell = createDeleteCell(peer.id);
				syncDeleteCell(deleteCell, peer);
				row.appendChild(deleteCell);
				return row;
			},
			(row, peer) => {
				syncCells(row, peerLeadingCells(peer));
				syncConnectedCell(row, peer);
				syncCells(row, peerTrailingCells(peer), 3);
				syncAddressesCell(row.children[row.children.length - 3], peer.id, peer.addresses || [], addressesOpenState);
				syncDeleteCell(row.lastElementChild, peer);
			}
		);
	}

	function peerLeadingCells(peer) {
		return [
			["ID", formatPeerLabel(peer)],
			["Groups", (peer.groupNames || []).join(", ")],
		];
	}

	function peerTrailingCells(peer) {
		return [
			["Connected via", (peer.connectedAddresses || []).join(", ")],
			["Main", peer.mainPeer ? "yes" : "no"],
			["Relay Service", peer.relayServiceEnabled ? "yes" : "no"],
			["Version", peer.version || ""],
			["Last Seen", peer.lastSeen ? new Date(peer.lastSeen).toLocaleString() : ""],
		];
	}

	function renderSwarm(peers) {
		swarmCount.textContent = peers.length;
		syncList(
			swarmBody,
			peers,
			(peer) => peer.id,
			(peer) => {
				const row = document.createElement("tr");
				syncCells(row, [["ID", peer.id]]);
				row.appendChild(createAddressesCell(peer.id, peer.addresses || [], swarmAddressesOpenState));
				return row;
			},
			(row, peer) => {
				syncCells(row, [["ID", peer.id]]);
				syncAddressesCell(row.lastElementChild, peer.id, peer.addresses || [], swarmAddressesOpenState);
			}
		);
	}

	function refreshPeers() {
		api.call("realm.listPeers").then((result) => {
			renderPeers(result.peers);
			if (onPeersUpdate) onPeersUpdate(result.peers);
		});
		api.call("realm.listSwarmPeers").then((result) => renderSwarm(result.peers));
	}

	clearAllButton.addEventListener("click", () =>
		report(output, async () => {
			await api.call("realm.clearAllPeerAddresses");
			refreshPeers();
		})
	);

	forcePeriodicTickButton.addEventListener("click", () =>
		report(output, async () => {
			await api.call("realm.forcePeriodicTick");
			refreshPeers();
		})
	);

	refreshPeers();
	setInterval(refreshPeers, PEERS_POLL_INTERVAL_MS);
}

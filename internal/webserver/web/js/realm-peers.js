import { formatPeerLabel, syncList, syncCells } from "./util.js";

const PEERS_POLL_INTERVAL_MS = 5000;

// Wires the Peers/Swarm tables. onPeersUpdate (optional) gets the latest
// peers list on every refresh — used by the permissions subtab.
export function initRealmPeers(api, onPeersUpdate) {
	const peersBody = document.getElementById("realm-peers-tbody");
	const peersCount = document.getElementById("realm-peers-count");
	const swarmBody = document.getElementById("realm-swarm-tbody");
	const swarmCount = document.getElementById("realm-swarm-count");

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

	function renderPeers(peers) {
		peersCount.textContent = peers.length;
		syncList(
			peersBody,
			peers,
			(peer) => peer.id,
			(peer) => {
				const row = document.createElement("tr");
				syncCells(row, peerCells(peer));
				row.appendChild(createAddressesCell(peer.id, peer.addresses || [], addressesOpenState));
				return row;
			},
			(row, peer) => {
				syncCells(row, peerCells(peer));
				syncAddressesCell(row.lastElementChild, peer.id, peer.addresses || [], addressesOpenState);
			}
		);
	}

	function peerCells(peer) {
		return [
			["ID", formatPeerLabel(peer)],
			["Groups", (peer.groupNames || []).join(", ")],
			["Connected", peer.connected ? "yes" : "no"],
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

	refreshPeers();
	setInterval(refreshPeers, PEERS_POLL_INTERVAL_MS);
}

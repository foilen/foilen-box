import { formatPeerLabel } from "./util.js";

const PEERS_POLL_INTERVAL_MS = 5000;

// initRealmPeers wires the Peers and Swarm tables on the Realm main
// subtab, and keeps the peer <select> used by the notifications subtab
// (owned there, populated here since the peer list is fetched here) up to
// date. onPeersUpdate, if given, is called with the latest peers list on
// every refresh (used by the permissions subtab).
export function initRealmPeers(api, onPeersUpdate) {
	const peersBody = document.getElementById("realm-peers-tbody");
	const peersCount = document.getElementById("realm-peers-count");
	const swarmBody = document.getElementById("realm-swarm-tbody");
	const swarmCount = document.getElementById("realm-swarm-count");
	const notificationToSelect = document.getElementById("realm-notification-to");

	const addressesOpenState = new Map();
	const swarmAddressesOpenState = new Map();

	// renderAddressesCell builds the "<details><summary>N address(es)</summary><ul>...</ul></details>"
	// widget shared by the peers and swarm tables, remembering per-row
	// open/closed state across re-renders in openStateMap.
	function renderAddressesCell(rowId, addresses, openStateMap) {
		const cell = document.createElement("td");
		cell.dataset.label = "Addresses";
		if (addresses.length === 0) {
			return cell;
		}
		const details = document.createElement("details");
		details.open = openStateMap.get(rowId) ?? false;
		details.addEventListener("toggle", () => {
			openStateMap.set(rowId, details.open);
		});
		const summary = document.createElement("summary");
		summary.textContent = `${addresses.length} address${addresses.length === 1 ? "" : "es"}`;
		details.appendChild(summary);
		const list = document.createElement("ul");
		for (const address of addresses) {
			const item = document.createElement("li");
			item.textContent = address;
			list.appendChild(item);
		}
		details.appendChild(list);
		cell.appendChild(details);
		return cell;
	}

	function renderPeerOptions(select, peers) {
		const previous = select.value;
		select.innerHTML = "";
		for (const peer of peers) {
			const option = document.createElement("md-select-option");
			option.value = peer.id;
			const headline = document.createElement("div");
			headline.slot = "headline";
			headline.textContent = `${formatPeerLabel(peer)}${(peer.groupNames || []).length ? " (" + peer.groupNames.join(", ") + ")" : ""}`;
			option.appendChild(headline);
			select.appendChild(option);
		}
		if (previous && peers.some((peer) => peer.id === previous)) {
			select.value = previous;
		}
	}

	function renderPeers(peers) {
		peersBody.innerHTML = "";
		peersCount.textContent = peers.length;
		renderPeerOptions(notificationToSelect, peers);
		for (const peer of peers) {
			const row = document.createElement("tr");
			const cells = [
				["ID", formatPeerLabel(peer)],
				["Groups", (peer.groupNames || []).join(", ")],
				["Connected", peer.connected ? "yes" : "no"],
				["Relay Service", peer.relayServiceEnabled ? "yes" : "no"],
				["Version", peer.version || ""],
				["Last Seen", peer.lastSeen ? new Date(peer.lastSeen).toLocaleString() : ""],
			];
			for (const [label, value] of cells) {
				const cell = document.createElement("td");
				cell.textContent = value;
				cell.dataset.label = label;
				row.appendChild(cell);
			}

			row.appendChild(renderAddressesCell(peer.id, peer.addresses || [], addressesOpenState));

			peersBody.appendChild(row);
		}
	}

	function renderSwarm(peers) {
		swarmBody.innerHTML = "";
		swarmCount.textContent = peers.length;
		for (const peer of peers) {
			const row = document.createElement("tr");

			const idCell = document.createElement("td");
			idCell.textContent = peer.id;
			idCell.dataset.label = "ID";
			row.appendChild(idCell);

			row.appendChild(renderAddressesCell(peer.id, peer.addresses || [], swarmAddressesOpenState));

			swarmBody.appendChild(row);
		}
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

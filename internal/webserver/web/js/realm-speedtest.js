import { report, formatPeerLabel, syncList } from "./util.js";

// initRealmSpeedtest wires the Speed Test subtab: a checkable peer list (fed
// by the same known-peers list as Permissions/Specs/Scripts/Services, via
// onPeersUpdate) and a "Run Speed Test" button that runs a download/upload
// throughput test (see internal/speedtest) against each checked peer, one
// after the other, filling in a result row as each one completes.
export function initRealmSpeedtest(api, output) {
	const peersBody = document.getElementById("realm-speedtest-peers-tbody");
	const selectAllCheckbox = document.getElementById("realm-speedtest-select-all");
	const runButton = document.getElementById("realm-speedtest-run-button");
	const progress = document.getElementById("realm-speedtest-progress");
	const resultsBody = document.getElementById("realm-speedtest-results-tbody");

	let peers = [];
	let ownPeerId = "";

	function selectedPeerIds() {
		return Array.from(peersBody.querySelectorAll("md-checkbox"))
			.filter((cb) => cb.checked)
			.map((cb) => cb.dataset.peerId);
	}

	function syncConnectedCell(row, peer) {
		let cell = row.children[2];
		let dot;
		if (!cell) {
			cell = document.createElement("td");
			dot = document.createElement("span");
			cell.appendChild(dot);
			row.appendChild(cell);
		} else {
			dot = cell.querySelector("span");
		}
		dot.className = `status-dot${peer.connected ? " connected" : ""}`;
		dot.title = peer.connected ? "Connected" : "Not connected";
	}

	function renderPeers() {
		syncList(
			peersBody,
			peers.filter((peer) => peer.id !== ownPeerId),
			(peer) => peer.id,
			(peer) => {
				const row = document.createElement("tr");

				const checkCell = document.createElement("td");
				const checkbox = document.createElement("md-checkbox");
				checkbox.dataset.peerId = peer.id;
				checkbox.addEventListener("change", updateSelectAllState);
				checkCell.appendChild(checkbox);
				row.appendChild(checkCell);

				const peerCell = document.createElement("td");
				peerCell.textContent = formatPeerLabel(peer);
				row.appendChild(peerCell);

				syncConnectedCell(row, peer);

				return row;
			},
			(row, peer) => {
				const peerCell = row.children[1];
				const label = formatPeerLabel(peer);
				if (peerCell.textContent !== label) peerCell.textContent = label;
				syncConnectedCell(row, peer);
			}
		);
		updateSelectAllState();
	}

	function updateSelectAllState() {
		const checkboxes = Array.from(peersBody.querySelectorAll("md-checkbox"));
		selectAllCheckbox.checked = checkboxes.length > 0 && checkboxes.every((cb) => cb.checked);
	}

	function addResultRow(peer) {
		const row = document.createElement("tr");
		const cells = [
			["Peer", formatPeerLabel(peer)],
			["Status", "Running..."],
			["Download", ""],
			["Upload", ""],
		];
		for (const [label, value] of cells) {
			const cell = document.createElement("td");
			cell.textContent = value;
			cell.dataset.label = label;
			row.appendChild(cell);
		}
		resultsBody.appendChild(row);
		return row;
	}

	function updateResultRow(row, result) {
		const [, statusCell, downloadCell, uploadCell] = row.children;
		if (result.error) {
			statusCell.textContent = "Failed";
			downloadCell.textContent = result.error;
			uploadCell.textContent = "";
		} else {
			statusCell.textContent = "Done";
			downloadCell.textContent = `${result.downloadMbps.toFixed(2)} Mbps`;
			uploadCell.textContent = `${result.uploadMbps.toFixed(2)} Mbps`;
		}
	}

	selectAllCheckbox.addEventListener("change", () => {
		const checked = selectAllCheckbox.checked;
		for (const checkbox of peersBody.querySelectorAll("md-checkbox")) checkbox.checked = checked;
	});

	runButton.addEventListener("click", () =>
		report(output, async () => {
			const peerIds = selectedPeerIds();
			if (peerIds.length === 0) {
				output.textContent = "Please select at least one peer.";
				return;
			}
			console.log("[action] run speed test", { peerIds });
			runButton.disabled = true;
			progress.classList.remove("hidden");
			resultsBody.innerHTML = "";
			try {
				for (const peerId of peerIds) {
					const peer = peers.find((p) => p.id === peerId) || { id: peerId };
					const row = addResultRow(peer);
					const result = await api.call("realm.runSpeedTest", { peerId });
					updateResultRow(row, result);
				}
			} finally {
				runButton.disabled = false;
				progress.classList.add("hidden");
			}
		})
	);

	return {
		onPeersUpdate: (updatedPeers) => {
			peers = updatedPeers;
			renderPeers();
		},
		onConfigUpdate: (cfg) => {
			ownPeerId = cfg.peerId || "";
			renderPeers();
		},
	};
}

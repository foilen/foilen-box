import mermaid from "../vendor-js/mermaid/entry.mjs";
import { report, formatGroupLabel, formatKnownPeerLabel, syncList } from "./util.js";

const POLL_INTERVAL_MS = 5000;

// Piggybacks on each group's existing "common" realmmap (every member
// already subscribes to it — see realm/features/maps.Feature.onPeerAvailable
// — so there's no dedicated map to create/delete) under a
// "groupTroubleshooting/" key prefix, mirroring
// internal/grouptroubleshooting's Go-side key layout.
const STORE_NAME = "common";
const EXPIRATION_KEY = "groupTroubleshooting/expiration";
const START_KEY = "groupTroubleshooting/start";
const CONNECTIONS_KEY_RE = /^groupTroubleshooting\/peer\/(.+)\/connections$/;
const STARTED_KEY_RE = /^groupTroubleshooting\/peer\/(.+)\/started$/;

mermaid.initialize({
	startOnLoad: false,
	theme: window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "default",
});

// Sanitizes a peer id into a syntactically-safe mermaid node id.
function nodeId(peerId) {
	return "p_" + peerId.replace(/[^a-zA-Z0-9]/g, "_");
}

function escapeLabel(text) {
	return String(text).replace(/"/g, "'");
}

// Parses the "common" map's groupTroubleshooting/* entries into {
// expiresAtUnixMillis | null, startAtUnixMillis | null, connectionsByPeer:
// Map<ownerPeerId, [{remotePeerId, address}]>, startedByPeer: Map<peerId,
// {startAtUnixMillis, startedAtUnixMillis}> }, ignoring every other entry
// (specs/*, scripts/*, services/*, peers/*, ...) also living in that store.
function parseEntries(entries) {
	let expiresAtUnixMillis = null;
	let startAtUnixMillis = null;
	const connectionsByPeer = new Map();
	const startedByPeer = new Map();
	for (const [key, entry] of Object.entries(entries)) {
		if (key === EXPIRATION_KEY) {
			try {
				expiresAtUnixMillis = JSON.parse(entry.value).expiresAtUnixMillis || null;
			} catch {
				// ignore malformed entry
			}
			continue;
		}
		if (key === START_KEY) {
			try {
				startAtUnixMillis = JSON.parse(entry.value).startAtUnixMillis || null;
			} catch {
				// ignore malformed entry
			}
			continue;
		}
		const connMatch = CONNECTIONS_KEY_RE.exec(key);
		if (connMatch) {
			try {
				connectionsByPeer.set(connMatch[1], JSON.parse(entry.value) || []);
			} catch {
				// ignore malformed entry
			}
			continue;
		}
		const startedMatch = STARTED_KEY_RE.exec(key);
		if (startedMatch) {
			try {
				startedByPeer.set(startedMatch[1], JSON.parse(entry.value));
			} catch {
				// ignore malformed entry
			}
		}
	}
	return { expiresAtUnixMillis, startAtUnixMillis, connectionsByPeer, startedByPeer };
}

// Builds [{peerId, latencyMillis}] rows, one per peer that has already
// reported a started entry for the session identified by startAtUnixMillis
// (a started entry left over from an earlier session is ignored), sorted by
// ascending propagation latency.
function buildLatencyRows(startAtUnixMillis, startedByPeer) {
	if (!startAtUnixMillis) return [];
	const rows = [];
	for (const [peerId, started] of startedByPeer) {
		if (!started || started.startAtUnixMillis !== startAtUnixMillis) continue;
		rows.push({ peerId, latencyMillis: started.startedAtUnixMillis - startAtUnixMillis });
	}
	rows.sort((a, b) => a.latencyMillis - b.latencyMillis);
	return rows;
}

// Builds mermaid flowchart source: one node per group member, colored gray
// when it has no connections and no realmmap entry of its own, blue when
// something connects to it but it hasn't published its own entry yet, green
// once we have its entry (i.e. connectionsByPeer.has(peerId)); a solid blue
// edge for a direct connection, a dashed yellow edge for a relayed one
// (address contains "/p2p-circuit").
function buildDiagram(groupPeers, connectionsByPeer, knownPeers) {
	const lines = ["graph LR"];
	const nodeIds = new Set();
	const peerIdById = new Map();
	const incomingTargets = new Set();

	const outgoingCounts = new Map();
	const incomingCounts = new Map();
	for (const [ownerId, conns] of connectionsByPeer) {
		outgoingCounts.set(ownerId, conns.length);
		for (const c of conns) {
			incomingCounts.set(c.remotePeerId, (incomingCounts.get(c.remotePeerId) || 0) + 1);
		}
	}

	function ensureNode(peerId) {
		const id = nodeId(peerId);
		if (!nodeIds.has(id)) {
			nodeIds.add(id);
			peerIdById.set(id, peerId);
			const label = `${formatKnownPeerLabel(knownPeers, peerId)} (${outgoingCounts.get(peerId) || 0}/${incomingCounts.get(peerId) || 0})`;
			lines.push(`  ${id}["${escapeLabel(label)}"]`);
		}
		return id;
	}

	for (const peerId of groupPeers) ensureNode(peerId);

	const edgeColors = [];
	for (const [ownerId, conns] of connectionsByPeer) {
		for (const c of conns) {
			const fromId = ensureNode(ownerId);
			const toId = ensureNode(c.remotePeerId);
			const relay = c.address.includes("/p2p-circuit");
			const arrow = relay ? "-.->" : "-->";
			lines.push(`  ${fromId} ${arrow}|"${escapeLabel(c.address)}"| ${toId}`);
			edgeColors.push(relay ? "#f9a825" : "#1e88e5");
			incomingTargets.add(toId);
		}
	}

	lines.push("  classDef grayNode fill:#9e9e9e,stroke:#616161,color:#fff;");
	lines.push("  classDef blueNode fill:#1e88e5,stroke:#1565c0,color:#fff;");
	lines.push("  classDef greenNode fill:#43a047,stroke:#2e7d32,color:#fff;");
	for (const id of nodeIds) {
		const peerId = peerIdById.get(id);
		const hasInfo = connectionsByPeer.has(peerId);
		const nodeClass = hasInfo ? "greenNode" : incomingTargets.has(id) ? "blueNode" : "grayNode";
		lines.push(`  class ${id} ${nodeClass};`);
	}
	edgeColors.forEach((color, i) => lines.push(`  linkStyle ${i} stroke:${color},stroke-width:2px;`));

	return lines.join("\n");
}

// Wires the "Group Troubleshooting" subtab: a group selector, a "Check
// Group" button that starts a fixed 10-minute session (internal/
// grouptroubleshooting.Manager.StartSession), and a mermaid diagram that
// keeps refreshing from the group's "common" realmmap while the session is
// active. See docs/pattern-encrypted-realmmap-feature.md.
export function initRealmGroupTroubleshooting(api, output) {
	const groupSelect = document.getElementById("group-troubleshooting-group-select");
	const startButton = document.getElementById("group-troubleshooting-start-button");
	const statusEl = document.getElementById("group-troubleshooting-status");
	const diagramEl = document.getElementById("group-troubleshooting-diagram");
	const freezeCheckbox = document.getElementById("group-troubleshooting-freeze");
	const latencyTbody = document.getElementById("group-troubleshooting-latency-tbody");

	let groups = [];
	let peers = [];
	let selectedGroupId = null;
	let renderCounter = 0;

	// One entry per group id ever viewed this session, so switching groups
	// and back doesn't lose the last-rendered diagram of a finished run.
	const sessionByGroup = new Map(); // groupId -> { running, finished, expiresAtUnixMillis, entries }

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
		select.value = previousValue || (entries.length > 0 ? entries[0][0] : "");
	}

	function groupPeerIds(groupId) {
		const group = groups.find((g) => g.id === groupId);
		if (!group) return [];
		const ids = peers.filter((p) => (p.groupNames || []).includes(group.name)).map((p) => p.id);
		if (group.selfPeerId && !ids.includes(group.selfPeerId)) ids.push(group.selfPeerId);
		return ids;
	}

	async function renderDiagram(entries) {
		const { startAtUnixMillis, connectionsByPeer, startedByPeer } = parseEntries(entries);
		const definition = buildDiagram(groupPeerIds(selectedGroupId), connectionsByPeer, peers);
		const id = "group-troubleshooting-mermaid-" + renderCounter++;
		try {
			const { svg } = await mermaid.render(id, definition);
			diagramEl.innerHTML = svg;
		} catch (err) {
			diagramEl.textContent = "Failed to render diagram: " + err.message;
		}
		renderLatencyTable(startAtUnixMillis, startedByPeer);
	}

	function renderLatencyTable(startAtUnixMillis, startedByPeer) {
		latencyTbody.innerHTML = "";
		for (const { peerId, latencyMillis } of buildLatencyRows(startAtUnixMillis, startedByPeer)) {
			const row = document.createElement("tr");
			const cells = [
				["Peer", formatKnownPeerLabel(peers, peerId)],
				["RealmMap propagation latency", (latencyMillis / 1000).toFixed(1) + " s"],
			];
			for (const [label, value] of cells) {
				const cell = document.createElement("td");
				cell.textContent = value;
				cell.dataset.label = label;
				row.appendChild(cell);
			}
			latencyTbody.appendChild(row);
		}
	}

	function renderForSelectedGroup() {
		if (freezeCheckbox.checked) return;
		const session = sessionByGroup.get(selectedGroupId);
		if (!session) {
			statusEl.textContent = "";
			diagramEl.innerHTML = "";
			latencyTbody.innerHTML = "";
			startButton.disabled = false;
			return;
		}
		if (session.running) {
			statusEl.textContent = `Group Troubleshooting already running — expires at ${new Date(session.expiresAtUnixMillis).toLocaleString()}`;
			startButton.disabled = true;
		} else if (session.finished) {
			statusEl.textContent = `Group Troubleshooting finished — ended at ${new Date(session.expiresAtUnixMillis).toLocaleString()}`;
			startButton.disabled = false;
		} else {
			statusEl.textContent = "";
			startButton.disabled = false;
		}
		renderDiagram(session.entries);
	}

	async function refresh() {
		if (!selectedGroupId) return;
		const map = await api.call("realm.getMap", { groupId: selectedGroupId, storeName: STORE_NAME });
		const entries = map.entries || {};
		const { expiresAtUnixMillis } = parseEntries(entries);

		// groupTroubleshooting/* entries are never deleted (see
		// internal/grouptroubleshooting.Manager.StartSession) — the expiration
		// timestamp alone says whether a session is running or just finished;
		// with no expiration entry at all, this group has never run one.
		if (expiresAtUnixMillis) {
			sessionByGroup.set(selectedGroupId, {
				running: Date.now() < expiresAtUnixMillis,
				finished: Date.now() >= expiresAtUnixMillis,
				expiresAtUnixMillis,
				entries,
			});
		}

		renderForSelectedGroup();
	}

	freezeCheckbox.addEventListener("change", () => {
		if (!freezeCheckbox.checked) renderForSelectedGroup();
	});

	groupSelect.addEventListener("change", () =>
		report(output, async () => {
			selectedGroupId = groupSelect.value || null;
			console.log("[action] select group troubleshooting group", { groupId: selectedGroupId });
			renderForSelectedGroup();
			await refresh();
		})
	);

	startButton.addEventListener("click", () =>
		report(output, async () => {
			if (!selectedGroupId) {
				output.textContent = "Please select a group.";
				return;
			}
			console.log("[action] start group troubleshooting", { groupId: selectedGroupId });
			await api.call("groupTroubleshooting.start", { groupId: selectedGroupId });
			await refresh();
		})
	);

	setInterval(() => report(output, refresh), POLL_INTERVAL_MS);

	return {
		onConfigUpdate: (cfg) => {
			groups = (cfg.groups || []).map((g) => ({ ...g, selfPeerId: cfg.peerId }));
			syncOptions(groupSelect, groups.map((g) => [g.id, formatGroupLabel(g)]));
			if (!selectedGroupId && groupSelect.value) {
				selectedGroupId = groupSelect.value;
				refresh();
			}
		},
		onPeersUpdate: (updatedPeers) => {
			peers = updatedPeers;
		},
		onSubtabActivated: () => report(output, refresh),
	};
}

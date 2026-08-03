import mermaid from "https://esm.sh/mermaid@11";
import { report, formatGroupLabel, formatKnownPeerLabel, syncList } from "./util.js";

const POLL_INTERVAL_MS = 5000;

// Piggybacks on each group's existing "common" realmmap (every member
// already subscribes to it — see realm/features/maps.Feature.onPeerAvailable
// — so there's no dedicated map to create/delete) under a
// "groupTroubleshooting/" key prefix, mirroring
// internal/grouptroubleshooting's Go-side key layout.
const STORE_NAME = "common";
const EXPIRATION_KEY = "groupTroubleshooting/expiration";
const CONNECTIONS_KEY_RE = /^groupTroubleshooting\/peer\/(.+)\/connections$/;

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
// expiresAtUnixMillis | null, connectionsByPeer: Map<ownerPeerId,
// [{remotePeerId, address}]> }, ignoring every other entry (specs/*,
// scripts/*, services/*, peers/*, ...) also living in that store.
function parseEntries(entries) {
	let expiresAtUnixMillis = null;
	const connectionsByPeer = new Map();
	for (const [key, entry] of Object.entries(entries)) {
		if (key === EXPIRATION_KEY) {
			try {
				expiresAtUnixMillis = JSON.parse(entry.value).expiresAtUnixMillis || null;
			} catch {
				// ignore malformed entry
			}
			continue;
		}
		const match = CONNECTIONS_KEY_RE.exec(key);
		if (!match) continue;
		try {
			connectionsByPeer.set(match[1], JSON.parse(entry.value) || []);
		} catch {
			// ignore malformed entry
		}
	}
	return { expiresAtUnixMillis, connectionsByPeer };
}

// Builds mermaid flowchart source: one gray node per group member, turning
// green when it's an endpoint of at least one connection; a solid blue edge
// for a direct connection, a dashed yellow edge for a relayed one
// (address contains "/p2p-circuit").
function buildDiagram(groupPeers, connectionsByPeer, knownPeers) {
	const lines = ["graph LR"];
	const nodeIds = new Set();
	const activeNodes = new Set();

	function ensureNode(peerId) {
		const id = nodeId(peerId);
		if (!nodeIds.has(id)) {
			nodeIds.add(id);
			lines.push(`  ${id}["${escapeLabel(formatKnownPeerLabel(knownPeers, peerId))}"]`);
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
			activeNodes.add(fromId);
			activeNodes.add(toId);
		}
	}

	lines.push("  classDef grayNode fill:#9e9e9e,stroke:#616161,color:#fff;");
	lines.push("  classDef greenNode fill:#43a047,stroke:#2e7d32,color:#fff;");
	for (const id of nodeIds) {
		lines.push(`  class ${id} ${activeNodes.has(id) ? "greenNode" : "grayNode"};`);
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
		const { connectionsByPeer } = parseEntries(entries);
		const definition = buildDiagram(groupPeerIds(selectedGroupId), connectionsByPeer, peers);
		const id = "group-troubleshooting-mermaid-" + renderCounter++;
		try {
			const { svg } = await mermaid.render(id, definition);
			diagramEl.innerHTML = svg;
		} catch (err) {
			diagramEl.textContent = "Failed to render diagram: " + err.message;
		}
	}

	function renderForSelectedGroup() {
		const session = sessionByGroup.get(selectedGroupId);
		if (!session) {
			statusEl.textContent = "";
			diagramEl.innerHTML = "";
			startButton.disabled = false;
			return;
		}
		if (session.running) {
			statusEl.textContent = `Group Troubleshooting already running — expires at ${new Date(session.expiresAtUnixMillis).toLocaleString()}`;
			startButton.disabled = true;
		} else if (session.finished) {
			statusEl.textContent = "Group Troubleshooting finished";
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

import { report, formatKnownPeerLabel, syncList, syncCells, syncConnectedCell } from "./util.js";

const RUNS_POLL_INTERVAL_MS = 3000;
const RUN_GIVE_UP_MS = 2 * 60 * 1000;

const PEER_SCRIPTS_POLL_INTERVAL_MS = 5000;
const SCRIPTS_STORE_NAME = "common";
const SCRIPTS_KEY_PREFIX = "scripts/";

export function initRealmScripts(api, output, renderConfig) {
	const myScriptsBody = document.getElementById("realm-my-scripts-tbody");
	const myScriptsCount = document.getElementById("realm-scripts-count");
	const newNameInput = document.getElementById("realm-new-script-name");
	const newDescriptionInput = document.getElementById("realm-new-script-description");
	const newCommandInput = document.getElementById("realm-new-script-command");
	const newArgsInput = document.getElementById("realm-new-script-args");
	const newWorkingDirectoryInput = document.getElementById("realm-new-script-working-directory");
	const addButton = document.getElementById("realm-add-script-button");

	const peerScriptsBody = document.getElementById("realm-peer-scripts-tbody");

	const pendingRuns = new Map();

	let peerScripts = [];
	let knownPeers = [];
	let groups = [];

	function isPeerConnected(peerId) {
		return knownPeers.find((p) => p.id === peerId)?.connected ?? false;
	}

	function scriptCells(script) {
		return [
			["Name", script.name],
			["Description", script.description || ""],
			["Command", script.command],
			["Args", (script.args || []).join(" ")],
			["Working Directory", script.workingDirectory || ""],
		];
	}

	function renderMyScripts(cfg) {
		const scripts = cfg.scripts || [];
		myScriptsCount.textContent = scripts.length;
		syncList(
			myScriptsBody,
			scripts,
			(script) => script.name,
			(script) => {
				const row = document.createElement("tr");
				syncCells(row, scriptCells(script));

				const deleteCell = document.createElement("td");
				const deleteButton = document.createElement("md-text-button");
				deleteButton.textContent = "Delete";
				deleteButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] delete script", { name: script.name });
						if (!confirm(`Delete script "${script.name}"?`)) return;
						renderConfig(await api.call("realm.deleteScript", { name: script.name }));
					})
				);
				deleteCell.appendChild(deleteButton);
				row.appendChild(deleteCell);

				return row;
			},
			(row, script) => syncCells(row, scriptCells(script))
		);
	}

	addButton.addEventListener("click", () =>
		report(output, async () => {
			const name = newNameInput.value.trim();
			const description = newDescriptionInput.value.trim();
			const command = newCommandInput.value.trim();
			const args = newArgsInput.value.trim() ? newArgsInput.value.trim().split(/\s+/) : [];
			const workingDirectory = newWorkingDirectoryInput.value.trim();
			console.log("[action] add script", { name, command, args, workingDirectory });
			if (!name || !command) {
				output.textContent = "Please enter both a name and a command.";
				return;
			}
			renderConfig(await api.call("realm.addScript", { name, description, command, args, workingDirectory }));
			newNameInput.value = "";
			newDescriptionInput.value = "";
			newCommandInput.value = "";
			newArgsInput.value = "";
			newWorkingDirectoryInput.value = "";
			output.textContent = `Script "${name}" added.`;
		})
	);

	function statusLabel(run) {
		if (!run) return "Started";
		if (run.status === "completed") return `Completed (exit ${run.exitCode})`;
		if (run.status === "failed") return `Failed (exit ${run.exitCode}${run.error ? ": " + run.error : ""})`;
		return "Started";
	}

	function pollRuns() {
		if (pendingRuns.size === 0) return;
		api.call("realm.listScriptRuns").then((result) => {
			const runsById = new Map((result.runs || []).map((r) => [r.runId, r]));
			const now = Date.now();
			for (const [runId, entry] of pendingRuns) {
				const run = runsById.get(runId);
				if (run && run.status !== "started") {
					entry.cell.textContent = statusLabel(run);
					pendingRuns.delete(runId);
					continue;
				}
				if (now - entry.startedAt > RUN_GIVE_UP_MS) {
					entry.cell.textContent = "Started (no confirmation yet)";
					pendingRuns.delete(runId);
				}
			}
		});
	}

	function peerScriptCells(script) {
		return [
			["Peer", formatKnownPeerLabel(knownPeers, script.peerId)],
			["Name", script.name],
			["Description", script.description || ""],
		];
	}

	function renderPeerScriptsTable() {
		syncList(
			peerScriptsBody,
			peerScripts,
			(script) => `${script.peerId}|${script.name}`,
			(script) => {
				const row = document.createElement("tr");
				syncCells(row, peerScriptCells(script));
				syncConnectedCell(row, isPeerConnected(script.peerId));

				const statusCell = document.createElement("td");
				statusCell.dataset.label = "Status";
				row.appendChild(statusCell);

				const executeCell = document.createElement("td");
				const executeButton = document.createElement("md-text-button");
				executeButton.textContent = "Execute";
				executeButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] run peer script", { peerId: script.peerId, name: script.name });
						const result = await api.call("realm.runPeerScript", { peerId: script.peerId, name: script.name });
						statusCell.textContent = "Started";
						pendingRuns.set(result.runId, { cell: statusCell, startedAt: Date.now() });
						output.textContent = `Started "${script.name}" on ${script.peerId}.`;
					})
				);
				executeCell.appendChild(executeButton);
				row.appendChild(executeCell);

				return row;
			},
			(row, script) => {
				syncCells(row, peerScriptCells(script));
				syncConnectedCell(row, isPeerConnected(script.peerId));
			}
		);
	}

	async function refreshPeerScripts() {
		const result = [];
		for (const group of groups) {
			const map = await api.call("realm.getMap", { groupId: group.id, storeName: SCRIPTS_STORE_NAME });
			for (const [key, entry] of Object.entries(map.entries || {})) {
				if (!key.startsWith(SCRIPTS_KEY_PREFIX)) continue;
				const rest = key.slice(SCRIPTS_KEY_PREFIX.length);
				const slash = rest.indexOf("/");
				if (slash < 0) continue;
				const peerId = rest.slice(0, slash);
				let parsed;
				try {
					parsed = JSON.parse(entry.value);
				} catch {
					continue;
				}
				if (result.some((sc) => sc.peerId === peerId && sc.name === parsed.name)) continue;
				result.push({ peerId, ...parsed });
			}
		}
		peerScripts = result;
		renderPeerScriptsTable();
	}

	refreshPeerScripts();
	setInterval(refreshPeerScripts, PEER_SCRIPTS_POLL_INTERVAL_MS);
	setInterval(pollRuns, RUNS_POLL_INTERVAL_MS);

	return {
		renderMyScripts,
		onPeersUpdate: (peers) => {
			knownPeers = peers;
			renderPeerScriptsTable();
		},
		onConfigUpdate: (cfg) => {
			groups = cfg.groups || [];
			refreshPeerScripts();
		},
	};
}

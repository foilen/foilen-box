import { report, formatKnownPeerLabel, syncList, syncCells } from "./util.js";

const RUNS_POLL_INTERVAL_MS = 3000;
// How long to keep polling for a completion before giving up on a run and
// leaving its row as "no confirmation" — matches the backend's own
// best-effort, no-retry delivery of the completion push.
const RUN_GIVE_UP_MS = 2 * 60 * 1000;

const PEER_SCRIPTS_POLL_INTERVAL_MS = 5000;
const SCRIPTS_STORE_NAME = "common";
const SCRIPTS_KEY_PREFIX = "scripts/";

// initRealmScripts wires the Scripts subtab: the "My Scripts" CRUD table
// (fed by the same full-config response as Groups/Permissions, so it's
// handed renderConfig the same way initRealmGroups/initRealmPermissions
// are), the "Peer Scripts" list+execute table — which aggregates the
// scripts/{peerId}/{name} entries every peer posts into the "common" store
// of each group it belongs to (see internal/webserver/realm_announce.go) —
// and polls realm.listScriptRuns while any triggered run hasn't been
// confirmed yet.
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

	// pendingRuns: runId -> the <td> status cell to update once
	// realm.listScriptRuns reports an outcome (or we give up).
	const pendingRuns = new Map();

	// peerScripts: [{ peerId, name, description, command, args, workingDirectory }]
	let peerScripts = [];
	let knownPeers = [];
	let groups = [];

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

	// Mirrors syncConnectedCell in realm-services.js: built on first call,
	// patched in place afterward, since the connected state changes on its
	// own poll cycle independent of the peer-scripts data refresh.
	function syncConnectedCell(row, peerId) {
		let cell = row.querySelector('td[data-label="Connected"]');
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
		const connected = knownPeers.find((p) => p.id === peerId)?.connected ?? false;
		dot.className = `status-dot${connected ? " connected" : ""}`;
		dot.title = connected ? "Connected" : "Not connected";
	}

	// The Status cell is intentionally left untouched on update: it's driven
	// by execute-click + pollRuns (via pendingRuns, keyed by the row's own
	// status <td>), not by the peer-scripts data refresh. Keeping the same
	// row (and thus the same <td>) across refreshes — instead of rebuilding
	// it — is what keeps pendingRuns' cell reference valid.
	function renderPeerScriptsTable() {
		syncList(
			peerScriptsBody,
			peerScripts,
			(script) => `${script.peerId}|${script.name}`,
			(script) => {
				const row = document.createElement("tr");
				syncCells(row, peerScriptCells(script));
				syncConnectedCell(row, script.peerId);

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
				syncConnectedCell(row, script.peerId);
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

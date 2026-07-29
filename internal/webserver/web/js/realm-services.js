import { report, formatKnownPeerLabel } from "./util.js";

const PEER_SERVICES_POLL_INTERVAL_MS = 5000;
const SERVICES_STORE_NAME = "common";
const SERVICES_KEY_PREFIX = "services/";

// initRealmServices wires the Services subtab: the "My Services" CRUD table
// (fed by the same full-config response as Scripts, so it's handed
// renderConfig the same way), the local-port scan modal, and the "Peer
// Services" section, which aggregates the services/{peerId}/{name} entries
// every peer posts into the "common" store of each group it belongs to (see
// internal/webserver/realm_announce.go) and lets the user start/stop a local
// proxy tunnel or connect with a native app.
export function initRealmServices(api, output, renderConfig) {
	const myServicesBody = document.getElementById("realm-my-services-tbody");
	const myServicesCount = document.getElementById("realm-services-count");
	const newNameInput = document.getElementById("realm-new-service-name");
	const newDescriptionInput = document.getElementById("realm-new-service-description");
	const newHostnameInput = document.getElementById("realm-new-service-hostname");
	const newTypeSelect = document.getElementById("realm-new-service-type");
	const newPortInput = document.getElementById("realm-new-service-port");
	const addButton = document.getElementById("realm-add-service-button");

	const scanButton = document.getElementById("realm-scan-ports-button");
	const scanModal = document.getElementById("realm-scan-services-modal");
	const scanBody = document.getElementById("realm-scan-services-tbody");
	const scanAddButton = document.getElementById("realm-scan-services-add-button");
	const scanCloseButton = document.getElementById("realm-scan-services-close-button");

	const peerServicesBody = document.getElementById("realm-peer-services-tbody");

	// peerServices: [{ peerId, name, description, hostname, type, port }]
	let peerServices = [];
	// activeProxies: "peerId|name" -> localPort
	let activeProxies = new Map();
	let knownPeers = [];
	let groups = [];
	let ownPeerId = "";

	function renderMyServices(cfg) {
		const services = cfg.services || [];
		myServicesCount.textContent = services.length;
		myServicesBody.innerHTML = "";
		for (const service of services) {
			const row = document.createElement("tr");
			const cells = [
				["Name", service.name],
				["Description", service.description || ""],
				["Hostname", service.hostname],
				["Type", service.type],
				["Port", service.port],
			];
			for (const [label, value] of cells) {
				const cell = document.createElement("td");
				cell.textContent = value;
				cell.dataset.label = label;
				row.appendChild(cell);
			}

			const deleteCell = document.createElement("td");
			const deleteButton = document.createElement("md-text-button");
			deleteButton.textContent = "Delete";
			deleteButton.addEventListener("click", () =>
				report(output, async () => {
					console.log("[action] delete service", { name: service.name });
					if (!confirm(`Delete service "${service.name}"?`)) return;
					renderConfig(await api.call("realm.deleteService", { name: service.name }));
				})
			);
			deleteCell.appendChild(deleteButton);
			row.appendChild(deleteCell);

			myServicesBody.appendChild(row);
		}
	}

	addButton.addEventListener("click", () =>
		report(output, async () => {
			const name = newNameInput.value.trim();
			const description = newDescriptionInput.value.trim();
			const hostname = newHostnameInput.value.trim();
			const type = newTypeSelect.value;
			const port = parseInt(newPortInput.value, 10);
			console.log("[action] add service", { name, hostname, type, port });
			if (!name || !hostname || !port) {
				output.textContent = "Please enter a name, a hostname, and a port.";
				return;
			}
			renderConfig(await api.call("realm.addService", { name, description, hostname, type, port }));
			newNameInput.value = "";
			newDescriptionInput.value = "";
			newPortInput.value = "";
			output.textContent = `Service "${name}" added.`;
		})
	);

	scanButton.addEventListener("click", () =>
		report(output, async () => {
			console.log("[action] scan local ports");
			const result = await api.call("realm.scanLocalPorts");
			scanBody.innerHTML = "";
			for (const r of result.results || []) {
				const row = document.createElement("tr");

				const checkCell = document.createElement("td");
				const checkbox = document.createElement("md-checkbox");
				checkbox.checked = r.open;
				checkbox.dataset.port = r.port;
				checkbox.dataset.name = r.guessedName;
				checkbox.dataset.type = r.guessedType;
				checkCell.appendChild(checkbox);
				row.appendChild(checkCell);

				const portCell = document.createElement("td");
				portCell.textContent = r.port;
				row.appendChild(portCell);

				const nameCell = document.createElement("td");
				nameCell.textContent = r.guessedName;
				row.appendChild(nameCell);

				const typeCell = document.createElement("td");
				typeCell.textContent = r.guessedType;
				row.appendChild(typeCell);

				const statusCell = document.createElement("td");
				statusCell.textContent = r.unverifiable ? "unverified" : r.open ? "open" : "closed";
				row.appendChild(statusCell);

				scanBody.appendChild(row);
			}
			scanModal.classList.remove("hidden");
		})
	);

	scanCloseButton.addEventListener("click", () => scanModal.classList.add("hidden"));

	scanAddButton.addEventListener("click", () =>
		report(output, async () => {
			const checked = Array.from(scanBody.querySelectorAll("md-checkbox")).filter((cb) => cb.checked);
			console.log("[action] add scanned services", { count: checked.length });
			let cfg = null;
			for (const checkbox of checked) {
				const port = parseInt(checkbox.dataset.port, 10);
				const name = `${checkbox.dataset.name}-${port}`;
				cfg = await api.call("realm.addService", {
					name,
					description: "",
					hostname: "127.0.0.1",
					type: checkbox.dataset.type,
					port,
				});
			}
			if (cfg) renderConfig(cfg);
			scanModal.classList.add("hidden");
			output.textContent = `Added ${checked.length} service(s).`;
		})
	);

	function proxyKey(peerId, name) {
		return `${peerId}|${name}`;
	}

	function statusLabelAndButtons(row, peerId, service) {
		const statusCell = document.createElement("td");
		statusCell.dataset.label = "Proxy";
		const localPort = activeProxies.get(proxyKey(peerId, service.name));
		statusCell.textContent = localPort ? `Running on 127.0.0.1:${localPort}` : "Stopped";
		row.appendChild(statusCell);

		appendConnectedCell(row, peerId);

		const actionsCell = document.createElement("td");

		const toggleButton = document.createElement("md-text-button");
		toggleButton.textContent = localPort ? "Stop" : "Start";
		toggleButton.addEventListener("click", () =>
			report(output, async () => {
				if (activeProxies.get(proxyKey(peerId, service.name))) {
					console.log("[action] stop service proxy", { peerId, name: service.name });
					await api.call("realm.stopServiceProxy", { peerId, name: service.name });
					activeProxies.delete(proxyKey(peerId, service.name));
				} else {
					console.log("[action] start service proxy", { peerId, name: service.name });
					const result = await api.call("realm.startServiceProxy", { peerId, name: service.name });
					activeProxies.set(proxyKey(peerId, service.name), result.localPort);
				}
				renderPeerServicesTable();
			})
		);
		actionsCell.appendChild(toggleButton);

		const connectableTypes = ["http", "https", "ssh", "vnc", "rdp"];
		if (connectableTypes.includes(service.type)) {
			const connectButton = document.createElement("md-text-button");
			connectButton.textContent = "Connect";
			connectButton.addEventListener("click", () =>
				report(output, async () => {
					console.log("[action] connect service", { peerId, name: service.name, type: service.type });
					const result = await api.call("realm.connectService", { peerId, name: service.name, type: service.type });
					activeProxies.set(proxyKey(peerId, service.name), result.localPort);
					renderPeerServicesTable();
					if (!result.opened) {
						output.textContent = result.error
							? `Started proxy on 127.0.0.1:${result.localPort}, but couldn't open an app: ${result.error}`
							: `Started proxy on 127.0.0.1:${result.localPort}. Connect to it manually.`;
					} else {
						output.textContent = `Connected to "${service.name}" on ${peerId}.`;
					}
				})
			);
			actionsCell.appendChild(connectButton);
		}

		row.appendChild(actionsCell);
	}

	function renderPeerServicesTable() {
		peerServicesBody.innerHTML = "";
		for (const service of peerServices) {
			if (service.peerId === ownPeerId) continue;
			const row = document.createElement("tr");
			const cells = [
				["Peer", formatKnownPeerLabel(knownPeers, service.peerId)],
				["Name", service.name],
				["Description", service.description || ""],
				["Type", service.type],
				["Target", `${service.hostname}:${service.port}`],
			];
			for (const [label, value] of cells) {
				const cell = document.createElement("td");
				cell.textContent = value;
				cell.dataset.label = label;
				row.appendChild(cell);
			}
			statusLabelAndButtons(row, service.peerId, service);
			peerServicesBody.appendChild(row);
		}
	}

	function appendConnectedCell(row, peerId) {
		const connected = knownPeers.find((p) => p.id === peerId)?.connected ?? false;
		const cell = document.createElement("td");
		cell.dataset.label = "Connected";
		const dot = document.createElement("span");
		dot.className = `status-dot${connected ? " connected" : ""}`;
		dot.title = connected ? "Connected" : "Not connected";
		cell.appendChild(dot);
		row.appendChild(cell);
	}

	async function refreshPeerServices() {
		const result = [];
		for (const group of groups) {
			const map = await api.call("realm.getMap", { scopeId: group.id, storeName: SERVICES_STORE_NAME });
			for (const [key, entry] of Object.entries(map.entries || {})) {
				if (!key.startsWith(SERVICES_KEY_PREFIX)) continue;
				const rest = key.slice(SERVICES_KEY_PREFIX.length);
				const slash = rest.indexOf("/");
				if (slash < 0) continue;
				const peerId = rest.slice(0, slash);
				let parsed;
				try {
					parsed = JSON.parse(entry.value);
				} catch {
					continue;
				}
				if (result.some((s) => s.peerId === peerId && s.name === parsed.name)) continue;
				result.push({ peerId, ...parsed });
			}
		}
		peerServices = result;
		renderPeerServicesTable();
	}

	async function refreshActiveProxies() {
		const result = await api.call("realm.listActiveProxies");
		activeProxies = new Map((result.proxies || []).map((p) => [proxyKey(p.peerId, p.serviceName), p.localPort]));
		renderPeerServicesTable();
	}

	refreshActiveProxies();
	refreshPeerServices();
	setInterval(refreshPeerServices, PEER_SERVICES_POLL_INTERVAL_MS);

	return {
		renderMyServices,
		onPeersUpdate: (peers) => {
			knownPeers = peers;
			renderPeerServicesTable();
		},
		onConfigUpdate: (cfg) => {
			groups = cfg.groups || [];
			ownPeerId = cfg.peerId || "";
			refreshPeerServices();
		},
	};
}

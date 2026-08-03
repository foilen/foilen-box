import { initSpecTab } from "./spec.js";
import { initTroubleshootingTab } from "./troubleshooting.js";
import { initEarlyTab } from "./early.js";
import { initGpsTab } from "./gps.js";
import { initRealmTab } from "./realm.js";
import { initAndroidConfigTab } from "./android-config.js";
import { initLogsTab } from "./logs.js";
import { initConfigTab } from "./config.js";
import { parseHash, updateHash } from "./hash.js";
import { updateFaviconForTab } from "./favicon.js";

class Api {
	constructor() {
		this.pending = new Map();
		this.nextId = 1;
		this.socket = null;
		this.ready = this._connect();
	}

	_connect() {
		return new Promise((resolveReady) => {
			const proto = location.protocol === "https:" ? "wss:" : "ws:";
			const socket = new WebSocket(`${proto}//${location.host}/ws`);
			this.socket = socket;
			socket.addEventListener("open", () => {
				socket.send(JSON.stringify({ token: window.FOILEN_BOX_TOKEN }));
				resolveReady();
			});
			socket.addEventListener("message", (event) => {
				const msg = JSON.parse(event.data);
				console.log("[ws] received", msg);
				const pending = this.pending.get(msg.id);
				if (!pending) return;
				this.pending.delete(msg.id);
				if (msg.error) {
					pending.reject(new Error(msg.error));
				} else {
					pending.resolve(msg.result);
				}
			});
			socket.addEventListener("error", () => socket.close());
			// Socket can die anytime (tab throttling, server restart); reject
			// in-flight calls and reconnect rather than hang callers forever.
			socket.addEventListener("close", () => {
				if (this.socket !== socket) return;
				for (const pending of this.pending.values()) {
					pending.reject(new Error("connection closed"));
				}
				this.pending.clear();
				this.ready = new Promise((resolve) => setTimeout(() => resolve(this._connect()), 1000));
			});
		});
	}

	async call(action, params) {
		await this.ready;
		const id = String(this.nextId++);
		const message = { id, action, params: params ?? {} };
		console.log("[ws] sending", message);
		return new Promise((resolve, reject) => {
			this.pending.set(id, { resolve, reject });
			this.socket.send(JSON.stringify(message));
		});
	}
}

function activateTab(api, tabId) {
	const button = document.querySelector(`.tab-button[data-tab="${tabId}"]`);
	if (!button) return false;
	document.querySelectorAll(".tab-button").forEach((b) => b.classList.remove("active"));
	document.querySelectorAll(".tab-panel").forEach((p) => p.classList.remove("active"));
	button.classList.add("active");
	document.getElementById(tabId).classList.add("active");
	api.call("config.recordTabLoad", { tabId }).catch(() => {});
	updateFaviconForTab(tabId);
	return true;
}

function initTabs(api) {
	const buttons = document.querySelectorAll(".tab-button");
	buttons.forEach((button) => {
		button.addEventListener("click", (event) => {
			if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
			event.preventDefault();
			console.log("[action] switch tab", { tab: button.dataset.tab });
			activateTab(api, button.dataset.tab);
			updateHash();
		});
	});
}

function reorderButtonsByLoadCount(containers, selector, dataKey, counts) {
	containers.forEach((container) => {
		const buttons = [...container.querySelectorAll(selector)];
		buttons
			.map((b, i) => ({ b, i, count: counts[b.dataset[dataKey]] || 0 }))
			.sort((x, y) => y.count - x.count || x.i - y.i)
			.forEach(({ b }) => container.appendChild(b));
	});
}

async function applyTabLoadOrder(api) {
	let stats;
	try {
		stats = await api.call("config.loadTabStats");
	} catch (err) {
		console.error("[tabs] failed to load tab stats", err);
		return;
	}
	reorderButtonsByLoadCount([document.getElementById("tabs")], "a.tab-button", "tab", stats.tabCounts || {});
	reorderButtonsByLoadCount(document.querySelectorAll(".subtab-nav"), "a.subtab-button", "subtab", stats.subtabCounts || {});
}

const platform = new URLSearchParams(location.search).get("platform");
const isAndroid = platform === "android";

const api = new Api();
await applyTabLoadOrder(api);
initTabs(api);
initSpecTab(api);
initTroubleshootingTab(api);
initEarlyTab(api);
initGpsTab(isAndroid);
initRealmTab(api, isAndroid);
initAndroidConfigTab(isAndroid);
initLogsTab(api);
initConfigTab(api);

if (!isAndroid) {
	document.getElementById("gps-tab-button").remove();
	document.getElementById("android-config-tab-button").remove();
	document.getElementById("top-buffer").remove();
}

const { tab } = parseHash();
if (tab) activateTab(api, tab);
updateHash();

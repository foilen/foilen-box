import { report, formatGroupLabel, formatIdentityLabel, formatPeerLabel, syncList, syncCells } from "./util.js";
import { updateHash } from "./hash.js";

const SMS_POLL_INTERVAL_MS = 5000;
const SMS_PERMISSION_POLL_INTERVAL_MS = 500;
const SMS_PERMISSION_POLL_TIMEOUT_MS = 60000;

// ensureSmsPermission resolves immediately if READ_SMS/SEND_SMS/RECEIVE_SMS
// are already granted (or window.SmsPermissionBridge isn't present, e.g. on
// desktop), otherwise triggers Android's runtime permission dialog
// (SmsPermissionBridge.requestPermission) and polls hasPermission() until
// the user responds or SMS_PERMISSION_POLL_TIMEOUT_MS elapses — there's no
// native callback wired for the dialog's result, so polling (the same
// pattern this file already uses for realmmap sync) is how the enable flow
// finds out.
function ensureSmsPermission() {
	if (typeof window.SmsPermissionBridge === "undefined") return Promise.resolve();
	if (window.SmsPermissionBridge.hasPermission()) return Promise.resolve();
	window.SmsPermissionBridge.requestPermission();
	return new Promise((resolve, reject) => {
		const deadline = Date.now() + SMS_PERMISSION_POLL_TIMEOUT_MS;
		const poll = () => {
			if (window.SmsPermissionBridge.hasPermission()) {
				resolve();
			} else if (Date.now() >= deadline) {
				reject(new Error("SMS permission was not granted"));
			} else {
				setTimeout(poll, SMS_PERMISSION_POLL_INTERVAL_MS);
			}
		};
		setTimeout(poll, SMS_PERMISSION_POLL_INTERVAL_MS);
	});
}

// syncOptions reconciles an <md-outlined-select>'s <md-select-option>
// children in place (keyed by option value) instead of clearing and
// rebuilding them, so the currently-selected option's node survives a
// refresh — same helper as realm-maps.js's own copy.
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
	select.value = previousValue;
}

// initRealmSms wires the SMS subtab: an Android-only collapsible
// configuration section (create/select the "SMS-<suffix>" realmmap this
// device manages), a store picker available on every platform, and a
// conversation list/detail view backed by internal/sms's WebSocket API.
export function initRealmSms(api, output, isAndroid) {
	const configSection = document.getElementById("sms-config");
	if (!isAndroid) {
		configSection.remove();
	}

	const storeSelect = document.getElementById("sms-store-select");
	const storeLocked = document.getElementById("sms-store-locked");
	const conversationsBody = document.getElementById("sms-conversations-tbody");
	const newConversationPhoneInput = document.getElementById("sms-new-conversation-phone");
	const newConversationPeerSelect = document.getElementById("sms-new-conversation-peer");
	const newConversationBodyInput = document.getElementById("sms-new-conversation-body");
	const newConversationSendButton = document.getElementById("sms-new-conversation-send-button");

	const detail = document.getElementById("sms-conversation-detail");
	const conversationTitle = document.getElementById("sms-conversation-title");
	const messagesBody = document.getElementById("sms-messages-tbody");
	const replyPeerSelect = document.getElementById("sms-reply-peer");
	const replyBodyInput = document.getElementById("sms-reply-body");
	const replySendButton = document.getElementById("sms-reply-send-button");
	const closeConversationButton = document.getElementById("sms-close-conversation-button");

	let groups = [];
	let identities = [];
	let peers = [];
	let stores = [];
	let smsCfg = { enabled: false, groupId: "", storeName: "" };
	let selectedStore = null; // { groupId, storeName } | null
	let selectedPhone = null;
	// replyPeerAutoSet guards the reply "Send from peer" default so it's only
	// applied once per opened conversation (from the peer whose device
	// received/sent the messages) rather than clobbering a peer the user
	// picked manually on every poll-driven refresh.
	let replyPeerAutoSet = false;

	function storeLabel(s) {
		return `${s.groupName} / ${s.storeName}`;
	}

	// renderPeerOptions fills select with only the peers listed in
	// enabledPeerIds — "Send from peer" only makes sense for a peer that
	// actually manages the selected store (see enabledPeerIdsForSelectedStore),
	// since only that peer's device can fulfill the resulting create-request
	// (internal/sms.Manager.fulfillCreate).
	function renderPeerOptions(select, enabledPeerIds) {
		syncOptions(
			select,
			peers.filter((p) => enabledPeerIds.has(p.id)).map((p) => [p.id, formatPeerLabel(p)])
		);
	}

	function enabledPeerIdsForSelectedStore() {
		if (!selectedStore) return new Set();
		const store = stores.find((s) => s.groupId === selectedStore.groupId && s.storeName === selectedStore.storeName);
		return new Set((store && store.enabledPeerIds) || []);
	}

	function refreshPeerOptions() {
		const enabledPeerIds = enabledPeerIdsForSelectedStore();
		renderPeerOptions(newConversationPeerSelect, enabledPeerIds);
		renderPeerOptions(replyPeerSelect, enabledPeerIds);
	}

	function renderStoreOptions() {
		syncOptions(storeSelect, stores.map((s) => [`${s.groupId}|${s.storeName}`, storeLabel(s)]));
		if (selectedStore) storeSelect.value = `${selectedStore.groupId}|${selectedStore.storeName}`;
	}

	// syncHash writes the currently viewed store (and, if open, conversation)
	// into location.hash's "extra" segment (see hash.js), so a page refresh
	// or a notification deep link (see cmd/mobile.SmsBridge.showNotification)
	// can restore this exact view instead of always falling back to the
	// first/default store.
	function syncHash() {
		if (!selectedStore) return;
		let extra = `${selectedStore.groupId}|${selectedStore.storeName}`;
		if (selectedPhone) extra += `|${selectedPhone}`;
		updateHash(extra);
	}

	function selectStore(groupId, storeName) {
		selectedStore = { groupId, storeName };
		storeSelect.value = `${groupId}|${storeName}`;
		refreshPeerOptions();
		closeConversation();
	}

	function openConversation(phoneNumber) {
		selectedPhone = phoneNumber;
		conversationTitle.textContent = phoneNumber;
		detail.classList.remove("hidden");
		replyPeerAutoSet = false;
		syncHash();
	}

	// applyReplyPeerDefault prepopulates "Send from peer" with the peer whose
	// device recorded the conversation's most recent incoming message (i.e.
	// the one we got the message from) — or, if the conversation has no
	// incoming message yet (a conversation the user just started), the peer
	// that recorded the most recent message overall.
	function applyReplyPeerDefault(messages) {
		if (replyPeerAutoSet || messages.length === 0) return;
		let defaultPeerId = null;
		for (let i = messages.length - 1; i >= 0; i--) {
			if (messages[i].direction === "incoming") {
				defaultPeerId = messages[i].peerId;
				break;
			}
		}
		if (!defaultPeerId) defaultPeerId = messages[messages.length - 1].peerId;
		if (!defaultPeerId) return;
		replyPeerSelect.value = defaultPeerId;
		replyPeerAutoSet = true;
	}

	function closeConversation() {
		selectedPhone = null;
		detail.classList.add("hidden");
		syncHash();
	}

	function conversationCells(c) {
		return [
			["Phone Number", c.phoneNumber],
			["Last message", c.lastMessageBody || ""],
			["Count", c.messageCount],
			["Updated", c.lastTimestampUnixMillis ? new Date(c.lastTimestampUnixMillis).toLocaleString() : "never"],
		];
	}

	function renderConversations(conversations) {
		syncList(
			conversationsBody,
			conversations,
			(c) => c.phoneNumber,
			(c) => {
				const row = document.createElement("tr");
				syncCells(row, conversationCells(c));
				const actionsCell = document.createElement("td");
				const openButton = document.createElement("md-text-button");
				openButton.textContent = "Open";
				openButton.addEventListener("click", () =>
					report(output, async () => {
						console.log("[action] open sms conversation", { phoneNumber: c.phoneNumber });
						openConversation(c.phoneNumber);
						await refreshSms();
					})
				);
				actionsCell.appendChild(openButton);
				row.appendChild(actionsCell);
				return row;
			},
			(row, c) => {
				syncCells(row, conversationCells(c));
			}
		);
	}

	function messageCells(m) {
		return [
			["Direction", m.direction],
			["Body", m.body],
			["Time", m.timestampUnixMillis ? new Date(m.timestampUnixMillis).toLocaleString() : ""],
		];
	}

	function renderMessages(messages) {
		messagesBody.innerHTML = "";
		for (const m of messages) {
			const row = document.createElement("tr");
			syncCells(row, messageCells(m));
			messagesBody.appendChild(row);
		}
	}

	async function loadStores() {
		const result = await api.call("sms.listStores");
		stores = result.stores || [];
		renderStoreOptions();
		refreshPeerOptions();
		if (isAndroid) renderExistingStoreOptions();
	}

	async function refreshSms() {
		// Re-fetch the store list on every refresh (poll tick, subtab
		// activation) rather than only once at init — a store created
		// locally or synced in from a peer after this page loaded would
		// otherwise never appear, unlike realm-maps.js's own refreshMaps.
		await loadStores();
		if (!selectedStore && stores.length > 0) {
			selectStore(stores[0].groupId, stores[0].storeName);
		}
		if (!selectedStore) {
			conversationsBody.innerHTML = "";
			storeLocked.classList.add("hidden");
			return;
		}
		const result = await api.call("sms.listConversations", selectedStore);
		storeLocked.classList.toggle("hidden", !(result.encrypted && !result.encryptionAvailable));
		renderConversations(result.conversations || []);
		if (selectedPhone) {
			const msgResult = await api.call("sms.listMessages", { ...selectedStore, phoneNumber: selectedPhone });
			const messages = msgResult.messages || [];
			renderMessages(messages);
			applyReplyPeerDefault(messages);
		}
	}

	storeSelect.addEventListener("change", () =>
		report(output, async () => {
			const value = storeSelect.value;
			if (!value) return;
			const [groupId, storeName] = value.split("|");
			console.log("[action] select sms store", { groupId, storeName });
			selectStore(groupId, storeName);
			await refreshSms();
		})
	);

	newConversationSendButton.addEventListener("click", () =>
		report(output, async () => {
			if (!selectedStore) {
				output.textContent = "Please select a store.";
				return;
			}
			const phoneNumber = newConversationPhoneInput.value.trim();
			const peerId = newConversationPeerSelect.value;
			const body = newConversationBodyInput.value.trim();
			console.log("[action] start sms conversation", { ...selectedStore, phoneNumber, peerId });
			if (!phoneNumber || !peerId || !body) {
				output.textContent = "Please choose a peer, phone number, and message.";
				return;
			}
			await api.call("sms.sendMessage", { ...selectedStore, peerId, phoneNumber, body });
			newConversationPhoneInput.value = "";
			newConversationBodyInput.value = "";
			output.textContent = "Message queued for sending.";
			await refreshSms();
		})
	);

	replySendButton.addEventListener("click", () =>
		report(output, async () => {
			if (!selectedStore || !selectedPhone) return;
			const peerId = replyPeerSelect.value;
			const body = replyBodyInput.value.trim();
			console.log("[action] reply sms", { ...selectedStore, phoneNumber: selectedPhone, peerId });
			if (!peerId || !body) {
				output.textContent = "Please choose a peer and enter a message.";
				return;
			}
			await api.call("sms.sendMessage", { ...selectedStore, peerId, phoneNumber: selectedPhone, body });
			replyBodyInput.value = "";
			output.textContent = "Message queued for sending.";
			await refreshSms();
		})
	);

	closeConversationButton.addEventListener("click", () => closeConversation());

	// --- Android-only management configuration ---
	let existingStoreSelect, enabledCheckbox;
	if (isAndroid) {
		const configToggle = document.getElementById("sms-config-toggle");
		const configBody = document.getElementById("sms-config-body");
		enabledCheckbox = document.getElementById("sms-config-enabled");
		const groupSelect = document.getElementById("sms-config-group");
		const suffixInput = document.getElementById("sms-config-suffix");
		const identitySelect = document.getElementById("sms-config-identity");
		const createButton = document.getElementById("sms-config-create-button");
		existingStoreSelect = document.getElementById("sms-config-existing-store");
		const useExistingButton = document.getElementById("sms-config-use-existing-button");

		function setConfigCollapsed(collapsed) {
			configBody.classList.toggle("hidden", collapsed);
			configToggle.textContent = (collapsed ? "▶" : "▼") + " Configuration (Android only)";
		}

		// The checkbox only ever disables/re-enables management of a store
		// that's already been created or selected via the buttons below —
		// there's nothing for it to toggle before that, so it's disabled
		// (rather than silently reverting itself when clicked) until then.
		function updateEnabledCheckboxState() {
			enabledCheckbox.disabled = !(smsCfg.groupId && smsCfg.storeName);
		}

		configToggle.addEventListener("click", () => {
			console.log("[action] toggle sms config visibility");
			setConfigCollapsed(!configBody.classList.contains("hidden"));
		});

		async function afterConfigSaved(result) {
			smsCfg = result;
			enabledCheckbox.checked = result.enabled;
			updateEnabledCheckboxState();
			setConfigCollapsed(result.enabled);
			await loadStores();
			selectStore(result.groupId, result.storeName);
			await refreshSms();
		}

		enabledCheckbox.addEventListener("change", () =>
			report(output, async () => {
				console.log("[action] toggle sms management", { enabled: enabledCheckbox.checked });
				if (enabledCheckbox.checked) {
					try {
						await ensureSmsPermission();
					} catch (err) {
						enabledCheckbox.checked = false;
						throw err;
					}
				}
				const result = await api.call("sms.saveManagementConfig", {
					enabled: enabledCheckbox.checked,
					groupId: smsCfg.groupId,
					storeName: smsCfg.storeName,
					createNew: false,
				});
				smsCfg = result;
				setConfigCollapsed(result.enabled);
			})
		);

		createButton.addEventListener("click", () =>
			report(output, async () => {
				const groupId = groupSelect.value;
				const suffix = suffixInput.value.trim();
				const identityId = identitySelect.value;
				console.log("[action] create sms store", { groupId, suffix, identityId });
				if (!groupId) {
					output.textContent = "Please select a group.";
					return;
				}
				if (!suffix) {
					output.textContent = "Please enter a suffix.";
					return;
				}
				if (!identityId) {
					output.textContent = "Please select an identity to encrypt to — SMS content is never stored unencrypted.";
					return;
				}
				await ensureSmsPermission();
				const result = await api.call("sms.saveManagementConfig", {
					enabled: true,
					groupId,
					createNew: true,
					suffix,
					identityId,
				});
				suffixInput.value = "";
				await afterConfigSaved(result);
				output.textContent = `Now managing SMS via store "${result.storeName}".`;
			})
		);

		useExistingButton.addEventListener("click", () =>
			report(output, async () => {
				const value = existingStoreSelect.value;
				console.log("[action] use existing sms store", { value });
				if (!value) {
					output.textContent = "Please select an existing store.";
					return;
				}
				const [groupId, storeName] = value.split("|");
				await ensureSmsPermission();
				const result = await api.call("sms.saveManagementConfig", {
					enabled: true,
					groupId,
					storeName,
					createNew: false,
				});
				await afterConfigSaved(result);
				output.textContent = `Now managing SMS via store "${result.storeName}".`;
			})
		);

		api.call("sms.loadConfig").then(async (cfg) => {
			smsCfg = cfg;
			enabledCheckbox.checked = cfg.enabled;
			updateEnabledCheckboxState();
			setConfigCollapsed(cfg.enabled);
			await loadStores();
			if (cfg.enabled && stores.some((s) => s.groupId === cfg.groupId && s.storeName === cfg.storeName)) {
				selectStore(cfg.groupId, cfg.storeName);
			} else if (stores.length > 0) {
				selectStore(stores[0].groupId, stores[0].storeName);
			}
			await refreshSms();
		});
	} else {
		refreshSms();
	}

	// Only encrypted stores are offered here — SMS content is never stored
	// unencrypted, so an unencrypted "SMS-*" map (e.g. created by hand via
	// the generic Maps tab) isn't a valid choice for management, even though
	// it can still be viewed read-only via the store picker above.
	function renderExistingStoreOptions() {
		syncOptions(
			existingStoreSelect,
			stores.filter((s) => s.encryptionIdentityId).map((s) => [`${s.groupId}|${s.storeName}`, storeLabel(s)])
		);
	}

	setInterval(() => report(output, refreshSms), SMS_POLL_INTERVAL_MS);

	return {
		onConfigUpdate: (cfg) => {
			groups = cfg.groups || [];
			identities = cfg.identities || [];
			if (isAndroid) {
				syncOptions(document.getElementById("sms-config-group"), groups.map((g) => [g.id, formatGroupLabel(g)]));
				syncOptions(
					document.getElementById("sms-config-identity"),
					identities.map((identity) => [identity.id, formatIdentityLabel(identity)])
				);
			}
		},
		onPeersUpdate: (updatedPeers) => {
			peers = updatedPeers;
			refreshPeerOptions();
		},
		// extra, if given, is "groupId|storeName" or "groupId|storeName|phoneNumber"
		// (see syncHash) — restores the store being viewed and, optionally,
		// reopens a conversation, whether from a page refresh or a
		// notification deep link (cmd/mobile.SmsBridge.showNotification).
		onSubtabActivated: (extra) =>
			report(output, async () => {
				const [groupId, storeName, phoneNumber] = extra ? extra.split("|") : [];
				if (groupId && storeName) {
					await loadStores();
					if (stores.some((s) => s.groupId === groupId && s.storeName === storeName)) {
						selectStore(groupId, storeName);
					}
				}
				await refreshSms();
				if (phoneNumber) {
					openConversation(phoneNumber);
					await refreshSms();
				}
			}),
	};
}

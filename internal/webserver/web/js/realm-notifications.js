import { report } from "./util.js";

const NOTIFICATIONS_POLL_INTERVAL_MS = 5000;

// initRealmNotifications wires the Notifications subtab. The peer <select>
// it reads from (#realm-notification-to) is populated by realm-peers.js.
export function initRealmNotifications(api) {
	const output = document.getElementById("realm-output");
	const notificationToSelect = document.getElementById("realm-notification-to");
	const notificationTitleInput = document.getElementById("realm-notification-title");
	const notificationBodyInput = document.getElementById("realm-notification-body");
	const notificationTtlSelect = document.getElementById("realm-notification-ttl");
	const notificationSendButton = document.getElementById("realm-notification-send-button");
	const notificationsBody = document.getElementById("realm-notifications-tbody");
	const notificationsCount = document.getElementById("realm-notifications-count");

	function renderNotifications(notifications) {
		notificationsBody.innerHTML = "";
		notificationsCount.textContent = notifications.length;
		for (const n of notifications) {
			const row = document.createElement("tr");
			const isSent = n.direction === "sent";
			const cells = [
				["Direction", isSent ? "Sent" : "Received"],
				["Peer", isSent ? n.to : n.from],
				["Title", n.title],
				["Message", n.body],
				["Sent", n.sentAt ? new Date(n.sentAt).toLocaleString() : ""],
				["Status", isSent ? (n.delivered ? "delivered" : "queued (peer offline)") : "received"],
			];
			for (const [label, value] of cells) {
				const cell = document.createElement("td");
				cell.textContent = value;
				cell.dataset.label = label;
				row.appendChild(cell);
			}
			notificationsBody.appendChild(row);
		}
	}

	function refreshNotifications() {
		api.call("notification.list").then((result) => renderNotifications(result.notifications));
	}

	notificationSendButton.addEventListener("click", () =>
		report(output, async () => {
			const to = notificationToSelect.value;
			const title = notificationTitleInput.value.trim();
			const body = notificationBodyInput.value.trim();
			const ttlSeconds = Number(notificationTtlSelect.value);
			console.log("[action] send notification", { to, title, ttlSeconds });
			if (!to) {
				output.textContent = "No peer selected — no known peers yet.";
				return;
			}
			if (!title) {
				output.textContent = "Please enter a title.";
				return;
			}
			await api.call("notification.send", { to, title, body, ttlSeconds });
			notificationTitleInput.value = "";
			notificationBodyInput.value = "";
			output.textContent = `Notification sent to ${to}.`;
			refreshNotifications();
		})
	);

	refreshNotifications();
	setInterval(refreshNotifications, NOTIFICATIONS_POLL_INTERVAL_MS);
}

// Shared between the groups and permissions subtabs: both render a
// checkable/selectable list of realm permission actions.

const ACTION_LABELS = {
	"common/spec/get": "Fetch this machine's spec",
	"common/notifications/receive": "Send this machine notifications",
	"common/scripts/run": "Run scripts on this machine",
	"box/speedtest/run": "Run a speed test against this machine",
};

export function actionLabel(action) {
	return ACTION_LABELS[action] || action;
}

export function renderActionCheckboxes(container, actions) {
	container.innerHTML = "";
	for (const action of actions) {
		const label = document.createElement("label");
		const checkbox = document.createElement("md-checkbox");
		checkbox.value = action;
		label.appendChild(checkbox);
		label.appendChild(document.createTextNode(" " + actionLabel(action)));
		container.appendChild(label);
	}
}

export function checkedActions(container) {
	return Array.from(container.querySelectorAll("md-checkbox"))
		.filter((cb) => cb.checked)
		.map((cb) => cb.value);
}

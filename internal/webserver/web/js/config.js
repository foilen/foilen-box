import { report } from "./util.js";

export function initConfigTab(api) {
	const randomPortCheckbox = document.getElementById("config-random-port");
	const portRow = document.getElementById("config-port-row");
	const portInput = document.getElementById("config-port");
	const saveButton = document.getElementById("config-save-button");
	const output = document.getElementById("config-output");

	function setPortRowVisible(visible) {
		portRow.classList.toggle("hidden", !visible);
	}

	randomPortCheckbox.addEventListener("change", () => {
		console.log("[action] toggle random webui port", { randomPort: randomPortCheckbox.checked });
		setPortRowVisible(!randomPortCheckbox.checked);
	});

	api.call("config.loadConfig").then((cfg) => {
		randomPortCheckbox.checked = cfg.randomPort;
		portInput.value = cfg.port;
		setPortRowVisible(!cfg.randomPort);
	});

	saveButton.addEventListener("click", async () => {
		const randomPort = randomPortCheckbox.checked;
		const port = Number(portInput.value);
		console.log("[action] save webui config", { randomPort, port });
		if (!randomPort && (!Number.isInteger(port) || port < 1 || port > 65535)) {
			output.textContent = "Please enter a valid port between 1 and 65535.";
			return;
		}
		await report(output, async () => {
			await api.call("config.saveConfig", { randomPort, port });
			output.textContent = "Configuration saved. Restart Foilen Box for it to take effect.";
		});
	});
}

import { report } from "./util.js";

export function initEarlyTab(api) {
	const configToggle = document.getElementById("early-config-toggle");
	const configBody = document.getElementById("early-config-body");
	const apiKeyInput = document.getElementById("early-api-key");
	const apiSecretInput = document.getElementById("early-api-secret");
	const saveButton = document.getElementById("early-save-button");
	const aggregateButton = document.getElementById("early-aggregate-button");
	const deleteButton = document.getElementById("early-delete-button");
	const activitySelect = document.getElementById("early-activity-select");
	const progress = document.getElementById("early-progress");
	const output = document.getElementById("early-output");

	function setConfigCollapsed(collapsed) {
		configBody.classList.toggle("hidden", collapsed);
		configToggle.textContent = (collapsed ? "▶" : "▼") + " Configuration";
	}

	configToggle.addEventListener("click", () => {
		console.log("[action] toggle early config visibility");
		setConfigCollapsed(!configBody.classList.contains("hidden"));
	});

	function setBusy(busy) {
		progress.classList.toggle("hidden", !busy);
		aggregateButton.disabled = busy;
		deleteButton.disabled = busy;
		saveButton.disabled = busy;
	}

	api.call("early.loadConfig").then((cfg) => {
		if (!apiKeyInput.value) apiKeyInput.value = cfg.apiKey;
		if (!apiSecretInput.value) apiSecretInput.value = cfg.apiSecret;
		setConfigCollapsed(!!(cfg.apiKey && cfg.apiSecret));
	});

	saveButton.addEventListener("click", async () => {
		const apiKey = apiKeyInput.value.trim();
		const apiSecret = apiSecretInput.value.trim();
		console.log("[action] save early config", { apiKeySet: !!apiKey, apiSecretSet: !!apiSecret });
		if (!apiKey || !apiSecret) {
			output.textContent = "Please enter both API Key and API Secret.";
			return;
		}
		try {
			await api.call("early.saveConfig", { apiKey, apiSecret });
			output.textContent = "Configuration saved successfully.";
			setConfigCollapsed(true);
		} catch (err) {
			output.textContent = "Failed to save configuration: " + err.message;
		}
	});

	aggregateButton.addEventListener("click", async () => {
		console.log("[action] aggregate early time entries");
		setBusy(true);
		output.textContent = "Aggregating time entries...\n";
		await report(output, async () => {
			const result = await api.call("early.aggregate");
			output.textContent = result.text;
			activitySelect.innerHTML = '<md-select-option value=""><div slot="headline">-- select --</div></md-select-option>';
			for (const name of result.activityNames) {
				const opt = document.createElement("md-select-option");
				opt.value = name;
				const headline = document.createElement("div");
				headline.slot = "headline";
				headline.textContent = name;
				opt.appendChild(headline);
				activitySelect.appendChild(opt);
			}
		});
		setBusy(false);
	});

	deleteButton.addEventListener("click", async () => {
		const selected = activitySelect.value;
		console.log("[action] delete early time entries", { activity: selected });
		if (!selected) {
			output.textContent = "No activity selected.";
			return;
		}
		if (!confirm(`Delete all time entries for activity "${selected}"?`)) {
			return;
		}
		setBusy(true);
		output.textContent = `Deleting time entries for activity "${selected}"...\n`;
		await report(output, async () => {
			const result = await api.call("early.delete", { activity: selected });
			output.textContent = result.text;
			for (const opt of Array.from(activitySelect.options)) {
				if (opt.value === selected) opt.remove();
			}
		});
		setBusy(false);
	});
}

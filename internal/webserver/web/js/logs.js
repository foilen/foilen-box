const LOGS_POLL_INTERVAL_MS = 5000;

export function initLogsTab(api) {
	const output = document.getElementById("logs-output");
	const refreshButton = document.getElementById("logs-refresh-button");
	const searchInput = document.getElementById("logs-search-input");
	const autoRefreshCheckbox = document.getElementById("logs-auto-refresh");
	const stayToEndCheckbox = document.getElementById("logs-stay-to-end");

	function load() {
		const search = searchInput.value.trim();
		console.log("[action] load logs", { search });
		api.call("logs.read", { search })
			.then((result) => {
				output.textContent = result.text || "(log file is empty)";
				if (stayToEndCheckbox.checked) {
					output.scrollTop = output.scrollHeight;
				}
			})
			.catch((err) => { output.textContent = "Error: " + err.message; });
	}

	refreshButton.addEventListener("click", load);
	searchInput.addEventListener("keydown", (e) => {
		if (e.key === "Enter") load();
	});

	setInterval(() => {
		if (autoRefreshCheckbox.checked) load();
	}, LOGS_POLL_INTERVAL_MS);

	load();
}

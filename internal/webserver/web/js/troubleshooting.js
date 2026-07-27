export function initTroubleshootingTab(api) {
	const domainInput = document.getElementById("domain-input");
	const button = document.getElementById("troubleshoot-button");
	const progress = document.getElementById("troubleshoot-progress");
	const output = document.getElementById("troubleshoot-output");

	async function run() {
		const domain = domainInput.value.trim();
		console.log("[action] troubleshoot domain", { domain });
		if (!domain) {
			output.textContent = "Please enter a domain name.";
			return;
		}

		button.disabled = true;
		progress.classList.remove("hidden");
		output.textContent = "Querying WHOIS and DNS for " + domain + " ...\n";

		try {
			const result = await api.call("troubleshooting.run", { domain });
			output.textContent = result.text;
		} catch (err) {
			output.textContent = "Error: " + err.message;
		} finally {
			progress.classList.add("hidden");
			button.disabled = false;
		}
	}

	button.addEventListener("click", run);
	domainInput.addEventListener("keydown", (e) => {
		if (e.key === "Enter") run();
	});
}

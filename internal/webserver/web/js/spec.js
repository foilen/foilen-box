export function initSpecTab(api) {
	const output = document.getElementById("spec-output");
	console.log("[action] load spec report");
	api.call("spec.report")
		.then((result) => { output.textContent = result.text; })
		.catch((err) => { output.textContent = "Error: " + err.message; });
}

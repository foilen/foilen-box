export function initGpsTab(isAndroid) {
	const unavailable = document.getElementById("gps-unavailable");
	const available = document.getElementById("gps-available");
	const startButton = document.getElementById("gps-start-button");
	const stopButton = document.getElementById("gps-stop-button");
	const output = document.getElementById("gps-output");

	if (!isAndroid) {
		unavailable.classList.remove("hidden");
		available.classList.add("hidden");
		return;
	}

	let watchId = null;

	function formatPosition(pos) {
		const c = pos.coords;
		const lines = [
			`Latitude:  ${c.latitude}`,
			`Longitude: ${c.longitude}`,
			`Accuracy:  ${c.accuracy} m`,
		];
		if (c.altitude !== null) lines.push(`Altitude:  ${c.altitude} m`);
		if (c.speed !== null) lines.push(`Speed:     ${c.speed} m/s`);
		if (c.heading !== null) lines.push(`Bearing:   ${c.heading}°`);
		lines.push(`Time:      ${new Date(pos.timestamp).toISOString()}`);
		return lines.join("\n");
	}

	startButton.addEventListener("click", () => {
		console.log("[action] start gps tracking");
		if (!("geolocation" in navigator)) {
			output.textContent = "Geolocation is not available.";
			return;
		}
		output.textContent = "Acquiring location...";
		startButton.disabled = true;
		stopButton.disabled = false;
		watchId = navigator.geolocation.watchPosition(
			(pos) => { output.textContent = formatPosition(pos); },
			(err) => { output.textContent = "Error: " + err.message; },
			{ enableHighAccuracy: true, maximumAge: 0 },
		);
	});

	stopButton.addEventListener("click", () => {
		console.log("[action] stop gps tracking");
		if (watchId !== null) {
			navigator.geolocation.clearWatch(watchId);
			watchId = null;
		}
		startButton.disabled = false;
		stopButton.disabled = true;
	});
}

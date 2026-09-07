import { report } from "./util.js";

const CAMERA_STATUS_POLL_INTERVAL_MS = 5000;

// Camera is a standalone top-level feature: its config lives entirely on
// internal/camera.Manager (camera.getStatus/listDevices/saveConfig), not in
// the shared realm config, since it's purely local-device configuration. It
// only touches Realm to optionally advertise itself as a Realm Service, so
// the "expose as service" checkbox is disabled while Realm is off.
export function initCameraTab(api) {
	const enabledCheckbox = document.getElementById("camera-enabled");
	const configBody = document.getElementById("camera-config-body");
	const deviceSelect = document.getElementById("camera-device-select");
	const audioSelect = document.getElementById("camera-audio-select");
	const refreshDevicesButton = document.getElementById("camera-refresh-devices-button");
	const resolutionSelect = document.getElementById("camera-resolution-select");
	const portInput = document.getElementById("camera-port");
	const bindSelect = document.getElementById("camera-bind-select");
	const exposeServiceCheckbox = document.getElementById("camera-expose-service");
	const exposeServiceHint = document.getElementById("camera-expose-service-hint");
	const saveButton = document.getElementById("camera-save-button");
	const manualControlBody = document.getElementById("camera-manual-control-body");
	const startButton = document.getElementById("camera-start-button");
	const stopButton = document.getElementById("camera-stop-button");
	const statusEl = document.getElementById("camera-status");
	const output = document.getElementById("camera-output");

	let devices = [];
	let audioDevices = [];
	let dirty = false;

	function markDirty() {
		dirty = true;
	}

	async function syncDeviceOptions(selectedId) {
		const want = selectedId || deviceSelect.value;
		deviceSelect.innerHTML = "";
		for (const device of devices) {
			const option = document.createElement("md-select-option");
			option.value = device.id;
			const headline = document.createElement("div");
			headline.slot = "headline";
			headline.textContent = device.label || device.id;
			option.appendChild(headline);
			deviceSelect.appendChild(option);
		}
		// <md-outlined-select> registers newly-slotted <md-select-option>s via
		// an async slotchange, so setting .value in the same tick as the
		// appendChild loop above silently no-ops — wait a frame first.
		await new Promise((resolve) => requestAnimationFrame(resolve));
		if (want && devices.some((d) => d.id === want)) {
			deviceSelect.value = want;
		} else if (devices.length > 0) {
			deviceSelect.value = devices[0].id;
		}
	}

	async function syncAudioOptions(selectedId) {
		const want = selectedId ?? audioSelect.value;
		audioSelect.innerHTML = "";
		const entries = [{ id: "", label: "None (no audio)" }, ...audioDevices];
		for (const device of entries) {
			const option = document.createElement("md-select-option");
			option.value = device.id;
			const headline = document.createElement("div");
			headline.slot = "headline";
			headline.textContent = device.label || device.id;
			option.appendChild(headline);
			audioSelect.appendChild(option);
		}
		await new Promise((resolve) => requestAnimationFrame(resolve));
		if (want && entries.some((d) => d.id === want)) {
			audioSelect.value = want;
		} else {
			audioSelect.value = "";
		}
	}

	async function refreshDevices(selectedId, selectedAudioId) {
		const [result, audioResult] = await Promise.all([
			api.call("camera.listDevices"),
			api.call("camera.listAudioDevices"),
		]);
		devices = result.devices || [];
		audioDevices = audioResult.devices || [];
		await syncDeviceOptions(selectedId);
		await syncAudioOptions(selectedAudioId);
	}

	function renderStatus(status) {
		if (!dirty) {
			enabledCheckbox.checked = status.enabled;
			configBody.classList.toggle("hidden", !status.enabled);
			portInput.value = status.port || 8554;
			resolutionSelect.value = status.resolution || "1280x720";
			bindSelect.value = status.bindAllInterfaces ? "all" : "local";
			exposeServiceCheckbox.checked = status.exposeAsService;
		}

		manualControlBody.classList.toggle("hidden", !status.enabled || !status.manualControl);
		startButton.disabled = status.streaming;
		stopButton.disabled = !status.streaming;

		const url = `rtsp://${status.bindAllInterfaces ? "<this device's LAN IP>" : "127.0.0.1"}:${status.port}/stream`;
		if (!status.enabled) {
			statusEl.textContent = "Camera exposure is disabled.";
		} else if (status.lastError) {
			statusEl.textContent = `Error: ${status.lastError} (${url})`;
		} else if (status.streaming) {
			statusEl.textContent = `Streaming — ${status.viewerCount} client(s) connected (${url})`;
		} else if (status.manualControl) {
			statusEl.textContent = `Off — press "Start streaming" to begin (${url})`;
		} else {
			statusEl.textContent = `Ready — camera stays off until a client connects (${url})`;
		}
	}

	function renderRealmEnabled(realmEnabled) {
		exposeServiceCheckbox.disabled = !realmEnabled;
		exposeServiceHint.classList.toggle("hidden", realmEnabled);
	}

	async function refreshStatus() {
		const [status, realmConfig] = await Promise.all([api.call("camera.getStatus"), api.call("realm.loadConfig")]);
		renderStatus(status);
		renderRealmEnabled(realmConfig.enabled);
		// Camera has no per-visit activation hook (it's a top-level tab, not a
		// lazily-activated subtab), so this periodic poll is also what recovers
		// the device list if the one-shot fetch below raced camera readiness at
		// page load and came back empty.
		if (status.enabled && devices.length === 0) {
			await refreshDevices(status.deviceId, status.audioDeviceId);
		}
		return status;
	}

	async function saveConfig() {
		const selectedDevice = devices.find((d) => d.id === deviceSelect.value);
		const selectedAudio = audioDevices.find((d) => d.id === audioSelect.value);
		const params = {
			enabled: enabledCheckbox.checked,
			deviceId: deviceSelect.value || "",
			deviceLabel: selectedDevice ? selectedDevice.label : "",
			audioDeviceId: audioSelect.value || "",
			audioDeviceLabel: selectedAudio ? selectedAudio.label : "",
			resolution: resolutionSelect.value || "1280x720",
			port: parseInt(portInput.value, 10) || 8554,
			bindAllInterfaces: bindSelect.value === "all",
			exposeAsService: exposeServiceCheckbox.checked,
		};
		console.log("[action] save camera config", params);
		const status = await api.call("camera.saveConfig", params);
		dirty = false;
		renderStatus(status);
		output.textContent = "Camera configuration saved.";
	}

	enabledCheckbox.addEventListener("change", () => {
		markDirty();
		configBody.classList.toggle("hidden", !enabledCheckbox.checked);
		if (enabledCheckbox.checked && devices.length === 0) {
			report(output, () => refreshDevices());
		} else if (!enabledCheckbox.checked) {
			// Unchecking hides camera-config-body, which contains the save
			// button itself, so it becomes unreachable — save right away.
			report(output, saveConfig);
		}
	});
	deviceSelect.addEventListener("change", markDirty);
	audioSelect.addEventListener("change", markDirty);
	resolutionSelect.addEventListener("change", markDirty);
	portInput.addEventListener("input", markDirty);
	bindSelect.addEventListener("change", markDirty);
	exposeServiceCheckbox.addEventListener("change", markDirty);

	refreshDevicesButton.addEventListener("click", () =>
		report(output, async () => {
			console.log("[action] refresh camera devices");
			await refreshDevices(deviceSelect.value, audioSelect.value);
		})
	);

	saveButton.addEventListener("click", () => report(output, saveConfig));

	startButton.addEventListener("click", () =>
		report(output, async () => {
			console.log("[action] start camera streaming");
			renderStatus(await api.call("camera.startCapture"));
		})
	);

	stopButton.addEventListener("click", () =>
		report(output, async () => {
			console.log("[action] stop camera streaming");
			renderStatus(await api.call("camera.stopCapture"));
		})
	);

	refreshStatus().catch(() => {});
	setInterval(() => refreshStatus().catch(() => {}), CAMERA_STATUS_POLL_INTERVAL_MS);
}

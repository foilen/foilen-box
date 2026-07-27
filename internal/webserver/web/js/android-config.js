export function initAndroidConfigTab(isAndroid) {
	if (!isAndroid) return;

	const bootAutostartCheckbox = document.getElementById("android-boot-autostart");
	bootAutostartCheckbox.checked = window.AndroidConfigBridge.isBootAutostartEnabled();

	bootAutostartCheckbox.addEventListener("change", () => {
		console.log("[action] set android boot autostart", { enabled: bootAutostartCheckbox.checked });
		window.AndroidConfigBridge.setBootAutostartEnabled(bootAutostartCheckbox.checked);
	});
}

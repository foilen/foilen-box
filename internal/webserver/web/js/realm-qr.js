// QR display and camera-scan modals used when exporting/importing a group's
// key. Decoupled from the groups subtab: initQrModal returns a function to
// show data as a QR code, and initScanModal takes a callback invoked with
// the raw scanned string.

export function initQrModal() {
	const qrModal = document.getElementById("realm-qr-modal");
	const qrTitle = document.getElementById("realm-qr-title");
	const qrCode = document.getElementById("realm-qr-code");
	const qrCloseButton = document.getElementById("realm-qr-close-button");

	qrCloseButton.addEventListener("click", () => {
		console.log("[action] close qr modal");
		qrModal.classList.add("hidden");
		qrCode.innerHTML = "";
	});

	return function showQrCode(title, data) {
		const qr = qrcode(0, "M");
		qr.addData(JSON.stringify(data));
		qr.make();
		qrTitle.textContent = title;
		qrCode.innerHTML = qr.createImgTag(6, 8);
		qrModal.classList.remove("hidden");
	};
}

export function initScanModal(onScanned) {
	const scanModal = document.getElementById("realm-scan-modal");
	const scanVideo = document.getElementById("realm-scan-video");
	const scanCanvas = document.getElementById("realm-scan-canvas");
	const scanStatus = document.getElementById("realm-scan-status");
	const scanCloseButton = document.getElementById("realm-scan-close-button");
	let scanStream = null;
	let scanRequestId = null;

	function stopScan() {
		if (scanRequestId !== null) {
			cancelAnimationFrame(scanRequestId);
			scanRequestId = null;
		}
		if (scanStream) {
			scanStream.getTracks().forEach((track) => track.stop());
			scanStream = null;
		}
		scanVideo.srcObject = null;
		scanModal.classList.add("hidden");
	}

	function scanFrame() {
		const context = scanCanvas.getContext("2d");
		if (scanVideo.readyState === scanVideo.HAVE_ENOUGH_DATA) {
			scanCanvas.width = scanVideo.videoWidth;
			scanCanvas.height = scanVideo.videoHeight;
			context.drawImage(scanVideo, 0, 0, scanCanvas.width, scanCanvas.height);
			const imageData = context.getImageData(0, 0, scanCanvas.width, scanCanvas.height);
			const code = jsQR(imageData.data, imageData.width, imageData.height);
			if (code) {
				scanStatus.textContent = "";
				stopScan();
				onScanned(code.data);
				return;
			}
		}
		scanRequestId = requestAnimationFrame(scanFrame);
	}

	scanCloseButton.addEventListener("click", () => {
		console.log("[action] close qr scan modal");
		stopScan();
	});

	return async function startScan() {
		console.log("[action] start qr scan");
		try {
			scanStatus.textContent = "";
			scanModal.classList.remove("hidden");
			scanStream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
			scanVideo.srcObject = scanStream;
			await scanVideo.play();
			scanRequestId = requestAnimationFrame(scanFrame);
		} catch (err) {
			scanStatus.textContent = "Error: " + err.message;
		}
	};
}

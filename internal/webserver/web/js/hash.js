// Keeps location.hash in sync with the active tab/sub-tab, and lets tab/subtab
// init code restore the right one from the hash on page load. Hash format is
// "#tab", "#tab/subtab", or "#tab/subtab/extra" — extra is an arbitrary,
// subtab-owned deep-link payload (e.g. SMS uses it for "groupId|storeName" or
// "groupId|storeName|phoneNumber", so a refresh or a notification click can
// restore the store being viewed and, optionally, the open conversation).
export function parseHash() {
	const [tab, subtab, extra] = location.hash.replace(/^#/, "").split("/").map((part) => decodeURIComponent(part));
	return { tab: tab || null, subtab: subtab || null, extra: extra || null };
}

// Reads the currently active tab (and, if it has a "<tab>-subtabs" nav with
// an active sub-tab button, that sub-tab too) and writes it to location.hash.
// extra, if given, is appended as a 3rd segment (see parseHash); omitting it
// preserves the previous 2-segment behavior for every tab that doesn't use
// one.
export function updateHash(extra) {
	const tabButton = document.querySelector(".tab-button.active");
	if (!tabButton) return;
	const tab = tabButton.dataset.tab;
	const subtabButton = document.querySelector(`#${tab}-subtabs .subtab-button.active`);
	let newHash = subtabButton ? `#${tab}/${subtabButton.dataset.subtab}` : `#${tab}`;
	if (subtabButton && extra) {
		newHash += `/${encodeURIComponent(extra)}`;
	}
	if (location.hash !== newHash) {
		history.replaceState(null, "", newHash);
	}
}

// Hash format is "#tab", "#tab/subtab", or "#tab/subtab/extra" — extra is an
// arbitrary, subtab-owned deep-link payload (e.g. SMS uses "groupId|storeName"
// so a refresh or notification click can restore the view being shown).
export function parseHash() {
	const [tab, subtab, extra] = location.hash.replace(/^#/, "").split("/").map((part) => decodeURIComponent(part));
	return { tab: tab || null, subtab: subtab || null, extra: extra || null };
}

// extra, if given, is appended as a 3rd segment (see parseHash).
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

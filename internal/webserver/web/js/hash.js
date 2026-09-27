export function parseHash() {
	const [tab, subtab, extra] = location.hash.replace(/^#/, "").split("/").map((part) => decodeURIComponent(part));
	return { tab: tab || null, subtab: subtab || null, extra: extra || null };
}

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

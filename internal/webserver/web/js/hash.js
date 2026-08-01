// Keeps location.hash in sync with the active tab/sub-tab, and lets tab/subtab
// init code restore the right one from the hash on page load. Hash format is
// "#tab" or "#tab/subtab".

export function parseHash() {
	const [tab, subtab] = location.hash.replace(/^#/, "").split("/");
	return { tab: tab || null, subtab: subtab || null };
}

// Reads the currently active tab (and, if it has a "<tab>-subtabs" nav with
// an active sub-tab button, that sub-tab too) and writes it to location.hash.
export function updateHash() {
	const tabButton = document.querySelector(".tab-button.active");
	if (!tabButton) return;
	const tab = tabButton.dataset.tab;
	const subtabButton = document.querySelector(`#${tab}-subtabs .subtab-button.active`);
	const newHash = subtabButton ? `#${tab}/${subtabButton.dataset.subtab}` : `#${tab}`;
	if (location.hash !== newHash) {
		history.replaceState(null, "", newHash);
	}
}

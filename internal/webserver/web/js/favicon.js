const DEFAULT_FAVICON = "images/favicon.png";
const SUBTAB_FAVICONS = {
	"realm-sms-subtab": "images/favicon-sms.png",
};

export function setFavicon(href) {
	document.getElementById("app-favicon").href = href;
}

export function updateFaviconForSubtab(subtabId) {
	setFavicon(SUBTAB_FAVICONS[subtabId] || DEFAULT_FAVICON);
}

export function updateFaviconForTab(tabId) {
	if (tabId !== "realm") {
		setFavicon(DEFAULT_FAVICON);
		return;
	}
	const activeSubtab = document.querySelector("#realm-subtabs .subtab-button.active");
	updateFaviconForSubtab(activeSubtab && activeSubtab.dataset.subtab);
}

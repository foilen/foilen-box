export async function report(output, fn) {
	try {
		await fn();
	} catch (err) {
		output.textContent = "Error: " + err.message;
	}
}

export function shortId(id) {
	return `[${id.slice(-6)}]`;
}

export function formatPeerLabel(peer) {
	const parts = [];
	if (peer.hostname) parts.push(peer.hostname);
	if (peer.description) parts.push(`(${peer.description})`);
	parts.push(shortId(peer.id));
	return parts.join(" ");
}

export function formatGroupLabel(group) {
	return `${group.name} ${shortId(group.id)}`;
}

export function formatIdentityLabel(identity) {
	return `${identity.name} ${shortId(identity.id)}`;
}

export function formatKnownPeerLabel(knownPeers, peerId) {
	const peer = knownPeers.find((p) => p.id === peerId);
	return peer ? formatPeerLabel(peer) : shortId(peerId);
}

export function syncList(container, items, keyOf, create, update) {
	const remaining = new Map();
	for (const child of container.children) {
		remaining.set(child.dataset.key, child);
	}

	let anchor = container.firstChild;
	for (const item of items) {
		const key = String(keyOf(item));
		let el = remaining.get(key);
		if (el) {
			remaining.delete(key);
			update(el, item);
		} else {
			el = create(item);
			el.dataset.key = key;
		}
		if (el === anchor) {
			anchor = anchor.nextSibling;
		} else {
			container.insertBefore(el, anchor);
		}
	}

	for (const el of remaining.values()) {
		el.remove();
	}
}

export function syncCells(row, cells, offset = 0) {
	cells.forEach(([label, value], i) => {
		const idx = offset + i;
		let cell = row.children[idx];
		if (!cell) {
			cell = document.createElement("td");
			row.appendChild(cell);
		}
		cell.dataset.label = label;
		const text = String(value);
		if (cell.textContent !== text) cell.textContent = text;
	});
	return offset + cells.length;
}

export function syncSelectOptions(select, entries) {
	const previousValue = select.value;
	syncList(
		select,
		entries,
		([value]) => value,
		([value, label]) => {
			const option = document.createElement("md-select-option");
			option.value = value;
			option.innerHTML = `<div slot="headline">${label}</div>`;
			return option;
		},
		(option, [, label]) => {
			const headline = option.querySelector('[slot="headline"]');
			if (headline.textContent !== label) headline.textContent = label;
		}
	);
	select.value = previousValue;
}

export function syncConnectedCell(row, connected) {
	let cell = row.querySelector('td[data-label="Connected"]');
	let dot;
	if (!cell) {
		cell = document.createElement("td");
		cell.dataset.label = "Connected";
		dot = document.createElement("span");
		cell.appendChild(dot);
		row.appendChild(cell);
	} else {
		dot = cell.querySelector("span");
	}
	dot.className = `status-dot${connected ? " connected" : ""}`;
	dot.title = connected ? "Connected" : "Not connected";
}

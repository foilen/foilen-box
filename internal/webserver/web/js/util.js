// Runs fn; on failure writes "Error: <message>" to output.
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

// Renders "hostname (description) [last 6 chars of id]", omitting parts that are missing.
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

// Falls back to the shortened id when peerId isn't in knownPeers yet (e.g. a
// peer that posted into a group's map but hasn't been approved/seen directly).
export function formatKnownPeerLabel(knownPeers, peerId) {
	const peer = knownPeers.find((p) => p.id === peerId);
	return peer ? formatPeerLabel(peer) : shortId(peerId);
}

// Reconciles container's children to match items (keyed by keyOf) instead of
// clearing and rebuilding on every render: existing keys are patched via
// update(el, item), gone keys are removed, new keys via create(item). Nodes
// are moved rather than recreated, which keeps <select> selections,
// checkbox/details state, and scroll position stable across polling refreshes.
// Each item's key must be stable and unique (e.g. an id, not an array index).
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

// Patches a <tr>'s leading <td>s in place from [label, value] pairs, creating
// cells if missing and only touching textContent when it changed — works for
// both syncList's create and update callbacks. Returns the cell count so
// callers can append further custom cells after these.
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

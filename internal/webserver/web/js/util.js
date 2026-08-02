// report runs fn and, on failure, writes "Error: <message>" to output. Used
// by every button handler that just needs to surface a failed api.call().
export async function report(output, fn) {
	try {
		await fn();
	} catch (err) {
		output.textContent = "Error: " + err.message;
	}
}

// shortId returns the last 6 characters of an id, wrapped in brackets, so
// long ids can be shown compactly while still being distinguishable.
export function shortId(id) {
	return `[${id.slice(-6)}]`;
}

// formatPeerLabel renders a peer id as "hostname (description) [last 6
// chars of id]", falling back gracefully when hostname/description are
// missing.
export function formatPeerLabel(peer) {
	const parts = [];
	if (peer.hostname) parts.push(peer.hostname);
	if (peer.description) parts.push(`(${peer.description})`);
	parts.push(shortId(peer.id));
	return parts.join(" ");
}

// formatGroupLabel renders a group as "name [last 6 chars of id]".
export function formatGroupLabel(group) {
	return `${group.name} ${shortId(group.id)}`;
}

// formatIdentityLabel renders an identity as "name [last 6 chars of id]".
export function formatIdentityLabel(identity) {
	return `${identity.name} ${shortId(identity.id)}`;
}

// formatKnownPeerLabel renders peerId via formatPeerLabel when it's found in
// knownPeers, falling back to just the shortened id (rather than the full raw
// id) when the peer isn't known yet, e.g. a peer that has posted into a
// group's map but hasn't been approved/seen directly.
export function formatKnownPeerLabel(knownPeers, peerId) {
	const peer = knownPeers.find((p) => p.id === peerId);
	return peer ? formatPeerLabel(peer) : shortId(peerId);
}

// syncList reconciles the element children of `container` to match `items`,
// keyed by `keyOf(item)`, instead of clearing and rebuilding everything on
// every render. Children whose key is still present are patched in place via
// `update(el, item)`; children whose key has disappeared are removed; new
// keys are built via `create(item)`. Existing nodes are moved (not
// recreated) to match the order of `items`. This is what keeps <select>
// selections, checkbox/details state, and scroll position stable across
// polling refreshes instead of flickering on every tick.
//
// Requires each item's key to be stable and unique within the list (e.g. an
// id, not an array index).
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

// syncCells patches a <tr>'s leading <td>s in place from `cells`, an array
// of [label, value] pairs, creating a cell at a position if missing and
// otherwise only touching textContent when the value actually changed.
// Works identically whether `row` is brand new (cells get created) or
// existing (cells get patched by position), so the same cell list can drive
// both a syncList `create` and `update` callback. Returns the number of
// cells written, so callers appending further custom cells (buttons, etc.)
// after these know how many leading cells to skip over.
export function syncCells(row, cells) {
	cells.forEach(([label, value], i) => {
		let cell = row.children[i];
		if (!cell) {
			cell = document.createElement("td");
			row.appendChild(cell);
		}
		cell.dataset.label = label;
		const text = String(value);
		if (cell.textContent !== text) cell.textContent = text;
	});
	return cells.length;
}

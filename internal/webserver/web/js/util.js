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

// formatKnownPeerLabel renders peerId via formatPeerLabel when it's found in
// knownPeers, falling back to just the shortened id (rather than the full raw
// id) when the peer isn't known yet, e.g. a peer that has posted into a
// group's map but hasn't been approved/seen directly.
export function formatKnownPeerLabel(knownPeers, peerId) {
	const peer = knownPeers.find((p) => p.id === peerId);
	return peer ? formatPeerLabel(peer) : shortId(peerId);
}

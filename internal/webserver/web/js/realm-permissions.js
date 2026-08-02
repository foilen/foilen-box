import { report, formatPeerLabel, syncList } from "./util.js";
import { actionLabel } from "./realm-actions.js";

// initRealmPermissions wires the Permissions subtab: a checkbox matrix per
// group and per peer, one checkbox per available action, mirroring the
// checkbox list used on group creation. renderConfig is the top-level
// fan-out (see realm.js) called after any mutation, since the backend
// returns the full config on every change.
export function initRealmPermissions(api, output, renderConfig) {
	const permissionsCount = document.getElementById("realm-permissions-count");
	const groupsContainer = document.getElementById("realm-permissions-groups");
	const peersContainer = document.getElementById("realm-permissions-peers");

	let latestCfg = { availableActions: [], groups: [], permissions: [] };
	let latestPeers = [];

	function hasPermission(action, target) {
		return latestCfg.permissions.some((perm) => {
			if (perm.action !== action) return false;
			if (target.groupName) return perm.groupName === target.groupName;
			return perm.peerId === target.peerId;
		});
	}

	function togglePermission(action, target, granted) {
		return report(output, async () => {
			console.log("[action] toggle permission", { action, target, granted });
			const params = { action, peerId: target.peerId || "", groupName: target.groupName || "" };
			renderConfig(await api.call(granted ? "realm.addPermission" : "realm.deletePermission", params));
			output.textContent = granted ? "Permission granted." : "Permission revoked.";
		});
	}

	function renderEntityCheckboxes(container, entities, keyFor, labelFor, targetFor) {
		syncList(
			container,
			entities,
			keyFor,
			(entity) => {
				const block = document.createElement("div");
				block.className = "permission-entity";

				const heading = document.createElement("strong");
				heading.textContent = labelFor(entity);
				block.appendChild(heading);

				const list = document.createElement("div");
				list.className = "checkbox-list";
				block.appendChild(list);
				syncActionCheckboxes(list, targetFor(entity));

				return block;
			},
			(block, entity) => {
				const heading = block.querySelector("strong");
				const label = labelFor(entity);
				if (heading.textContent !== label) heading.textContent = label;
				syncActionCheckboxes(block.querySelector(".checkbox-list"), targetFor(entity));
			}
		);
	}

	// syncActionCheckboxes reconciles the per-action checkbox row for one
	// entity (group or peer), keeping each <md-checkbox> node stable across
	// re-renders so mid-click state / focus isn't lost when this runs on
	// every peers poll.
	function syncActionCheckboxes(list, target) {
		syncList(
			list,
			latestCfg.availableActions,
			(action) => action,
			(action) => {
				const label = document.createElement("label");
				const checkbox = document.createElement("md-checkbox");
				checkbox.checked = hasPermission(action, target);
				checkbox.addEventListener("change", () => togglePermission(action, target, checkbox.checked));
				label.appendChild(checkbox);
				label.appendChild(document.createTextNode(" " + actionLabel(action)));
				return label;
			},
			(label, action) => {
				label.querySelector("md-checkbox").checked = hasPermission(action, target);
			}
		);
	}

	function render() {
		permissionsCount.textContent = latestCfg.permissions.length;
		renderEntityCheckboxes(groupsContainer, latestCfg.groups, (group) => group.name, (group) => group.name, (group) => ({ groupName: group.name }));
		renderEntityCheckboxes(peersContainer, latestPeers, (peer) => peer.id, formatPeerLabel, (peer) => ({ peerId: peer.id }));
	}

	function renderPermissions(cfg) {
		latestCfg = {
			availableActions: cfg.availableActions || [],
			groups: cfg.groups || [],
			permissions: cfg.permissions || [],
		};
		render();
	}

	function updatePeers(peers) {
		latestPeers = peers || [];
		render();
	}

	return { renderPermissions, updatePeers };
}

#!/usr/bin/env node
// Mirrors a set of esm.sh ES module graphs to local files, rewriting every
// import/export specifier to a relative local path, so the browser never
// hits esm.sh at runtime. Run at build time (see flake.nix's vendorJs FOD
// and scripts/fetch-vendor-assets.sh for the non-Nix path).
//
// Usage: node fetch-vendor-js.mjs <output-dir>

import { mkdir, writeFile, access } from "node:fs/promises";
import { dirname, join, posix } from "node:path";

const ESM_ORIGIN = "https://esm.sh";

// name: subdirectory under <output-dir> and the fixed local entry filename
// referenced by index.html / app JS.
const ENTRIES = [
	{ name: "material-web", url: "https://esm.sh/@material/web@2.5.0/all.js" },
	{ name: "mermaid", url: "https://esm.sh/mermaid@11" },
];

// Matches real import/export specifiers while avoiding false positives on
// the words "import"/"export" appearing inside string literals or
// identifiers in minified bundles (e.g. `ur="@import"` in stylis).
// - group 1: bare `import "url";`
// - group 2: `import ... from "url"` / `export ... from "url"` (the clause
//   between the keyword and `from` is restricted to identifier-ish
//   characters so it can't skip over unrelated code to a later `from`)
// - group 3: dynamic `import("url")`
const IMPORT_RE = /(?<![\w@"'])import\s*["']([^"']+)["']|(?<![\w@"'])(?:import|export)\b[^;"'()]*?\bfrom\s*["']([^"']+)["']|(?<![\w@"'])import\(\s*["']([^"']+)["']\s*\)/g;

function sanitizeSegment(segment) {
	return segment.replace(/[^a-zA-Z0-9._-]/g, (c) => "_" + c.charCodeAt(0).toString(16) + "_");
}

// Maps an esm.sh URL to a stable, unique, filesystem-safe relative path.
function localPathFor(url) {
	const u = new URL(url);
	const segments = u.pathname.split("/").filter(Boolean).map(sanitizeSegment);
	let filename = segments.pop() ?? "index";
	if (u.search) {
		filename += sanitizeSegment(u.search);
	}
	if (!/\.(m?js|css)$/.test(filename)) {
		filename += ".mjs";
	}
	segments.push(filename);
	return segments.join("/");
}

async function fetchText(url) {
	const res = await fetch(url);
	if (!res.ok) {
		throw new Error(`fetch failed ${res.status} ${res.statusText}: ${url}`);
	}
	return res.text();
}

async function crawl(entryUrl) {
	// url (canonical, resolved) -> { localPath, body, specifiers: [{raw, resolvedUrl}] }
	const visited = new Map();
	const queue = [entryUrl];

	while (queue.length > 0) {
		const url = queue.shift();
		if (visited.has(url)) continue;

		const body = await fetchText(url);
		const specifiers = [];
		for (const match of body.matchAll(IMPORT_RE)) {
			const raw = match[1] ?? match[2] ?? match[3];
			if (!raw) continue;
			const resolvedUrl = new URL(raw, url).toString();
			if (!resolvedUrl.startsWith(ESM_ORIGIN + "/")) {
				throw new Error(`refusing to follow import outside ${ESM_ORIGIN}: ${resolvedUrl} (from ${url})`);
			}
			specifiers.push({ raw, resolvedUrl });
			if (!visited.has(resolvedUrl)) {
				queue.push(resolvedUrl);
			}
		}

		visited.set(url, { localPath: localPathFor(url), body, specifiers });
	}

	return visited;
}

// Rewrites every import/export specifier in `body` to a path relative to
// `fromLocalPath`, pointing at the target module's local path. Also strips
// sourceMappingURL comments, which still point at esm.sh and would
// otherwise be the only remaining external reference (harmless if unused,
// but noisy in devtools and not needed since we don't ship .map files).
function rewrite(body, fromLocalPath, specifiers, visited) {
	let out = body;
	for (const { raw, resolvedUrl } of specifiers) {
		const target = visited.get(resolvedUrl);
		let rel = posix.relative(posix.dirname(fromLocalPath), target.localPath);
		if (!rel.startsWith(".")) rel = "./" + rel;
		out = out.split(`"${raw}"`).join(`"${rel}"`).split(`'${raw}'`).join(`'${rel}'`);
	}
	out = out.replace(/\/\/# sourceMappingURL=.*$/gm, "");
	out = out.replace(/\/\*# sourceMappingURL=.*?\*\//g, "");
	return out;
}

async function main() {
	const outDir = process.argv[2];
	if (!outDir) {
		console.error("usage: fetch-vendor-js.mjs <output-dir>");
		process.exit(1);
	}

	for (const entry of ENTRIES) {
		const entryMarker = join(outDir, entry.name, "entry.mjs");
		if (await access(entryMarker).then(() => true).catch(() => false)) {
			console.log(`${entry.name} already present at ${entryMarker}, skipping (delete the directory to refetch)`);
			continue;
		}

		console.log(`Crawling ${entry.name} from ${entry.url} ...`);
		const visited = await crawl(entry.url);

		const entryInfo = visited.get(entry.url);
		// Force the entry module itself to a fixed, predictable path (relative
		// to this entry's own directory) so the rest of the app can import it
		// by a stable name: vendor-js/<entry.name>/entry.mjs.
		entryInfo.localPath = "entry.mjs";

		let fileCount = 0;
		for (const [, info] of visited) {
			const rewritten = rewrite(info.body, info.localPath, info.specifiers, visited);
			const fullPath = join(outDir, entry.name, info.localPath);
			await mkdir(dirname(fullPath), { recursive: true });
			await writeFile(fullPath, rewritten);
			fileCount++;
		}
		console.log(`  wrote ${fileCount} files under ${entry.name}/`);
	}
}

main().catch((err) => {
	console.error(err.stack || err.message);
	process.exit(1);
});

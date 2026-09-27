#!/usr/bin/env node

import { mkdir, writeFile, access } from "node:fs/promises";
import { dirname, join, posix } from "node:path";

const ESM_ORIGIN = "https://esm.sh";

const ENTRIES = [
	{ name: "material-web", url: "https://esm.sh/@material/web@2.5.0/all.js" },
	{ name: "mermaid", url: "https://esm.sh/mermaid@11.17.2" },
];

const IMPORT_RE = /(?<![\w@"'])import\s*["']([^"']+)["']|(?<![\w@"'])(?:import|export)\b[^;"'()]*?\bfrom\s*["']([^"']+)["']|(?<![\w@"'])import\(\s*["']([^"']+)["']\s*\)/g;

function sanitizeSegment(segment) {
	return segment.replace(/[^a-zA-Z0-9._-]/g, (c) => "_" + c.charCodeAt(0).toString(16) + "_");
}

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

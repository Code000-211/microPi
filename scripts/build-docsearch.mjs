#!/usr/bin/env node
/**
 * Builds the project_search addon (packages/docsearch, a Rust addon) for the platform this machine is on and stages the
 * built-in plugin manifest for a release build, where the addon is compiled into the executable
 * (scripts/build-pi.sh --builtins). Prints the manifest's path.
 *
 * packages/docsearch/install.sh remains for development: it installs the extension into the agent directory instead, from
 * where `upi compile` compiles it in; a release's built-in stands in for it (`-builtin:docsearch` in the `extensions`
 * setting turns it off, an installed copy with the same name replaces it).
 *
 * Usage: scripts/build-docsearch.mjs --out DIR
 *   --out DIR  where the builtins manifest and the addon go (build-pi.sh stages its folder into the Pi tree)
 * Needs Rust (cargo) on the PATH, or ~/.cargo/bin on it.
 */

import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const DOCSEARCH = join(HERE, "..", "packages", "docsearch");
const pathDelimiter = process.platform === "win32" ? ";" : ":";

/** The addon's file name on this platform, and the name the extension loads it by, as install.sh puts it. */
const platform = () => {
	switch (process.platform) {
		case "linux":
			return { os: "linux", lib: "libdocsearch_node.so" };
		case "darwin":
			return { os: "darwin", lib: "libdocsearch_node.dylib" };
		case "win32":
			return { os: "win32", lib: "docsearch_node.dll" };
		default:
			return undefined;
	}
};

function die(message) {
	process.stderr.write(`error: ${message}\n`);
	process.exit(1);
}

function main(argv) {
	let out = "";
	for (let i = 0; i < argv.length; i += 2) {
		if (argv[i] === "--out") out = argv[i + 1];
		else die(`bad argument ${argv[i] ?? "(missing)"}`);
	}
	if (!out) die("--out DIR is required");

	const { os, lib } = platform() ?? {};
	const arch = process.arch === "x64" || process.arch === "arm64" ? process.arch : undefined;
	if (!os || !arch) die(`docsearch: no addon for ${process.platform} ${process.arch}`);
	const addonName = `docsearch.${os}-${arch}.node`;

	// (The PATH that .cargo/bin is on: cargo installs itself there, and a fresh runner's shell may not have it.)
	const env = { ...process.env, PATH: `${homedir()}/.cargo/bin${pathDelimiter}${process.env.PATH ?? ""}` };
	const found = spawnSync("cargo", ["--version"], { env });
	if (found.error || found.status !== 0) die("cargo not found: Rust is needed to build the docsearch addon (rustup.rs)");
	const build = spawnSync("cargo", ["build", "--release", "-p", "docsearch-node"], {
		cwd: DOCSEARCH,
		env,
		stdio: "inherit",
	});
	if (build.status !== 0) die("cargo build -p docsearch-node failed");

	// The extension with this platform's addon name (so that `upi compile` embeds the addon by a literal path, as when it is
	// installed by install.sh), and the manifest that names it a built-in, replaceable extension.
	rmSync(out, { recursive: true, force: true });
	mkdirSync(join(out, "docsearch"), { recursive: true });
	const addon = join(DOCSEARCH, "target", "release", lib);
	if (!existsSync(addon)) die(`${addon} not found (cargo build)`);
	cpSync(addon, join(out, "docsearch", addonName));
	const index = readFileSync(join(DOCSEARCH, "extension", "index.ts"), "utf8");
	if (!index.includes("./docsearch.linux-x64.node")) die("packages/docsearch/extension/index.ts loads the addon from another path");
	writeFileSync(join(out, "docsearch", "index.ts"), index.replace("./docsearch.linux-x64.node", `./${addonName}`));
	writeFileSync(
		join(out, "builtins.ts"),
		`/**
 * µPi's built-in plugins (packages/docsearch: project_search), for a release build (scripts/build-pi.sh --builtins). The
 * addon is built for the platform the release is made on and embedded by its literal file name.
 */
import type { InlineExtension } from "@earendil-works/pi-coding-agent";
import docsearch from "./docsearch/index.ts";

const plugins: InlineExtension[] = [{ name: "docsearch", builtin: true, replaceable: true, factory: docsearch }];

export default plugins;
`,
	);
	process.stdout.write(`${join(out, "builtins.ts")}\n`);
}

main(process.argv.slice(2));

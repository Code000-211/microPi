/**
 * µPi plugin manifest: the extensions compiled into the executable.
 *
 * Build:  scripts/build-pi.sh --plugins examples/plugins/plugins.ts --out out/pi-bolt-plugins
 *
 * Each entry is an extension factory, `(pi: ExtensionAPI) => void | Promise<void>`, the default export of a Pi extension, or
 * `{ name, factory }` to give it a name in Pi's startup list and in errors. Everything this file imports is compiled ahead of
 * time with Pi, so these extensions start and run as machine code: no TypeScript compile at load, no interpreter.
 */
import type { InlineExtension } from "@earendil-works/pi-coding-agent";
import wordCount from "./extensions/word-count.ts";

const plugins: InlineExtension[] = [
	{ name: "word-count", factory: wordCount },
];

export default plugins;

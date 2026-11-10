import type { InlineExtension } from "../core/extensions/types.ts";
import { PIBOLT } from "../upi.ts";
import codemodeExtension from "./codemode/index.ts";
import herdrExtension from "./herdr/index.ts";
import { herdrFromEnvironment } from "./herdr/transport.ts";
import llamaExtension from "./llama/index.ts";
import mcpExtension from "./mcp/index.ts";
import toolSearchExtension from "./tool-search/index.ts";
import usageExtension from "./usage/index.ts";

export const builtInExtensions: InlineExtension[] = [
	{ name: "llama.cpp", factory: llamaExtension, builtin: true },
	// Replaceable: an extension that registers `codemode`, `tool_search`, or `/mcp` (such as a third-party
	// MCP extension) takes over instead of running alongside the built-in one.
	{ name: "codemode", factory: codemodeExtension, replaceable: true, builtin: true },
	{ name: "tool-search", factory: toolSearchExtension, replaceable: true, builtin: true },
	{ name: "mcp", factory: mcpExtension, replaceable: true, builtin: true },
	{ name: "usage", factory: usageExtension, replaceable: true, builtin: true },
	// µPi only, in a Herdr pane: reports to Herdr in place of Herdr's own Pi integration (see herdr/index.ts). (Outside
	// Herdr it is not set up at all, which is work at every start for nothing.)
	...(PIBOLT && herdrFromEnvironment() ? [{ name: "herdr", factory: herdrExtension, builtin: true }] : []),
];

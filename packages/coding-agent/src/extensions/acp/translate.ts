/**
 * Conversions between ACP and Pi: prompts in, tool calls and conversation history out. Pure functions, no I/O.
 */

import { isAbsolute, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { AgentMessage } from "@earendil-works/pi-agent-core";
import type { ImageContent } from "@earendil-works/pi-ai";
import { MOVED_TO_BACKGROUND_NOTE } from "../../core/tools/bash.ts";
import { resolveToCwd } from "../../core/tools/path-utils.ts";
import { isRecord } from "../json-rpc.ts";
import type {
	ContentBlock,
	SessionUpdate,
	StopReason,
	ToolCallContent,
	ToolCallLocation,
	ToolCallStatus,
	ToolKind,
} from "./types.ts";

/** A prompt as `AgentSession.prompt()` takes it. */
export interface PiPrompt {
	text: string;
	images: ImageContent[];
}

/** The path of a `file:` URI, or undefined for other URIs. */
export function fileUriPath(uri: string): string | undefined {
	if (!uri.startsWith("file:")) return undefined;
	try {
		return fileURLToPath(uri);
	} catch {
		return undefined;
	}
}

/**
 * Turns ACP prompt content into Pi's prompt text and images. Text and links stay where the user put them (a link reads
 * as its path, as an `@` mention does in Pi's editor); embedded resources follow as `<file>` blocks, the form Pi gives
 * `@file` arguments.
 */
export function promptToPi(blocks: readonly ContentBlock[]): PiPrompt {
	let text = "";
	const files: string[] = [];
	const images: ImageContent[] = [];
	const append = (piece: string) => {
		if (text.length > 0 && !/\s$/.test(text) && !/^\s/.test(piece)) text += " ";
		text += piece;
	};
	for (const block of blocks) {
		switch (block.type) {
			case "text":
				text += block.text;
				break;
			case "image":
				images.push({ type: "image", data: block.data, mimeType: block.mimeType });
				break;
			case "resource_link":
				append(fileUriPath(block.uri) ?? block.uri);
				break;
			case "resource": {
				const resource = block.resource;
				const name = fileUriPath(resource.uri) ?? resource.uri;
				if ("text" in resource) {
					files.push(`<file name="${name}">\n${resource.text}\n</file>`);
				} else if (resource.mimeType?.startsWith("image/")) {
					images.push({ type: "image", data: resource.blob, mimeType: resource.mimeType });
				} else {
					files.push(`<file name="${name}"></file>`);
				}
				break;
			}
			case "audio":
				// Not advertised (promptCapabilities.audio is false); a client that sends it anyway loses only that block.
				break;
		}
	}
	if (files.length > 0) text = text.length > 0 ? `${files.join("\n")}\n${text}` : files.join("\n");
	return { text, images };
}

/** How a tool call is shown in the editor. */
export interface ToolCallInfo {
	title: string;
	kind: ToolKind;
	locations: ToolCallLocation[];
}

function stringArg(args: unknown, key: string): string | undefined {
	if (typeof args !== "object" || args === null) return undefined;
	const value = (args as Record<string, unknown>)[key];
	return typeof value === "string" ? value : undefined;
}

function numberArg(args: unknown, key: string): number | undefined {
	if (typeof args !== "object" || args === null) return undefined;
	const value = (args as Record<string, unknown>)[key];
	return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function oneLine(text: string, max = 120): string {
	const line = text.replace(/\s+/g, " ").trim();
	return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

/** Absolute path of a tool's path argument, resolved as the tools resolve it. */
export function toolPath(args: unknown, cwd: string, key = "path"): string | undefined {
	const path = stringArg(args, key);
	if (path === undefined || path === "") return undefined;
	const absolute = resolveToCwd(path, cwd);
	return isAbsolute(absolute) ? absolute : resolve(cwd, absolute);
}

/** Title, kind, and file locations of one of Pi's tools; other tools are `other`, titled with their name. */
export function toolCallInfo(toolName: string, args: unknown, cwd: string): ToolCallInfo {
	const path = toolPath(args, cwd);
	const at = path ? [{ path }] : [];
	switch (toolName) {
		case "read": {
			const offset = numberArg(args, "offset");
			return {
				title: `Read ${stringArg(args, "path") ?? "file"}`,
				kind: "read",
				// The read tool's offset counts from 1; a location's line counts from 0, as Zed takes it.
				locations: path
					? [{ path, line: offset !== undefined && offset >= 1 ? Math.floor(offset) - 1 : undefined }]
					: [],
			};
		}
		case "edit":
			return { title: `Edit ${stringArg(args, "path") ?? "file"}`, kind: "edit", locations: at };
		case "write":
			return { title: `Write ${stringArg(args, "path") ?? "file"}`, kind: "edit", locations: at };
		case "bash": {
			const command = stringArg(args, "command");
			return { title: command ? oneLine(command) : "Run command", kind: "execute", locations: [] };
		}
		case "grep": {
			const pattern = stringArg(args, "pattern");
			return {
				title: pattern ? `Search ${oneLine(pattern, 80)}${path ? ` in ${stringArg(args, "path")}` : ""}` : "Search",
				kind: "search",
				locations: at,
			};
		}
		case "find": {
			const pattern = stringArg(args, "pattern");
			return { title: pattern ? `Find ${oneLine(pattern, 80)}` : "Find files", kind: "search", locations: at };
		}
		case "ls":
			return { title: `List ${stringArg(args, "path") ?? "."}`, kind: "search", locations: at };
		default:
			return { title: toolName, kind: "other", locations: at };
	}
}

/** The text and images of a tool result (`AgentToolResult.content` or a tool result message's content). */
export function toolResultContent(result: unknown): ToolCallContent[] {
	const content =
		typeof result === "object" && result !== null && Array.isArray((result as { content?: unknown }).content)
			? ((result as { content: unknown[] }).content as unknown[])
			: Array.isArray(result)
				? result
				: undefined;
	if (!content) return [];
	const blocks: ToolCallContent[] = [];
	for (const item of content) {
		if (typeof item !== "object" || item === null) continue;
		const part = item as Record<string, unknown>;
		if (part.type === "text" && typeof part.text === "string") {
			if (part.text.length > 0) blocks.push({ type: "content", content: { type: "text", text: part.text } });
		} else if (part.type === "image" && typeof part.data === "string" && typeof part.mimeType === "string") {
			blocks.push({ type: "content", content: { type: "image", data: part.data, mimeType: part.mimeType } });
		}
	}
	return blocks;
}

/** Shows text as a fenced block, for command output, which editors would otherwise render as Markdown. */
export function fenced(text: string): string {
	let fence = "```";
	while (text.includes(fence)) fence += "`";
	return `${fence}\n${text}${text.endsWith("\n") ? "" : "\n"}${fence}`;
}

/** Bash output in a fence, so the editor shows it as it is. */
export function bashResultContent(result: unknown): ToolCallContent[] {
	return toolResultContent(result).map((block) =>
		block.type === "content" && block.content.type === "text"
			? { type: "content", content: { type: "text", text: fenced(block.content.text) } }
			: block,
	);
}

/** The text of a tool result, its text blocks joined. */
export function resultText(result: unknown): string {
	return toolResultContent(result)
		.map((block) => (block.type === "content" && block.content.type === "text" ? block.content.text : ""))
		.join("");
}

/** Command output as the bash tool reports it while the command runs. */
export interface OutputTail {
	/** The output so far, or when `truncated` part of it: its end (maybe without a trailing newline), or, for the raw tail, its last bytes with the start left out. */
	text: string;
	/** UTF-8 bytes of all the output so far. */
	totalBytes: number;
	truncated: boolean;
}

/** The output in a partial result of the bash tool. */
export function bashOutputTail(partialResult: unknown): OutputTail {
	const details = isRecord(partialResult) ? partialResult.details : undefined;
	const terminal = isRecord(details) ? details.terminal : undefined;
	if (isRecord(terminal) && typeof terminal.text === "string" && typeof terminal.totalBytes === "number") {
		// The output as the command wrote it, escape sequences included: a terminal is not given the cleaned text.
		return { text: terminal.text, totalBytes: terminal.totalBytes, truncated: terminal.truncated === true };
	}
	const text = resultText(partialResult);
	const truncation = isRecord(details) ? details.truncation : undefined;
	if (isRecord(truncation) && truncation.truncated === true && typeof truncation.totalBytes === "number") {
		// Whole lines, which a newline comes before: with it, a tail that has only new lines still shows where they start.
		const lines = truncation.lastLinePartial !== true;
		return { text: lines ? `\n${text}` : text, totalBytes: truncation.totalBytes, truncated: true };
	}
	return { text, totalBytes: Buffer.byteLength(text, "utf8"), truncated: false };
}

/** What a terminal has been sent: the output up to byte `end`, which `text` is the end of. */
export interface TerminalSent {
	text: string;
	end: number;
	/** False when `text` may end a byte before `end` (a truncated tail without its trailing newline). */
	exact: boolean;
}

export const TERMINAL_START: TerminalSent = { text: "", end: 0, exact: true };

/** Clears a terminal's screen and scrollback. */
const CLEAR_TERMINAL = "\x1b[H\x1b[2J\x1b[3J";

/**
 * The part of the output a terminal has not been sent yet, for a terminal that appends what it is sent. The bash tool
 * reports only the end of long output, so the new part is found by byte counts, and checked against what was sent:
 * the rest of the new tail must be the end of the old one. A truncated tail leaves out a trailing newline, so where a
 * tail ends is known only to a byte; the counts that could be right are tried in turn. Output that went by between two
 * reports is marked as skipped; output that changed instead of growing (another tool's partial results) clears the
 * terminal and starts over.
 */
export function terminalOutputDelta(sent: TerminalSent, next: OutputTail): { data: string; sent: TerminalSent } {
	const bytes = Buffer.from(next.text, "utf8");
	const added = next.totalBytes - sent.end;
	const lengths = [added, ...(next.truncated ? [added - 1] : []), ...(sent.exact ? [] : [added + 1])];
	for (const length of lengths) {
		if (length < 0 || length > bytes.length) continue;
		const at = bytes.length - length;
		if (!sent.text.endsWith(bytes.subarray(0, at).toString("utf8"))) continue;
		return {
			data: bytes.subarray(at).toString("utf8"),
			sent: { text: next.text, end: sent.end + length, exact: true },
		};
	}
	const restart = { text: next.text, end: next.totalBytes, exact: !next.truncated };
	if (lengths.every((length) => length > bytes.length)) {
		const before = sent.text !== "" && !sent.text.endsWith("\n") ? "\n" : "";
		const after = next.text.startsWith("\n") ? "" : "\n";
		return { data: `${before}[… output skipped …]${after}${next.text}`, sent: restart };
	}
	return { data: `${CLEAR_TERMINAL}${next.text}`, sent: restart };
}

/** The exit code of a finished bash call, when the command ran to its end. */
export function bashExitCode(result: unknown, isError: boolean): number | null {
	const structured = isRecord(result) ? result.structuredContent : undefined;
	if (isRecord(structured) && typeof structured.exit_code === "number") return structured.exit_code;
	if (!isError && resultText(result).includes(MOVED_TO_BACKGROUND_NOTE))
		// The command went on in the background: it has no exit code yet, whatever its text says.
		return null;
	return isError ? null : 0;
}

/** The prompt's stop reason, from the last assistant message of the run. */
export function stopReasonOf(messages: readonly AgentMessage[], cancelled: boolean): StopReason {
	if (cancelled) return "cancelled";
	for (let i = messages.length - 1; i >= 0; i--) {
		const message = messages[i];
		if (message.role !== "assistant") continue;
		switch (message.stopReason) {
			case "aborted":
				return "cancelled";
			case "length":
				return "max_tokens";
			default:
				return "end_turn";
		}
	}
	return "end_turn";
}

/** The error that ended the run, if the last assistant message failed. */
export function runError(messages: readonly AgentMessage[]): string | undefined {
	for (let i = messages.length - 1; i >= 0; i--) {
		const message = messages[i];
		if (message.role !== "assistant") continue;
		return message.stopReason === "error" ? message.errorMessage || "The model request failed" : undefined;
	}
	return undefined;
}

function textOf(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	return content
		.map((part) =>
			typeof part === "object" && part !== null && (part as { type?: unknown }).type === "text"
				? String((part as { text?: unknown }).text ?? "")
				: "",
		)
		.join("");
}

function imagesOf(content: unknown): ContentBlock[] {
	if (!Array.isArray(content)) return [];
	const images: ContentBlock[] = [];
	for (const part of content) {
		if (typeof part !== "object" || part === null) continue;
		const image = part as { type?: unknown; data?: unknown; mimeType?: unknown };
		if (image.type === "image" && typeof image.data === "string" && typeof image.mimeType === "string") {
			images.push({ type: "image", data: image.data, mimeType: image.mimeType });
		}
	}
	return images;
}

/**
 * The conversation as session updates, for `session/load`: user and assistant messages, thinking, and tool calls with
 * their results, and compaction summaries for clients that show compactions. Messages Pi keeps out of the transcript
 * (extension state, branch summaries) are left out.
 */
export function replayMessages(
	messages: readonly AgentMessage[],
	cwd: string,
	options: { compaction?: boolean } = {},
): SessionUpdate[] {
	const updates: SessionUpdate[] = [];
	const results = new Map<string, { content: unknown; isError: boolean }>();
	for (const message of messages) {
		if (message.role === "toolResult") {
			results.set(message.toolCallId, { content: message.content, isError: message.isError });
		}
	}
	let index = 0;
	for (const message of messages) {
		const messageId = `msg_${index++}`;
		switch (message.role) {
			case "user": {
				const text = textOf(message.content);
				if (text) updates.push({ sessionUpdate: "user_message_chunk", messageId, content: { type: "text", text } });
				for (const image of imagesOf(message.content)) {
					updates.push({ sessionUpdate: "user_message_chunk", messageId, content: image });
				}
				break;
			}
			case "assistant": {
				for (const part of message.content) {
					if (part.type === "thinking" && part.thinking) {
						updates.push({
							sessionUpdate: "agent_thought_chunk",
							messageId,
							content: { type: "text", text: part.thinking },
						});
					} else if (part.type === "text" && part.text) {
						updates.push({
							sessionUpdate: "agent_message_chunk",
							messageId,
							content: { type: "text", text: part.text },
						});
					} else if (part.type === "toolCall") {
						const info = toolCallInfo(part.name, part.arguments, cwd);
						const result = results.get(part.id);
						const status: ToolCallStatus = result ? (result.isError ? "failed" : "completed") : "failed";
						updates.push({
							sessionUpdate: "tool_call",
							toolCallId: part.id,
							name: part.name,
							title: info.title,
							kind: info.kind,
							status,
							locations: info.locations,
							rawInput: part.arguments,
							content: result
								? part.name === "bash"
									? bashResultContent(result.content)
									: toolResultContent(result.content)
								: [],
						});
					}
				}
				break;
			}
			case "compactionSummary":
				// For clients that show compactions: the summary that took the place of the messages before it.
				if (options.compaction) {
					updates.push({
						sessionUpdate: "compaction_update",
						compactionId: `compaction_${messageId}`,
						status: "completed",
						summary: [{ type: "text", text: message.summary }],
					});
				}
				break;
			default:
				break;
		}
	}
	return updates;
}

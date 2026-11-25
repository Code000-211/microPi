import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { AgentTool } from "@earendil-works/pi-agent-core";
import { validateToolArguments } from "@earendil-works/pi-ai";
import type { AssistantMessage, ToolCall } from "@earendil-works/pi-ai/compat";
import type { TSchema } from "typebox";
import { afterEach, describe, expect, it } from "vitest";
import { computeFileLists, createFileOps, extractFileOpsFromMessage } from "../src/core/compaction/utils.ts";
import { renameArgumentAliases } from "../src/core/tools/argument-aliases.ts";
import { createBashTool } from "../src/core/tools/bash.ts";
import { createEditTool } from "../src/core/tools/edit.ts";
import { createGrepTool } from "../src/core/tools/grep.ts";
import { createReadTool } from "../src/core/tools/read.ts";
import { createWriteTool } from "../src/core/tools/write.ts";

const tempDirs: string[] = [];

async function createTempDir(): Promise<string> {
	const dir = await mkdtemp(join(tmpdir(), "pi-argument-aliases-"));
	tempDirs.push(dir);
	return dir;
}

afterEach(async () => {
	await Promise.all(tempDirs.splice(0, tempDirs.length).map((dir) => rm(dir, { recursive: true, force: true })));
});

/** The arguments the agent loop executes a call with: prepared, then validated against the tool's schema. */
function callArguments<T extends TSchema>(tool: AgentTool<T>, raw: ToolCall["arguments"]) {
	const prepared = tool.prepareArguments ? tool.prepareArguments(raw) : raw;
	return validateToolArguments(tool, {
		type: "toolCall",
		id: "call-1",
		name: tool.name,
		arguments: prepared as ToolCall["arguments"],
	});
}

function textOf(result: { content: Array<{ type: string; text?: string }> }): string {
	return result.content.map((block) => block.text ?? "").join("");
}

describe("renameArgumentAliases", () => {
	it("renames an alias and leaves the input as it was", () => {
		const input = { file_path: "a.txt", limit: 5 };
		expect(renameArgumentAliases(input, { file_path: "path" })).toEqual({ path: "a.txt", limit: 5 });
		expect(input).toEqual({ file_path: "a.txt", limit: 5 });
	});

	it("returns the input itself when nothing is renamed", () => {
		const input = { path: "a.txt" };
		expect(renameArgumentAliases(input, { file_path: "path" })).toBe(input);
		expect(renameArgumentAliases("garbage", { file_path: "path" })).toBe("garbage");
		expect(renameArgumentAliases(null, { file_path: "path" })).toBe(null);
	});

	it("keeps the tool's own name when both are sent", () => {
		const input = { path: "own.txt", file_path: "alias.txt" };
		expect(renameArgumentAliases<{ path: string }>(input, { file_path: "path" }).path).toBe("own.txt");
	});
});

describe("tool calls with Claude Code's argument names", () => {
	it("fail validation without the rename", async () => {
		const tool = createReadTool(await createTempDir());
		expect(() =>
			validateToolArguments(tool, {
				type: "toolCall",
				id: "call-1",
				name: "read",
				arguments: { file_path: "a.txt" },
			}),
		).toThrow();
	});

	it("read takes file_path", async () => {
		const dir = await createTempDir();
		await writeFile(join(dir, "a.txt"), "one\ntwo\nthree\n", "utf8");
		const tool = createReadTool(dir);
		const args = callArguments(tool, { file_path: "a.txt", offset: 2, limit: 1 });
		expect(args).toEqual({ path: "a.txt", offset: 2, limit: 1 });
		expect(textOf(await tool.execute("call-1", args))).toContain("two");
	});

	it("write takes file_path", async () => {
		const dir = await createTempDir();
		const tool = createWriteTool(dir);
		const args = callArguments(tool, { file_path: "new.txt", content: "hello\n" });
		await tool.execute("call-1", args);
		expect(await readFile(join(dir, "new.txt"), "utf8")).toBe("hello\n");
	});

	it("edit takes file_path, old_string and new_string", async () => {
		const dir = await createTempDir();
		await writeFile(join(dir, "a.txt"), "before\n", "utf8");
		const tool = createEditTool(dir);
		const args = callArguments(tool, {
			file_path: "a.txt",
			old_string: "before",
			new_string: "after",
			replace_all: false,
		});
		expect(args).toMatchObject({ path: "a.txt", edits: [{ oldText: "before", newText: "after" }] });
		await tool.execute("call-1", args);
		expect(await readFile(join(dir, "a.txt"), "utf8")).toBe("after\n");
	});

	it("edit takes old_string and new_string inside edits", async () => {
		const tool = createEditTool(await createTempDir());
		const expected = {
			path: "a.txt",
			edits: [
				{ oldText: "a", newText: "b" },
				{ oldText: "c", newText: "d" },
			],
		};
		const aliased: Record<string, string> = { old_string: "a", new_string: "b" };
		const edits = [aliased, { oldText: "c", newText: "d" }];
		expect(callArguments(tool, { path: "a.txt", edits })).toEqual(expected);
		expect(callArguments(tool, { path: "a.txt", edits: JSON.stringify(edits) })).toEqual(expected);
		expect(callArguments(tool, { path: "a.txt", edits: aliased })).toEqual({
			path: "a.txt",
			edits: [expected.edits[0]],
		});
	});

	it("grep takes -i, -C and head_limit", async () => {
		const tool = createGrepTool(await createTempDir());
		expect(callArguments(tool, { pattern: "x", "-i": true, "-C": 2, head_limit: 10 })).toEqual({
			pattern: "x",
			ignoreCase: true,
			context: 2,
			limit: 10,
		});
	});

	it("bash takes run_in_background", async () => {
		const tool = createBashTool(await createTempDir());
		expect(callArguments(tool, { command: "true", run_in_background: true })).toEqual({
			command: "true",
			background: true,
		});
	});

	it("count for the file lists of a compaction", () => {
		const message: AssistantMessage = {
			role: "assistant",
			content: [
				{ type: "toolCall", id: "call-1", name: "read", arguments: { file_path: "a.ts" } },
				{ type: "toolCall", id: "call-2", name: "edit", arguments: { file_path: "b.ts", old_string: "a" } },
			],
			api: "openai-completions",
			provider: "test",
			model: "test",
			usage: {
				input: 0,
				output: 0,
				cacheRead: 0,
				cacheWrite: 0,
				totalTokens: 0,
				cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
			},
			stopReason: "toolUse",
			timestamp: 0,
		};
		const fileOps = createFileOps();
		extractFileOpsFromMessage(message, fileOps);
		expect(computeFileLists(fileOps)).toEqual({
			readFiles: ["a.ts"],
			modifiedFiles: ["b.ts"],
			omittedReadFiles: 0,
			omittedModifiedFiles: 0,
		});
	});
});

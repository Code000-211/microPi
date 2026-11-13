import { mkdtempSync, rmSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";
import { afterEach, describe, expect, it, vi } from "vitest";
import type * as OutputFiles from "../src/utils/output-files.ts";

const scratch = mkdtempSync(join(tmpdir(), "pi-output-accumulator-"));

// The full output file with a size limit of 1000 bytes, as the real one is past 1 GiB.
vi.mock("../src/utils/output-files.ts", async (importOriginal) => {
	const actual = await importOriginal<typeof OutputFiles>();
	let files = 0;
	return {
		...actual,
		createOutputFile: () => new actual.OutputFile(join(scratch, `out-${files++}.log`), 1000),
	};
});

import { OutputAccumulator } from "../src/core/tools/output-accumulator.ts";

describe("OutputAccumulator.readRawTail", () => {
	it("keeps the output as it was written, escape sequences included", () => {
		const output = new OutputAccumulator({ maxLines: 4, maxBytes: 40, clean: true, keepRawTail: true });
		output.append(Buffer.from("\x1b[31mfirst\x1b[0m\n", "utf8"));
		expect(output.readRawTail()).toEqual({ text: "\x1b[31mfirst\x1b[0m\n", totalBytes: 15, truncated: false });
		// An escape sequence the cleaned path holds back is in the raw tail as it arrives.
		output.append(Buffer.from("\x1b[3", "utf8"));
		output.append(Buffer.from("2my\x1b[0m\n", "utf8"));
		expect(output.readRawTail()).toEqual({
			text: "\x1b[31mfirst\x1b[0m\n\x1b[32my\x1b[0m\n",
			totalBytes: 26,
			truncated: false,
		});
		// The cleaned snapshot for the model has no escape sequences.
		expect(output.snapshot().content).not.toContain("\x1b");
	});

	it("keeps only the end of long output, cutting it at a character boundary", () => {
		const output = new OutputAccumulator({ maxLines: 4, maxBytes: 40, clean: true, keepRawTail: true });
		output.append(Buffer.from("\x1b[31mfirst\x1b[0m\n", "utf8"));
		// "é" is two bytes, so a cut in its middle would show a broken character.
		output.append(Buffer.from(`\u00e9${"\u00e9".repeat(119)}\n`, "utf8"));
		const tail = output.readRawTail();
		expect(tail?.truncated).toBe(true);
		expect(tail?.totalBytes).toBe(15 + 241);
		expect(tail?.text).toMatch(/^[é]+\n$/);
		output.finish();
		output.closeTempFile();
	});

	it("is undefined without the option or without output", () => {
		const plain = new OutputAccumulator({ maxLines: 4, maxBytes: 40 });
		plain.append(Buffer.from("text\n", "utf8"));
		expect(plain.readRawTail()).toBeUndefined();
		const empty = new OutputAccumulator({ maxLines: 4, maxBytes: 40, clean: true, keepRawTail: true });
		expect(empty.readRawTail()).toBeUndefined();
	});
});

describe("OutputAccumulator.readFullOutput", () => {
	afterEach(() => {
		rmSync(scratch, { recursive: true, force: true });
	});

	it("ends with the output's real end when the full output file reached its size limit", async () => {
		const output = new OutputAccumulator({ maxBytes: 100 });
		output.append(Buffer.from("first line\n"));
		for (let i = 0; i < 30; i++) output.append(Buffer.from(`${"y".repeat(99)}\n`));
		output.append(Buffer.from("TESTS: 3 failed\n"));
		output.finish();
		output.closeTempFile();

		const full = await output.readFullOutput(200);

		expect(full.truncated).toBe(true);
		const [head, omitted, tail] = full.content.split(/\n\n\[\.\.\. (\d+) bytes omitted \.\.\.\]\n\n/);
		expect(head.startsWith("first line\n")).toBe(true);
		expect(tail).toMatch(/TESTS: 3 failed\n?$/);
		expect(full.content).not.toContain("was not saved");
		// Of all the output, not of the file.
		expect(Number(omitted)).toBe(11 + 30 * 100 + 16 - head.length - tail.length);
	});
});

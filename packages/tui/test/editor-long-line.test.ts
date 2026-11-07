import assert from "node:assert";
import { describe, it } from "node:test";
import { Editor, wordWrapLine } from "../src/components/editor.ts";
import type { TUI } from "../src/tui.ts";
import { TuiMainScreen } from "../src/tui-main-screen.ts";
import { cjkBreakRegex, getGraphemeSegmenter, isWhitespaceChar, visibleWidth } from "../src/utils.ts";
import { defaultEditorTheme } from "./test-themes.ts";
import { VirtualTerminal } from "./virtual-terminal.ts";

// The editor's long-line fast paths (wrapping a line of standalone characters without segmenting it, and looking up
// the grapheme at the cursor directly) must give exactly what segmenting the whole text gives. Run this file with bun
// as well (`bun test test/editor-long-line.test.ts`): JSC's Intl.Segmenter is the one µPi runs (see graphemeAt()).

const segmenter = getGraphemeSegmenter();

/** A deterministic random source (mulberry32). */
function createRandom(seed: number): () => number {
	let state = seed >>> 0;
	return () => {
		state = (state + 0x6d2b79f5) >>> 0;
		let t = state;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const ASCII = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.,;:!?-_/\\()[]{}<>@#$%^&*+=~`'\"|";
// One-cell characters that are graphemes of their own, with those that are whitespace or CJK for wrapping (U+00A0,
// U+00B7, U+02C7, U+02D9).
const SINGLE_CELL = "éàüßøñçÆŒſ\u00a0«»¿¡°±×÷ʼˆ\u00b7\u02c7\u02d9‐–—‘’“”„†‡•…‧←→↔⇒∀∂∑√∞≠≤≥─│┌┐■□▲▶◆○●";
// Wide characters that are graphemes of their own (CJK, kana, Hangul syllables, CJK punctuation, fullwidth forms).
const WIDE = "中文字漢あいうがカタナー한국어가ＡＢ１２。、，！？「」";
// Wide characters that are not: an ideograph outside the BMP, a variation sequence, kana with a combining voicing mark.
const WIDE_JOINED = ["𠮷", "葛\u{e0100}", "か\u3099", "\u1100\u1161"];
const COMBINING = ["e\u0301", "a\u0308", "n\u0303", "\u0915\u094d\u0937", "\u0e01\u0e33", ...WIDE_JOINED];
const EMOJI = ["😀", "👍🏽", "👨\u200d👩\u200d👧", "🇺🇸", "🇬🇧", "❤\ufe0f", "✔\ufe0f", "©\ufe0f", "↔\ufe0f", "▶\ufe0f"];
const SPACES = [" ", " ", " ", "  ", "\u3000", "\u2003"];

type Alphabet = "ascii" | "standalone" | "mixed";

function randomText(random: () => number, length: number, alphabet: Alphabet): string {
	let text = "";
	while (text.length < length) {
		const r = random();
		if (r < 0.18) {
			text += SPACES[Math.floor(random() * SPACES.length)]!;
		} else if (alphabet === "ascii" || r < 0.6) {
			text += ASCII[Math.floor(random() * ASCII.length)]!;
		} else if (r < 0.75) {
			text += SINGLE_CELL[Math.floor(random() * SINGLE_CELL.length)]!;
		} else if (alphabet === "standalone" || r < 0.85) {
			text += WIDE[Math.floor(random() * WIDE.length)]!;
		} else if (r < 0.93) {
			text += COMBINING[Math.floor(random() * COMBINING.length)]!;
		} else {
			text += EMOJI[Math.floor(random() * EMOJI.length)]!;
		}
	}
	return text;
}

/** The wrapping of the code before the fast path: wordWrapLine() over the line's graphemes. */
function wrapBySegments(line: string, width: number) {
	return wordWrapLine(line, width, [...segmenter.segment(line)]);
}

interface EditorInternals {
	segment(text: string, mode: "word" | "grapheme"): Iterable<Intl.SegmentData>;
	firstGrapheme(text: string): Intl.SegmentData | undefined;
	lastGrapheme(text: string): Intl.SegmentData | undefined;
	graphemeContaining(text: string, index: number): Intl.SegmentData | undefined;
}

function createEditor(): Editor {
	const tui: TUI = new TuiMainScreen(new VirtualTerminal(120, 60));
	return new Editor(tui, defaultEditorTheme);
}

/** The segment fields the editor reads. */
function pick(segment: Intl.SegmentData | undefined): { segment: string; index: number } | undefined {
	return segment === undefined ? undefined : { segment: segment.segment, index: segment.index };
}

describe("Editor long-line fast paths", () => {
	it("wraps lines of standalone characters as wrapping their graphemes does", () => {
		const random = createRandom(1);
		for (let i = 0; i < 3000; i++) {
			const alphabet: Alphabet = i % 2 === 0 ? "ascii" : "standalone";
			const line = randomText(random, Math.floor(random() * 400), alphabet);
			const width = 1 + Math.floor(random() * 90);
			assert.deepStrictEqual(
				wordWrapLine(line, width),
				wrapBySegments(line, width),
				JSON.stringify({ line, width }),
			);
		}
	});

	it("wraps mixed lines (wide, combining, emoji) as before", () => {
		const random = createRandom(2);
		for (let i = 0; i < 1500; i++) {
			const line = randomText(random, Math.floor(random() * 300), "mixed");
			const width = 1 + Math.floor(random() * 60);
			assert.deepStrictEqual(
				wordWrapLine(line, width),
				wrapBySegments(line, width),
				JSON.stringify({ line, width }),
			);
		}
	});

	it("treats every standalone character as a grapheme of its own, one or two columns wide", () => {
		const ranges: Array<[number, number, number]> = [
			[0x20, 0x7e, 1],
			[0xa0, 0x2ff, 1],
			[0x2010, 0x2027, 1],
			[0x2190, 0x22ff, 1],
			[0x2500, 0x25fc, 1],
			[0x3000, 0x3029, 2],
			[0x3041, 0x3096, 2],
			[0x309d, 0x309f, 2],
			[0x30a1, 0x30ff, 2],
			[0x3400, 0x4dbf, 2],
			[0x4e00, 0x9fff, 2],
			[0xac00, 0xd7a3, 2],
			[0xff01, 0xff60, 2],
			[0xffe0, 0xffe6, 2],
		];
		const neighbors = ["a", " ", "é", "中", "\u0301", "\u200d", "\ufe0f", "\u3099", "\u1161", "😀", "🇺", "\u0600"];
		for (const [start, end, width] of ranges) {
			for (let code = start; code <= end; code++) {
				if (code === 0xad) continue;
				const char = String.fromCharCode(code);
				const name = `U+${code.toString(16)}`;
				assert.strictEqual(visibleWidth(char), width, name);
				if (code <= 0x7e) {
					// The fast path takes no printable ASCII character but the space for whitespace or for CJK.
					assert.strictEqual(isWhitespaceChar(char), code === 0x20, name);
					assert.strictEqual(cjkBreakRegex.test(char), false, name);
				}
				// Doubled, it is two graphemes; beside anything else (in a line that then takes the general path) it
				// wraps as before. (Every character of the small ranges, a sample of the large ones.)
				assert.strictEqual([...segmenter.segment(char + char)].length, 2, `${name} doubled`);
				if (end - start > 1000 && code % 61 !== 0) continue;
				for (const neighbor of neighbors) {
					const line = `${char}${neighbor}${char}${char}`;
					for (const wrapWidth of [1, 2, 3]) {
						assert.deepStrictEqual(wordWrapLine(line, wrapWidth), wrapBySegments(line, wrapWidth), name);
					}
				}
			}
		}
	});

	it("looks up the grapheme at the cursor as segmenting the whole text does", () => {
		const random = createRandom(3);
		const editor = createEditor();
		const internals = editor as unknown as EditorInternals;
		for (let i = 0; i < 400; i++) {
			const text = randomText(random, 1 + Math.floor(random() * 120), "mixed");
			const graphemes = [...internals.segment(text, "grapheme")];
			assert.deepStrictEqual(pick(internals.firstGrapheme(text)), pick(graphemes[0]));
			assert.deepStrictEqual(pick(internals.lastGrapheme(text)), pick(graphemes[graphemes.length - 1]));
			for (let index = -1; index <= text.length; index++) {
				const expected = graphemes.find((g) => g.index <= index && index < g.index + g.segment.length);
				assert.deepStrictEqual(
					pick(internals.graphemeContaining(text, index)),
					pick(expected),
					`${text} @${index}`,
				);
			}
		}
	});

	it("keeps paste markers atomic in the lookups", () => {
		const random = createRandom(4);
		const editor = createEditor();
		const internals = editor as unknown as EditorInternals;
		editor.handleInput(`\x1b[200~${"x".repeat(1500)}\x1b[201~`);
		editor.handleInput(`\x1b[200~${"y\n".repeat(20)}\x1b[201~`);
		const markers = ["[paste #1 1500 chars]", "[paste #2 +21 lines]", "[paste #3 12 chars]"];
		for (let i = 0; i < 300; i++) {
			let text = randomText(random, Math.floor(random() * 80), i % 2 === 0 ? "ascii" : "mixed");
			const at = Math.floor(random() * (text.length + 1));
			text = text.slice(0, at) + markers[i % markers.length] + text.slice(at);
			const graphemes = [...internals.segment(text, "grapheme")];
			assert.deepStrictEqual(pick(internals.firstGrapheme(text)), pick(graphemes[0]));
			assert.deepStrictEqual(pick(internals.lastGrapheme(text)), pick(graphemes[graphemes.length - 1]));
			for (let index = 0; index < text.length; index++) {
				const expected = graphemes.find((g) => g.index <= index && index < g.index + g.segment.length);
				assert.deepStrictEqual(
					pick(internals.graphemeContaining(text, index)),
					pick(expected),
					`${text} @${index}`,
				);
			}
		}
	});

	it("edits and moves through a long line by graphemes", () => {
		const random = createRandom(5);
		for (let round = 0; round < 40; round++) {
			const editor = createEditor();
			let text = randomText(random, 300 + Math.floor(random() * 2000), round % 3 === 0 ? "mixed" : "ascii");
			editor.setText(text);
			editor.render(1 + Math.floor(random() * 100));
			let cursor = text.length;
			for (let step = 0; step < 60; step++) {
				// What the code before the fast paths segmented: the text before and after the cursor.
				const before = [...segmenter.segment(text.slice(0, cursor))];
				const after = [...segmenter.segment(text.slice(cursor))];
				const op = random();
				if (op < 0.25) {
					const char = randomText(random, 1, "mixed").slice(0, 1);
					if (char.charCodeAt(0) >= 0xd800 && char.charCodeAt(0) <= 0xdfff) continue;
					editor.handleInput(char);
					text = text.slice(0, cursor) + char + text.slice(cursor);
					cursor += char.length;
				} else if (op < 0.45) {
					editor.handleInput("\x7f");
					const last = before[before.length - 1];
					if (last) {
						text = text.slice(0, last.index) + text.slice(cursor);
						cursor = last.index;
					}
				} else if (op < 0.6) {
					editor.handleInput("\x1b[3~");
					const first = after[0];
					if (first) text = text.slice(0, cursor) + text.slice(cursor + first.segment.length);
				} else if (op < 0.8) {
					editor.handleInput("\x1b[D");
					const last = before[before.length - 1];
					if (last) cursor = last.index;
				} else {
					editor.handleInput("\x1b[C");
					const first = after[0];
					if (first) cursor += first.segment.length;
				}
				assert.strictEqual(editor.getText(), text);
				assert.deepStrictEqual(editor.getCursor(), { line: 0, col: cursor });
			}
		}
	});
});

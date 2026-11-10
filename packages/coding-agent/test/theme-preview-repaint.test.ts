import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import type { AssistantMessage } from "@earendil-works/pi-ai";
import { type Component, Container, isViewportTUI, Text } from "@earendil-works/pi-tui";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { VirtualTerminal } from "../../tui/test/virtual-terminal.ts";
import { createShellRenderers } from "../src/core/tools/renderers/bash.ts";
import { createChatViewport } from "../src/modes/interactive/chat-viewport.ts";
import { AssistantMessageComponent } from "../src/modes/interactive/components/assistant-message.ts";
import { DynamicBorder } from "../src/modes/interactive/components/dynamic-border.ts";
import { ThemeSelectorComponent } from "../src/modes/interactive/components/theme-selector.ts";
import { ThemedText } from "../src/modes/interactive/components/themed-text.ts";
import { ToolExecutionComponent } from "../src/modes/interactive/components/tool-execution.ts";
import { TranscriptContainer } from "../src/modes/interactive/components/transcript-container.ts";
import { UserMessageComponent } from "../src/modes/interactive/components/user-message.ts";
import { createInteractiveTui } from "../src/modes/interactive/interactive-mode.ts";
import {
	getAvailableThemes,
	getThemeInstance,
	initTheme,
	loadThemeFromPath,
	setRegisteredThemes,
	setTheme,
	setThemeInstance,
	theme,
} from "../src/modes/interactive/theme/theme.ts";
import { InteractiveThemeController } from "../src/modes/interactive/theme/theme-controller.ts";

// /theme previews each theme while the list is scrolled: the TUI keeps the lines it rendered (change reports; tool rows no
// longer sample their background each frame), so a preview must redraw every visible line. After each preview, what the
// terminal shows (characters, colors, attributes) must be what the same screen shows when drawn from nothing in that theme,
// in fullscreen and in regular mode; Esc must give back the screen as it was.

const COLUMNS = 100;
const ROWS = 40;
const DOWN = "\x1b[B";
const UP = "\x1b[A";

function assistant(text: string, thinking: string, toolCall: boolean): AssistantMessage {
	return {
		role: "assistant",
		content: [
			...(thinking ? [{ type: "thinking" as const, thinking }] : []),
			{ type: "text", text },
			...(toolCall ? [{ type: "toolCall" as const, id: "call", name: "bash", arguments: { command: "ls" } }] : []),
		],
		api: "openai-completions",
		provider: "fake",
		model: "fake-model",
		usage: {
			input: 0,
			output: 0,
			cacheRead: 0,
			cacheWrite: 0,
			totalTokens: 0,
			cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
		},
		stopReason: toolCall ? "toolUse" : "stop",
		timestamp: 0,
	};
}

type Mode = "fullscreen" | "regular";

/** The transcript and dock as InteractiveMode lays them out, with the theme selector in place of the editor. */
function buildScreen(mode: Mode, terminal: VirtualTerminal, original: string, selectedIndex: number | undefined) {
	const ui = createInteractiveTui({ tuiMode: mode, showHardwareCursor: false, logDirectory: tmpdir(), terminal });
	const transcript = new TranscriptContainer();
	const shell = createShellRenderers("$");
	for (let round = 0; round < 3; round++) {
		transcript.addChild(new UserMessageComponent(`List the files, round ${round}, and show **some** code`));
		transcript.addChild(
			new AssistantMessageComponent(
				assistant(
					`Here is \`inline\` code and a block:\n\n\`\`\`ts\nconst round = ${round};\nexport function f() { return "x"; }\n\`\`\`\n\n> a quote with a [link](https://example.com)\n\n- one\n- two`,
					"Thinking about which files to list",
					true,
				),
				false,
			),
		);
		const ok = new ToolExecutionComponent("bash", `ok${round}`, { command: "ls -la" }, {}, shell, ui, "/tmp");
		ok.updateResult({ content: [{ type: "text", text: "a.txt\nb.txt\nsrc/" }], isError: false }, false);
		transcript.addChild(ok);
		const failed = new ToolExecutionComponent(
			"bash",
			`err${round}`,
			{ command: "cat missing" },
			{},
			shell,
			ui,
			"/tmp",
		);
		failed.updateResult({ content: [{ type: "text", text: "cat: missing: No such file" }], isError: true }, false);
		transcript.addChild(failed);
		const running = new ToolExecutionComponent("bash", `run${round}`, { command: "sleep 1" }, {}, shell, ui, "/tmp");
		transcript.addChild(running);
	}
	const document = new Container();
	document.addChild(new ThemedText(() => theme.fg("accent", "upi header")));
	document.addChild(transcript);
	const editor = new Container();
	editor.addChild(new DynamicBorder());
	editor.addChild(new Text("the editor", 1, 0));
	editor.addChild(new DynamicBorder());
	const editorContainer = new Container();
	const footer = new ThemedText(() => `${theme.fg("dim", "~/project")} ${theme.fg("success", "fake-model")}`, 0, 0);
	const footerContainer = new Container();
	footerContainer.addChild(footer);
	const pending = new Container();
	const status = new Container();
	const components: Component[] = [document, pending, status, editorContainer, footerContainer];
	for (const component of components) ui.addChild(component);
	if (isViewportTUI(ui)) {
		ui.setLayoutRoot(
			createChatViewport({
				document,
				pendingMessages: pending,
				status,
				editor: editorContainer,
				footer: footerContainer,
			}).root,
		);
	}
	let selector: ThemeSelectorComponent | undefined;
	if (selectedIndex !== undefined) {
		selector = new ThemeSelectorComponent(
			original,
			() => {},
			() => {},
			() => {},
		);
		selector.getSelectList().setSelectedIndex(selectedIndex);
		editorContainer.addChild(selector);
		ui.setFocus(selector.getSelectList());
	} else {
		editorContainer.addChild(editor);
	}
	return { ui, editor, editorContainer };
}

/** The screen drawn from nothing in the current theme. */
async function reference(mode: Mode, original: string, selectedIndex: number | undefined): Promise<string[][]> {
	const terminal = new VirtualTerminal(COLUMNS, ROWS);
	const { ui } = buildScreen(mode, terminal, original, selectedIndex);
	ui.start();
	try {
		await terminal.waitForRender();
		return terminal.getStyledViewport();
	} finally {
		ui.stop();
	}
}

async function settle(terminal: VirtualTerminal): Promise<void> {
	await terminal.waitForRender();
	await new Promise((resolve) => setTimeout(resolve, 60));
	await terminal.flush();
}

/** Opens /theme's selector as InteractiveMode.showThemeSelector() does, on a live screen. */
function openSelector(
	live: ReturnType<typeof buildScreen>,
	controller: InteractiveThemeController,
	original: string,
	onClose: () => void,
): ThemeSelectorComponent {
	const restore = controller.previewRestorer();
	const done = () => {
		live.editorContainer.clear();
		live.editorContainer.addChild(live.editor);
		onClose();
	};
	const selector = new ThemeSelectorComponent(
		original,
		() => done(),
		() => {
			done();
			restore();
		},
		(name) => controller.preview(name),
	);
	live.editorContainer.clear();
	live.editorContainer.addChild(selector);
	live.ui.setFocus(selector.getSelectList());
	live.ui.requestRender();
	return selector;
}

describe("/theme preview", () => {
	let tempRoot: string;

	beforeEach(() => {
		tempRoot = mkdtempSync(join(tmpdir(), "pi-theme-preview-"));
		vi.stubEnv("PI_CODING_AGENT_DIR", join(tempRoot, "agent"));
		mkdirSync(join(tempRoot, "agent", "themes"), { recursive: true });
		setRegisteredThemes([]);
	});

	afterEach(() => {
		setRegisteredThemes([]);
		initTheme("dark");
		rmSync(tempRoot, { recursive: true, force: true });
		vi.unstubAllEnvs();
	});

	for (const mode of ["fullscreen", "regular"] as const) {
		test(`every preview redraws every visible line, and Esc restores the screen (${mode})`, async () => {
			const original = "dark";
			initTheme(original);
			const terminal = new VirtualTerminal(COLUMNS, ROWS);
			const live = buildScreen(mode, terminal, original, undefined);
			const controller = new InteractiveThemeController(live.ui, {
				getSettingsManager: () => ({ getThemeSetting: () => original }) as never,
				showError: () => {},
				onChanged: () => {},
				initialThemeSetting: original,
			});
			live.ui.start();
			try {
				await settle(terminal);
				const before = terminal.getStyledViewport();
				expect(before).toEqual(await reference(mode, original, undefined));

				let closed = false;
				const selector = openSelector(live, controller, original, () => {
					closed = true;
				});
				await settle(terminal);
				const themes = getAvailableThemes();
				expect(themes.length).toBeGreaterThanOrEqual(19);
				let index = themes.indexOf(original);
				// Down to the last theme, then up to the first: every theme is previewed at least once.
				const keys = [
					...Array.from({ length: themes.length - 1 - index }, () => DOWN),
					...Array.from({ length: themes.length - 1 }, () => UP),
				];
				let changed = 0;
				for (const key of keys) {
					terminal.sendInput(key);
					index += key === DOWN ? 1 : -1;
					await settle(terminal);
					expect(selector.getSelectList().getSelectedItem()?.value).toBe(themes[index]);
					const shown = terminal.getStyledViewport();
					expect(shown, `${mode}: preview of ${themes[index]}`).toEqual(await reference(mode, original, index));
					if (JSON.stringify(shown) !== JSON.stringify(before)) changed++;
				}
				// (Not a screen that never changes: the previews recolor it.)
				expect(changed).toBeGreaterThan(keys.length / 2);

				terminal.sendInput("\x1b");
				await settle(terminal);
				await new Promise((resolve) => setTimeout(resolve, 100));
				await settle(terminal);
				expect(closed).toBe(true);
				expect(terminal.getStyledViewport()).toEqual(before);
			} finally {
				controller.dispose();
				live.ui.stop();
			}
		});
	}

	test("Esc gives back a theme an extension set as an instance", async () => {
		const original = "dark";
		initTheme(original);
		const terminal = new VirtualTerminal(COLUMNS, ROWS);
		const live = buildScreen("fullscreen", terminal, original, undefined);
		const controller = new InteractiveThemeController(live.ui, {
			getSettingsManager: () => ({ getThemeSetting: () => original }) as never,
			showError: () => {},
			onChanged: () => {},
			initialThemeSetting: original,
		});
		live.ui.start();
		try {
			// As ctx.ui.setTheme(theme) does: a theme that is no setting.
			const instance = loadThemeFromPath(
				fileURLToPath(new URL("../src/modes/interactive/theme/gruvbox-light.json", import.meta.url)),
			);
			controller.setThemeInstance(instance);
			await settle(terminal);
			const before = terminal.getStyledViewport();
			openSelector(live, controller, original, () => {});
			await settle(terminal);
			terminal.sendInput(DOWN);
			await settle(terminal);
			expect(getThemeInstance()).not.toBe(instance);
			terminal.sendInput("\x1b");
			await new Promise((resolve) => setTimeout(resolve, 100));
			await settle(terminal);
			expect(getThemeInstance()).toBe(instance);
			expect(terminal.getStyledViewport()).toEqual(before);
		} finally {
			controller.dispose();
			live.ui.stop();
		}
	});

	test("a user theme named like a built-in wins: listed once, previewed and applied in its colors", async () => {
		const builtinPath = fileURLToPath(new URL("../src/modes/interactive/theme/nord.json", import.meta.url));
		const builtin = JSON.parse(readFileSync(builtinPath, "utf-8")) as { colors: Record<string, string | number> };
		const customPath = join(tempRoot, "agent", "themes", "nord.json");
		writeFileSync(
			customPath,
			JSON.stringify({
				...builtin,
				colors: { ...builtin.colors, accent: "#ff00ff", toolSuccessBg: "#004400", userMessageBg: "#440044" },
			}),
		);
		// As at startup: the resource loader loads the user's themes and InteractiveMode registers them.
		setRegisteredThemes([loadThemeFromPath(customPath)]);
		expect(getAvailableThemes().filter((name) => name === "nord")).toEqual(["nord"]);

		const original = "dark";
		initTheme(original);
		const terminal = new VirtualTerminal(COLUMNS, ROWS);
		const live = buildScreen("fullscreen", terminal, original, undefined);
		const controller = new InteractiveThemeController(live.ui, {
			getSettingsManager: () => ({ getThemeSetting: () => original }) as never,
			showError: () => {},
			onChanged: () => {},
			initialThemeSetting: original,
		});
		live.ui.start();
		try {
			await settle(terminal);
			const selector = openSelector(live, controller, original, () => {});
			await settle(terminal);
			const themes = getAvailableThemes();
			const nord = themes.indexOf("nord");
			const from = themes.indexOf(original);
			for (let i = from; i !== nord; i += nord > from ? 1 : -1) {
				terminal.sendInput(nord > from ? DOWN : UP);
				await settle(terminal);
			}
			expect(selector.getSelectList().getSelectedItem()?.value).toBe("nord");
			const shown = terminal.getStyledViewport();

			setThemeInstance(loadThemeFromPath(customPath));
			const custom = await reference("fullscreen", original, nord);
			setThemeInstance(loadThemeFromPath(builtinPath));
			const builtinScreen = await reference("fullscreen", original, nord);
			expect(shown).toEqual(custom);
			expect(shown).not.toEqual(builtinScreen);

			expect(setTheme("nord").success).toBe(true);
			expect(theme.fg("accent", "x")).toBe(loadThemeFromPath(customPath).fg("accent", "x"));
		} finally {
			controller.dispose();
			live.ui.stop();
		}
	});
});

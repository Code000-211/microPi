import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { parseArgs } from "../src/cli/args.ts";
import { SettingsManager } from "../src/core/settings-manager.ts";
import { main } from "../src/main.ts";
import { getPiBoltPackageFlagError, handleConfigCommand, handlePackageCommand } from "../src/package-manager-cli.ts";

const build = vi.hoisted(() => ({ piBolt: true }));
vi.mock("../src/upi.ts", async (importOriginal) => ({
	...(await importOriginal<object>()),
	get PIBOLT() {
		return build.piBolt ? { version: "0.8.4", variant: "arm64", jit: false } : undefined;
	},
}));

let originalExitCode: typeof process.exitCode;

beforeEach(() => {
	originalExitCode = process.exitCode;
	process.exitCode = undefined;
	vi.spyOn(console, "error").mockImplementation(() => {});
	vi.spyOn(console, "log").mockImplementation(() => {});
	vi.spyOn(SettingsManager, "create").mockImplementation(() => {
		throw new Error("Preflight must not access settings");
	});
});

afterEach(() => {
	process.exitCode = originalExitCode;
	vi.restoreAllMocks();
});

// #28: package selector preflight must reuse startup argument consumption, not scan option values.
describe.each([true, false])("getPiBoltPackageFlagError (µPi=%s)", (piBolt) => {
	beforeEach(() => {
		build.piBolt = piBolt;
	});

	test.each([
		"--name",
		"-n",
		"--provider",
		"--model",
		"--api-key",
		"--system-prompt",
		"--append-system-prompt",
		"--session",
		"--session-id",
		"--fork",
		"--session-dir",
		"--models",
		"--tools",
		"-t",
		"--exclude-tools",
		"-xt",
		"--thinking",
		"--export",
		"--extension",
		"-e",
		"--skill",
		"--prompt-template",
		"--theme",
	])("preserves selector-like values of %s", (option) => {
		for (const value of ["--upi", "--upi=true", "--upi="]) {
			const args = [option, value, "--version"];
			expect(parseArgs(args).unknownFlags.has("upi")).toBe(false);
			expect(getPiBoltPackageFlagError(args)).toBeUndefined();
		}
	});

	test("preserves the reported session-name example and repeated literal prompts", () => {
		const args = [
			"--name",
			"--upi",
			"--system-prompt",
			"--upi=false",
			"--append-system-prompt",
			"--upi",
			"--append-system-prompt",
			"--upi=true",
			"--version",
		];
		expect(parseArgs(args)).toMatchObject({
			name: "--upi",
			systemPrompt: "--upi=false",
			appendSystemPrompt: ["--upi", "--upi=true"],
			version: true,
			diagnostics: [],
		});
		expect(getPiBoltPackageFlagError(args)).toBeUndefined();
	});

	test.each([
		["--", "--upi", "--upi=true"],
		["--name", "--upi", "--", "--upi=true"],
		["--print", "--", "--upi"],
		["--print", "---literal --upi"],
		["@--upi"],
		["--upi-other"],
	])("does not treat positional or unrelated tokens as selectors: %j", (...args) => {
		expect(getPiBoltPackageFlagError(args)).toBeUndefined();
	});

	// #28: real selectors still fail at main's preflight, before settings reads or migrations.
	test.each([
		["--upi"],
		["--upi=true"],
		["--upi=", "--upi"],
		["--upi", "--upi=false"],
		["--name", "--upi=true", "--upi"],
		["--name", "--", "--upi"],
		["--mode", "--upi"],
		["--use-theme", "--upi"],
		["--tui-mode", "--upi"],
		["--list-models", "--upi"],
		["--print", "--upi"],
		["--custom-extension-flag", "--upi"],
		["auth", "--upi"],
		["mcp", "--upi"],
	])("rejects actual unsupported selector tokens: %j", async (...args) => {
		const expected = piBolt
			? "--upi is only supported by install, remove, and uninstall"
			: "--upi requires a µPi build";
		expect(getPiBoltPackageFlagError(args)).toBe(expected);
		await main(args);
		expect(process.exitCode).toBe(1);
		expect(console.error).toHaveBeenCalledWith(expect.stringContaining(expected));
		expect(SettingsManager.create).not.toHaveBeenCalled();
	});

	// #28: startup value handling must not relax package-command selector validation or help precedence.
	test.each(["install", "remove", "uninstall"])("preserves %s selector grammar", async (command) => {
		const args = [command, "npm:fixture", "--upi", "--help"];
		expect(getPiBoltPackageFlagError(args)).toBe(piBolt ? undefined : "--upi requires a µPi build");
		expect(await handlePackageCommand(args)).toBe(true);
		expect(process.exitCode).toBe(piBolt ? undefined : 1);
		expect(SettingsManager.create).not.toHaveBeenCalled();
	});

	test.each([
		["install", "npm:fixture", "--upi=true"],
		["install", "npm:fixture", "--upi", "--upi=false"],
		["remove", "npm:fixture", "--upi=", "--upi"],
		["update", "--extension", "--upi=true"],
	])("rejects every valued package selector: %j", async (...args) => {
		expect(getPiBoltPackageFlagError(args)).toBe(
			piBolt ? "--upi does not take a value" : "--upi requires a µPi build",
		);
		await handlePackageCommand(args);
		expect(process.exitCode).toBe(1);
		expect(SettingsManager.create).not.toHaveBeenCalled();
	});

	test.each(["list", "update", "config"])("rejects unsupported %s selectors before settings", async (command) => {
		const args = command === "update" ? [command, "--extension", "--upi"] : [command, "--upi"];
		expect(getPiBoltPackageFlagError(args)).toBe(
			piBolt ? "--upi is only supported by install, remove, and uninstall" : "--upi requires a µPi build",
		);
		if (command === "config") await handleConfigCommand(args);
		else await handlePackageCommand(args);
		expect(process.exitCode).toBe(1);
		expect(SettingsManager.create).not.toHaveBeenCalled();
	});

	test("preserves the package preflight cutoff without adding package end-of-options support", async () => {
		const args = ["install", "npm:fixture", "--", "--upi=true"];
		expect(getPiBoltPackageFlagError(args)).toBeUndefined();
		await handlePackageCommand(args);
		expect(process.exitCode).toBe(1);
		expect(console.error).toHaveBeenCalledWith(expect.stringContaining('Unknown option -- for "install".'));
		expect(SettingsManager.create).not.toHaveBeenCalled();
	});
});

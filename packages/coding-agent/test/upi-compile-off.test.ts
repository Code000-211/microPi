import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type * as PiBolt from "../src/upi.ts";
import { runPiBoltCompile } from "../src/upi-compile.ts";

// A µPi build (scripts/build-pi.sh defines PIBOLT_BUILD); the tests run as Pi otherwise. (vi.mock runs before the imports.)
vi.mock("../src/upi.ts", async (importOriginal) => ({
	...(await importOriginal<typeof PiBolt>()),
	PIBOLT: { version: "0.9.1", variant: "arm64", jit: false },
}));

const realExecPath = process.execPath;
let install: string;

beforeEach(() => {
	install = mkdtempSync(join(tmpdir(), "pibolt-off-"));
	mkdirSync(join(install, "upi-linux-x64"));
	Object.defineProperty(process, "execPath", { value: join(install, "upi-linux-x64", "pi"), configurable: true });
	writeFileSync(join(install, "compile.json"), JSON.stringify({ version: "0.9.1", extensions: ["word-count"] }));
});

afterEach(() => {
	Object.defineProperty(process, "execPath", { value: realExecPath, configurable: true });
	rmSync(install, { recursive: true, force: true });
});

describe.skipIf(process.platform === "win32")("upi compile --off", () => {
	test("keeps what was compiled in when the release could not be installed again", async () => {
		expect(await runPiBoltCompile(["--off"], async () => false)).toBe(1);
		expect(existsSync(join(install, "compile.json"))).toBe(true);
	});

	test("forgets it once the release is installed again", async () => {
		expect(await runPiBoltCompile(["--off"], async () => true)).toBe(0);
		expect(existsSync(join(install, "compile.json"))).toBe(false);
	});
});

import type * as Fs from "node:fs";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getPowerShellConfig, POWERSHELL_ARGS } from "../src/utils/shell.ts";
import type * as WindowsPath from "../src/utils/windows-path.ts";

const { accessSyncMock } = vi.hoisted(() => ({
	accessSyncMock: vi.fn(),
}));

vi.mock("node:fs", async (importOriginal) => ({
	...(await importOriginal<typeof Fs>()),
	accessSync: accessSyncMock,
}));

// µPi looks shells up in PATH's directories itself (not with `where`, which also searches the working directory): a PATH
// entry counts when the existence check passes for the file in it.
vi.mock("../src/utils/windows-path.ts", async (importOriginal) => ({
	...(await importOriginal<typeof WindowsPath>()),
	isProgramFile: (file: string) => {
		try {
			accessSyncMock(file);
			return true;
		} catch {
			return false;
		}
	},
}));

const platformDescriptor = Object.getOwnPropertyDescriptor(process, "platform")!;
const pwsh7 = "D:\\Applications\\PowerShell\\7\\pwsh.exe";
const windowsPowerShell = "E:\\WinNT\\System32\\WindowsPowerShell\\v1.0\\powershell.exe";

beforeEach(() => {
	Object.defineProperty(process, "platform", { configurable: true, value: "win32" });
	vi.stubEnv("ProgramFiles", "D:\\Applications");
	vi.stubEnv("SystemRoot", "E:\\WinNT");
	vi.stubEnv("PATH", "");
	accessSyncMock.mockReset().mockImplementation(() => {
		throw Object.assign(new Error("not found"), { code: "ENOENT" });
	});
});

afterEach(() => {
	Object.defineProperty(process, "platform", platformDescriptor);
	vi.unstubAllEnvs();
});

// #9490: discover PowerShell outside PATH without assuming a drive or Windows directory name.
describe("getPowerShellConfig", () => {
	it("prefers PowerShell 7 on PATH over known installation locations", () => {
		const shell = "E:\\Tools\\pwsh.exe";
		vi.stubEnv("PATH", "E:\\Tools");
		accessSyncMock.mockReturnValue(undefined);

		expect(getPowerShellConfig()).toEqual({ shell, args: [...POWERSHELL_ARGS] });
	});

	it("finds PowerShell 7 under ProgramFiles before Windows PowerShell on PATH", () => {
		vi.stubEnv("PATH", "E:\\WinNT\\System32\\WindowsPowerShell\\v1.0");
		accessSyncMock.mockImplementation((path: string) => {
			if (![pwsh7, windowsPowerShell].includes(path as string)) {
				throw Object.assign(new Error("not found"), { code: "ENOENT" });
			}
		});

		expect(getPowerShellConfig()).toEqual({ shell: pwsh7, args: [...POWERSHELL_ARGS] });
	});

	it("prefers Windows PowerShell on PATH over its system installation", () => {
		const shell = "E:\\Tools\\powershell.exe";
		vi.stubEnv("PATH", "E:\\Tools");
		accessSyncMock.mockImplementation((path: string) => {
			if (![shell, windowsPowerShell].includes(path as string)) {
				throw Object.assign(new Error("not found"), { code: "ENOENT" });
			}
		});

		expect(getPowerShellConfig()).toEqual({ shell, args: [...POWERSHELL_ARGS] });
	});

	it("finds Windows PowerShell under SystemRoot when PATH has no PowerShell", () => {
		accessSyncMock.mockImplementation((path: string) => {
			if (path !== windowsPowerShell) {
				throw Object.assign(new Error("not found"), { code: "ENOENT" });
			}
		});

		expect(getPowerShellConfig()).toEqual({ shell: windowsPowerShell, args: [...POWERSHELL_ARGS] });
	});

	it("reports an error when no PowerShell installation exists", () => {
		expect(() => getPowerShellConfig()).toThrow("No PowerShell executable found");
	});
});

import { homedir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, test } from "vitest";
import { piBoltUpdateEnvironment } from "../src/upi.ts";

const realExecPath = process.execPath;
const realInstall = process.env.MICROPI_INSTALL;

function runningFrom(execPath: string): void {
	Object.defineProperty(process, "execPath", { value: execPath, configurable: true });
}

afterEach(() => {
	runningFrom(realExecPath);
	if (realInstall === undefined) delete process.env.MICROPI_INSTALL;
	else process.env.MICROPI_INSTALL = realInstall;
});

describe("upi update installs where µPi is", () => {
	test("an installation in the home directory", () => {
		delete process.env.MICROPI_INSTALL;
		runningFrom(join(homedir(), ".micropi", "upi-linux-x64", "pi"));
		expect(piBoltUpdateEnvironment("0.8.0").MICROPI_INSTALL).toBe(join(homedir(), ".micropi"));
	});

	test("an installation outside it, such as /opt/upi", () => {
		delete process.env.MICROPI_INSTALL;
		runningFrom("/opt/upi/upi-darwin-arm64/pi-bin");
		expect(piBoltUpdateEnvironment("0.8.0").MICROPI_INSTALL).toBe("/opt/upi");
	});

	test("a build run from its output folder gets the default installation", () => {
		delete process.env.MICROPI_INSTALL;
		runningFrom("/home/me/Pi-Bolt/out/upi/pi");
		expect(piBoltUpdateEnvironment("0.8.0").MICROPI_INSTALL).toBe(join(homedir(), ".micropi"));
	});

	// The installation that is running is the one updated: a MICROPI_INSTALL left in the environment would put a second copy
	// elsewhere and leave this one as it was. It is used for an executable that is not in an installation.
	test("the running installation wins over MICROPI_INSTALL", () => {
		process.env.MICROPI_INSTALL = "/srv/tools/upi";
		runningFrom("/opt/upi/upi-linux-x64/pi");
		expect(piBoltUpdateEnvironment("0.8.0").MICROPI_INSTALL).toBe("/opt/upi");
	});

	test("MICROPI_INSTALL, for a build run from its output folder", () => {
		process.env.MICROPI_INSTALL = "/srv/tools/upi";
		runningFrom("/home/me/Pi-Bolt/out/upi/pi");
		expect(piBoltUpdateEnvironment("0.8.0").MICROPI_INSTALL).toBe("/srv/tools/upi");
	});
});

import { restoreSandboxEnv } from "./restore-sandbox-env.ts";

// Restore the environment before evaluating modules that read it at startup.
restoreSandboxEnv();

// Windows: a program named without a path is looked up on PATH only, never in the working directory first. Bun's spawn looks
// there unless this is set, and takes a .cmd there too, so a repository opened in Pi could have its own git.cmd or npm.cmd run
// for a bare `git` or `npm` (Pi's own calls, and extensions' pi.exec()). Programs started from here inherit it, so the shells
// the tools run (cmd.exe) do not look there either; a path such as .\build.cmd still runs what it names.
if (process.platform === "win32" && !process.env.NoDefaultCurrentDirectoryInExePath) {
	process.env.NoDefaultCurrentDirectoryInExePath = "1";
}

// A µPi executable starts its runtime with --smol (scripts/build-pi.*: --compile-exec-argv), which is in effect before any
// of this runs. It is not an argument of Pi's, nor one a program started as `process.execPath ...process.execArgv` (fork(), an
// extension starting Pi again) accepts: "Unknown option: --smol". Left out of execArgv, as a program run by Bun sees it.
const smol = process.execArgv.indexOf("--smol");
if (smol !== -1) process.execArgv.splice(smol, 1);

// For tests/termux/run.sh: what µPi in Termux gives the programs it starts (Termux's LD_PRELOAD, back, and after it an empty
// GLIBC_LD_PRELOAD, for a glibc program among them), its time zone, and that its event loop, timers and child processes work under
// Android's system call filter.
import { execFileSync, spawn } from "node:child_process";

export default function () {
	const sync = execFileSync(process.env.SHOW_PRELOAD, { encoding: "utf8" });
	const child = spawn(process.env.SHOW_PRELOAD, { stdio: ["ignore", "pipe", "inherit"] });
	let async = "";
	child.stdout.on("data", (data) => {
		async += data;
	});
	child.on("close", () => {
		setTimeout(() => {
			const names = Object.keys(process.env);
			const glibc = process.env.GLIBC_LD_PRELOAD === "" && names.indexOf("GLIBC_LD_PRELOAD") > names.indexOf("LD_PRELOAD");
			console.log(`termux-env: sync=${sync} async=${async} glibc-after=${glibc} tz=${process.env.TZ ?? ""}`);
			process.exit(0);
		}, 20);
	});
}

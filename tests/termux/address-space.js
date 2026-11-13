// For tests/termux/run.sh: where µPi put what it maps at fixed addresses, which must be within the 512 GB of addresses a process
// has on Android: its prebuilt heap and code, from its own file (above its ELF segments, at 2 MB), and the Structures.
import { readFileSync } from "node:fs";

export default function () {
	let fromFile = 0;
	let highest = 0n;
	let structures = false;
	for (const line of readFileSync("/proc/self/maps", "utf8").split("\n")) {
		const [range, , , , , path] = line.trim().split(/\s+/);
		if (!range) continue;
		const [start, end] = range.split("-").map((hex) => BigInt(`0x${hex}`));
		// (The Structures: right after the static region, which is at 64 GB and 32 GB long.)
		if (start === 0x1800000000n) structures = true;
		if (path !== process.execPath || start < 0x100000000n) continue;
		fromFile++;
		if (end > highest) highest = end;
	}
	console.log(`address-space: from-file=${fromFile} highest=0x${highest.toString(16)} structures=${structures}`);
	process.exit(0);
}

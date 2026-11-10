// `pi --version` (or -v) alone is answered here, before the rest of Pi is loaded: its modules' top-level code, which runs at
// every start, is two thirds of what that command costs. Anything else on the command line goes to main() as before.
import { VERSION } from "../config.ts";
import { MICROPI_PI, PIBOLT } from "../upi.ts";

const args = process.argv.slice(2);
if (args.length === 1 && (args[0] === "--version" || args[0] === "-v")) {
	console.log(
		PIBOLT
			? `µPi ${VERSION} (Pi ${MICROPI_PI ?? PIBOLT.version}, ${process.platform}-${PIBOLT.variant}, JIT ${PIBOLT.jit ? "on" : "off"})`
			: VERSION,
	);
	process.exit(0);
}

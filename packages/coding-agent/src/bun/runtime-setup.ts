import { bedrockProviderModule } from "@earendil-works/pi-ai/bedrock-provider";
import { registerBunOAuthFlows } from "@earendil-works/pi-ai/bun-oauth";
import { setBedrockProviderModule } from "@earendil-works/pi-ai/compat";
// Bun loads .wasm imports as files: embedded in compiled executables, evaluating to a readable path.
import quickjsWasmPath from "quickjs-wasi/quickjs.wasm";
import { APP_NAME, setEmbeddedQuickJSWasmPath } from "../config.ts";

process.title = APP_NAME;
// Termux: µPi runs with Termux's glibc, started without Termux's LD_PRELOAD (a library of Android's libc, which glibc's loader
// cannot load) by its launcher (install.sh), which keeps it in PIBOLT_TERMUX_LD_PRELOAD. The programs Pi starts get it back
// (Termux's shebang and path handling), and after it an empty GLIBC_LD_PRELOAD, which Termux's glibc reads as LD_PRELOAD (the
// later of the two counts): a glibc program among them, µPi itself, starts without it.
if (process.env.PIBOLT_TERMUX_LD_PRELOAD !== undefined) {
	process.env.LD_PRELOAD = process.env.PIBOLT_TERMUX_LD_PRELOAD;
	delete process.env.GLIBC_LD_PRELOAD;
	process.env.GLIBC_LD_PRELOAD = "";
}
process.emitWarning = (() => {}) as typeof process.emitWarning;
registerBunOAuthFlows();
setBedrockProviderModule(bedrockProviderModule);
setEmbeddedQuickJSWasmPath(quickjsWasmPath);

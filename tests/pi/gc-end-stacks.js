// A Pi extension that keeps Errors whose top frame dies, with Pi's own ahead-of-time compiled frames (the extension loader)
// below them, then collects. JSC formats such an Error's stack at the end of the collection
// (ErrorInstance::reconcileWeakReferencesAtGCEnd), where a frame of code compiled ahead of time has no CodeBlock and none can be
// made. Bun's formatter dereferenced it: "Segmentation fault at address 0x38" (upi 0.3.0 and 0.4.0, every run).
export default function () {
	const errors = [];
	for (let i = 0; i < 200; i++) {
		let make = new Function(`return new Error("kept ${i}")`);
		errors.push(make());
		make = null;
	}
	for (let i = 0; i < 5; i++) Bun.gc(true);
	const frames = errors[0].stack.split("\n").length;
	console.log(`gc-end-stacks: ${errors.length} errors, ${frames} lines in the first stack`);
	process.exit(0);
}

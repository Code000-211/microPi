import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { CHECKERS, onPath, radisaDiagnostics } from "../src/core/checks/checkers.ts";

const rdna4 = CHECKERS.find((c) => c.name === "rdna4-isa")!;
/** A checkout of the suite, for the test that runs radisa itself. */
const suite = process.env.PI_RADISA;

describe("rdna4-isa checker", () => {
	let dir: string;
	const saved = { ...process.env };

	beforeEach(() => {
		dir = mkdtempSync(join(tmpdir(), "pi-rdna4-"));
		delete process.env.PI_RADISA;
		delete process.env.HIP_OFFLOAD_ARCH;
		delete process.env.HCC_AMDGPU_TARGET;
	});

	afterEach(() => {
		rmSync(dir, { recursive: true, force: true });
		process.env = { ...saved };
	});

	/** A project with the suite under tools/ and a compile_commands.json building `file` for `arch`. */
	function project(file: string, arch: string): string {
		mkdirSync(join(dir, "tools", "rdna4-isa-suite", "radisa"), { recursive: true });
		writeFileSync(join(dir, "tools", "rdna4-isa-suite", "radisa", "__main__.py"), "");
		mkdirSync(join(dir, "build"));
		writeFileSync(
			join(dir, "build", "compile_commands.json"),
			JSON.stringify([{ directory: dir, file, command: `clang++ -x hip --offload-arch=${arch} -c ${file}` }]),
		);
		const path = join(dir, file);
		writeFileSync(path, "");
		return path;
	}

	it("takes HIP units built for gfx12 and leaves other targets to the C/C++ checks", () => {
		if (!onPath("python3")) return;
		expect(rdna4.unit(project("k.hip", "gfx1201"))?.key).toBe(`rdna4-isa:${join(dir, "k.hip")}`);
		rmSync(join(dir, "build"), { recursive: true });
		expect(rdna4.unit(project("halo.hip", "gfx1151"))).toBeUndefined();
	});

	it("needs the suite", () => {
		const path = join(dir, "k.hip");
		writeFileSync(path, "");
		expect(rdna4.unit(path)).toBeUndefined();
		process.env.PI_RADISA = join(dir, "missing");
		expect(rdna4.unit(path)).toBeUndefined();
	});

	it("takes .s files only when they are AMDGPU assembly", () => {
		const amd = join(dir, "k.s");
		writeFileSync(amd, "global_load_b32 v1, v[2:3], off\ns_endpgm\n");
		const x86 = join(dir, "x.s");
		writeFileSync(x86, "movq %rsp, %rbp\nret\n");
		expect(rdna4.matches(amd)).toBe(true);
		expect(rdna4.matches(x86)).toBe(false);
	});

	it("maps errors to errors and warnings to lint, without volatile pcs or rules the agent cannot fix", () => {
		const file = "/p/k.hip";
		const diagnostics = radisaDiagnostics(
			{
				findings: [
					{ rule: "COMPILE", severity: "error", message: "device compile failed" },
					{ rule: "HIP_INLINE_ASM_MEMORY", severity: "review", message: "asm load", loc: { file, line: 14 } },
					{ rule: "HIP_NO_LAUNCH_BOUNDS", severity: "info", message: "no bounds", loc: { file, line: 3 } },
				],
				kernels: [
					{
						name: "racy",
						findings: [
							{
								rule: "MISSING_WAIT",
								severity: "error",
								message:
									"v_add_f32_e32 reads v1 while ds_load_b32 (k.hip:14) (pc 0x124) may still be outstanding",
								loc: { file, line: 15, column: 12 },
								instruction: "v_add_f32_e32 v1, 1.0, v1",
							},
							{ rule: "VOPD_ANTIDEP", severity: "review", message: "X writes v3", loc: { file, line: 9 } },
							{ rule: "REGISTER_SPILL", severity: "review", message: "2 VGPR spills", pc: null },
						],
					},
				],
			},
			file,
		);
		expect(diagnostics).toEqual([
			{ file, line: 14, column: 1, severity: "warning", message: "asm load", code: "HIP_INLINE_ASM_MEMORY" },
			{
				file,
				line: 15,
				column: 12,
				severity: "error",
				message:
					"v_add_f32_e32 reads v1 while ds_load_b32 (1 line above) may still be outstanding [v_add_f32_e32 v1, 1.0, v1]",
				code: "MISSING_WAIT",
			},
			{
				file,
				line: 1,
				column: 1,
				severity: "warning",
				message: "in kernel racy: 2 VGPR spills",
				code: "REGISTER_SPILL",
			},
		]);
	});

	it("keeps a finding's text when lines move: the instruction it names is given by its distance", () => {
		const file = "/p/k.hip";
		const at = (line: number, producer: number) =>
			radisaDiagnostics(
				{
					findings: [],
					kernels: [
						{
							name: "k",
							findings: [
								{
									rule: "MISSING_WAIT",
									severity: "error",
									message: `reads v1 while ds_load_b32 (k.hip:${producer}) and x (other.h:7) may be outstanding`,
									loc: { file, line },
								},
							],
						},
					],
				},
				file,
			)[0]!.message;
		expect(at(15, 14)).toBe("reads v1 while ds_load_b32 (1 line above) and x (other.h:7) may be outstanding");
		expect(at(25, 24)).toBe(at(15, 14));
		expect(at(15, 15)).toContain("(same line)");
		expect(at(15, 18)).toContain("(3 lines below)");
	});

	it("reports what radisa could not check as lint, and names a function as one", () => {
		const file = "/p/k.hip";
		const diagnostics = radisaDiagnostics(
			{
				findings: [{ rule: "CHECK_FAILED", severity: "incomplete", message: "no gfx1201 compiler found" }],
				kernels: [
					{
						name: "k",
						findings: [{ rule: "BYTE_COVERAGE", severity: "incomplete", message: "not every byte decoded" }],
					},
					{
						name: "helper",
						callee: true,
						findings: [
							{ rule: "CALLEE_RETURN_OUTSTANDING", severity: "review", message: "returns with a load pending" },
						],
					},
				],
			},
			file,
		);
		expect(diagnostics.map((d) => [d.severity, d.code, d.message])).toEqual([
			["warning", "CHECK_FAILED", "not checked: no gfx1201 compiler found"],
			["warning", "BYTE_COVERAGE", "in kernel k: not checked: not every byte decoded"],
			["warning", "CALLEE_RETURN_OUTSTANDING", "in function helper: returns with a load pending"],
		]);
	});

	it("leaves the s_wait_alu rules out for a compiler older than LLVM 20, and says so once", () => {
		const report = (compiler: string) => ({
			findings: [],
			kernels: [
				{
					name: "k",
					findings: [
						{
							rule: "MISSING_ALU_WAIT",
							severity: "error",
							message: "carry read too early",
							loc: { file: "/p/k.hip", line: 4 },
						},
						{
							rule: "MISSING_ALU_WAIT",
							severity: "error",
							message: "carry read too early",
							loc: { file: "/p/k.hip", line: 9 },
						},
						{
							rule: "MISSING_WAIT",
							severity: "error",
							message: "load not waited for",
							loc: { file: "/p/k.hip", line: 5 },
						},
					],
				},
			],
			incremental: { compiler },
		});
		const old = radisaDiagnostics(report("Ubuntu clang version 18.1.3 (1ubuntu1)"), "/p/k.hip");
		expect(old.map((d) => [d.severity, d.code])).toEqual([
			["error", "MISSING_WAIT"],
			["warning", "RADISA_OLD_COMPILER"],
		]);
		expect(old[1]!.message).toContain("LLVM 18");
		const current = radisaDiagnostics(
			report("AMD clang version 24.0.0git (https://github.com/ROCm/llvm-project)"),
			"/p/k.hip",
		);
		expect(current.map((d) => d.code)).toEqual(["MISSING_ALU_WAIT", "MISSING_ALU_WAIT", "MISSING_WAIT"]);
		// Hand-written assembly has no compiler to wait for: its findings stay.
		const assembly = radisaDiagnostics({ ...report("Ubuntu clang version 18.1.3"), incremental: null }, "/p/k.s");
		expect(assembly.filter((d) => d.code === "MISSING_ALU_WAIT")).toHaveLength(2);
	});

	it("puts an assembler error of a .s file at its line, and does not blame HIP source for one", () => {
		const message = "/p/k.s:2:1: error: invalid instruction\nv_bogus v4, v1\n^";
		const finding = { rule: "ASSEMBLER", severity: "error", message };
		expect(radisaDiagnostics({ findings: [finding], kernels: [] }, "/p/k.s")).toEqual([
			{ file: "/p/k.s", line: 2, column: 1, severity: "error", message: "invalid instruction", code: "ASSEMBLER" },
		]);
		const hip = radisaDiagnostics({ findings: [finding], kernels: [] }, "/p/k.hip");
		expect(hip).toHaveLength(1);
		expect(hip[0]).toMatchObject({ file: "/p/k.hip", line: 1, severity: "warning" });
	});

	describe("running radisa", () => {
		/** A suite whose `radisa check` writes `report` (when given) and ends with `code`. */
		function fakeSuite(code: number, report?: object, stderr = ""): string {
			const suite = join(dir, "suite");
			mkdirSync(join(suite, "radisa"), { recursive: true });
			writeFileSync(join(suite, "radisa", "__init__.py"), "");
			writeFileSync(join(suite, "radisa", "__main__.py"), "");
			writeFileSync(join(suite, "radisa", "fake.json"), JSON.stringify({ code, report: report ?? null, stderr }));
			writeFileSync(
				join(suite, "radisa", "cli.py"),
				[
					"import json, os, sys",
					"def main(argv):",
					"    spec = json.load(open(os.path.join(os.path.dirname(__file__), 'fake.json')))",
					"    if spec['report'] is not None:",
					"        open(argv[argv.index('--json') + 1], 'w').write(json.dumps(spec['report']))",
					"    sys.stderr.write(spec['stderr'])",
					"    return spec['code']",
					"",
				].join("\n"),
			);
			process.env.PI_RADISA = suite;
			return suite;
		}

		async function check(): Promise<ReturnType<typeof radisaDiagnostics>> {
			const path = join(dir, "k.s");
			writeFileSync(path, "s_endpgm\n");
			return rdna4.run(rdna4.unit(path)!, new AbortController().signal);
		}

		const finding = { rule: "MISSING_WAIT", severity: "error", message: "load not waited for" };

		it.runIf(onPath("python3"))("reads the report also when parts were not checked (exit 3)", async () => {
			fakeSuite(3, {
				findings: [{ rule: "DECODE", severity: "incomplete", message: "undecodable bytes" }],
				kernels: [{ name: "k", findings: [finding] }],
			});
			expect((await check()).map((d) => [d.severity, d.code])).toEqual([
				["warning", "DECODE"],
				["error", "MISSING_WAIT"],
			]);
		});

		it.runIf(onPath("python3"))("says why when radisa did not start the check (exit 2)", async () => {
			fakeSuite(2, undefined, "usage: radisa check ...\nradisa: .radisa.json: unknown rule NOPE\n");
			const found = await check();
			expect(found).toHaveLength(1);
			expect(found[0]).toMatchObject({ severity: "warning", code: "RADISA_USAGE" });
			expect(found[0]!.message).toContain("unknown rule NOPE");
		});

		it.runIf(onPath("python3"))(
			"fails, keeping the findings before, when radisa was killed or left no report",
			async () => {
				fakeSuite(143);
				await expect(check()).rejects.toThrow(/143/);
				fakeSuite(1);
				await expect(check()).rejects.toThrow();
			},
		);

		it.runIf(onPath("python3"))("does not take a report an earlier run left for this one's", async () => {
			fakeSuite(1, { findings: [], kernels: [{ name: "k", findings: [finding] }] });
			expect(await check()).toHaveLength(1);
			fakeSuite(0, { findings: [], kernels: [] });
			expect(await check()).toEqual([]);
			fakeSuite(1);
			await expect(check()).rejects.toThrow();
		});
	});

	it("takes a unit that one of two compile commands builds for gfx12", () => {
		if (!onPath("python3")) return;
		const path = project("k.hip", "gfx1151");
		writeFileSync(
			join(dir, "build", "compile_commands.json"),
			JSON.stringify(
				["gfx1151", "gfx1201"].map((arch) => ({
					directory: dir,
					file: "k.hip",
					command: `clang++ -x hip --offload-arch=${arch} -c k.hip`,
				})),
			),
		);
		expect(rdna4.unit(path)?.key).toBe(`rdna4-isa:${path}`);
	});

	// Runs radisa itself when PI_RADISA names a checkout of the suite and llvm-mc is installed.
	it.runIf(suite && onPath("python3") && onPath("llvm-mc"))(
		"reports a load whose result is used before its wait",
		async () => {
			process.env.PI_RADISA = suite;
			const path = join(dir, "k.s");
			writeFileSync(path, "global_load_b32 v1, v[2:3], off\nv_add_f32 v4, v1, v1\ns_endpgm\n");
			const unit = rdna4.unit(path)!;
			const found = await rdna4.run(unit, new AbortController().signal);
			expect(found).toHaveLength(1);
			expect(found[0]).toMatchObject({ file: path, line: 2, severity: "error", code: "MISSING_WAIT" });
			expect(found[0]!.message).toContain("s_wait_loadcnt 0");
		},
		60_000,
	);
});

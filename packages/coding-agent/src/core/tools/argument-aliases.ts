/** The file tools' `path`, as Claude Code and Qwen Code name it. */
export const FILE_PATH_ALIAS = { file_path: "path" } as const;

/**
 * Renames arguments a model sent under the name another harness gives them: Claude Code and the harnesses modelled on
 * it (`file_path`, `old_string`, `run_in_background`), and Qwen Code (`absolute_path`, `include`, `is_background`). A
 * model trained with those names sends them here now and then, and the call fails validation for a missing `path`. A
 * tool passes its aliases from prepareArguments, which runs before validation, so extensions' tool_call handlers still
 * see only the tool's own names. Only names that mean the same are aliases: a different unit or format is not.
 *
 * `aliases` maps an alias to the tool's name. An argument sent under both names keeps the tool's own. The input is
 * returned as it is when nothing is renamed, and is never changed.
 */
export function renameArgumentAliases<T>(input: unknown, aliases: Readonly<Record<string, string>>): T {
	if (!input || typeof input !== "object" || Array.isArray(input)) {
		return input as T;
	}

	const args = input as Record<string, unknown>;
	let renamed: Record<string, unknown> | undefined;
	for (const [alias, name] of Object.entries(aliases)) {
		if (args[alias] === undefined || args[name] !== undefined) continue;
		renamed ??= { ...args };
		renamed[name] = args[alias];
		delete renamed[alias];
	}
	return (renamed ?? input) as T;
}

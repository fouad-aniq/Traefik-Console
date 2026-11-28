/** Indent a newly opened YAML block before its incomplete syntax has a value. */
export function yamlBlockIndent(beforeCursor: string): string | null {
	const block = /^( *)(- +)?(?:[^"'#:\[\]{}]+|"[^"]+"|'[^']+'):\s*(?:#.*)?$/.exec(beforeCursor);
	return block ? block[1] + ' '.repeat((block[2]?.length ?? 0) + 2) : null;
}

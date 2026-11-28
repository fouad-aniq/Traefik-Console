import { describe, it, expect } from 'vitest';
import { yamlTokens, diffLines, diffRows, collapseDiff } from './codeTokens';
describe('read-only review highlighting', () => {
	it('preserves YAML bytes, comments, nested keys, quotes and block scalars', () => {
		const source =
			'# Compose\r\nservices:\r\n  traefik:\r\n    image: "example:1"\r\n    enabled: true\r\n    note: |\r\n      <script>alert(1)</script>\r\n';
		const tokens = yamlTokens(source);
		expect(tokens.map((t) => t.text).join('')).toBe(source);
		expect(tokens.some((t) => t.kind === 'key')).toBe(true);
		expect(tokens.some((t) => t.kind === 'comment')).toBe(true);
		expect(tokens.some((t) => t.kind === 'literal')).toBe(true);
	});
	it('preserves malformed and oversized text without hiding review content', () => {
		for (const source of [
			'broken: [\n<script>',
			'a'.repeat(200001),
			'',
			'value: &anchor {list: [one, two]}\ncopy: *anchor\n',
		])
			expect(
				yamlTokens(source)
					.map((t) => t.text)
					.join(''),
			).toBe(source);
	});
	it('keeps diff headers and signs, CRLF, blank lines and the final unterminated line', () => {
		const source = '--- active\r\n+++ proposed\r\n@@ -1 +1 @@\r\n-old\r\n+new\r\n\r\n unchanged';
		const tokens = diffLines(source);
		expect(tokens.map((t) => t.text).join('')).toBe(source);
		expect(tokens.map((t) => t.kind)).toEqual(['removed', 'added', 'hunk', 'removed', 'added', '', '']);
	});
	it('highlights YAML inside diff rows and keeps headers and context apart', () => {
		const rows = diffRows('--- active\n+++ proposed\n router:\n-  rule: old\n+  rule: "new"\n... diff truncated ...');
		expect(rows.map((r) => r.kind)).toEqual(['meta', 'meta', 'context', 'removed', 'added', 'meta']);
		expect(rows[3].marker).toBe('-');
		expect(rows[3].tokens.map((t) => t.text).join('')).toBe('  rule: old');
		expect(rows[4].tokens.some((t) => t.kind === 'key')).toBe(true);
		expect(rows[4].tokens.some((t) => t.kind === 'string')).toBe(true);
	});
	it('folds unchanged lines into gaps around the changes, GitHub style', () => {
		const lines = Array.from({ length: 12 }, (_, i) => ' line' + i);
		const items = collapseDiff(
			diffRows(['--- active', '+++ proposed', ...lines, '+new', '+  sub', ' tail1', ' tail2'].join('\n')),
			3,
		);
		expect(items[0]).toMatchObject({ kind: 'gap' });
		expect((items[0] as any).rows).toHaveLength(9);
		const shown = items.filter((i) => i.kind === 'row').map((i: any) => i.row.tokens.map((t: any) => t.text).join(''));
		expect(shown).toEqual(['line9', 'line10', 'line11', 'new', '  sub', 'tail1', 'tail2']);
		expect(items.some((i) => i.kind === 'gap' && i.id > 0)).toBe(false);
	});
	it('leaves a diff without changes alone', () => {
		expect(collapseDiff(diffRows(' a\n b'), 3).every((i) => i.kind === 'row')).toBe(true);
	});
});

/** Small collision-safe id generator for DOM element wiring (no dependency). */
export function nanoid(): string {
	const bytes = new Uint8Array(8);
	crypto.getRandomValues(bytes);
	return 'id-' + Array.from(bytes, (b) => b.toString(36).padStart(2, '0')).join('');
}

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
vi.hoisted(() => {
	(globalThis as any).__mockFetch = async () => { throw new Error('no mock'); };
	globalThis.fetch = ((...args: unknown[]) => (globalThis as any).__mockFetch(...args)) as typeof fetch;
});
import { reconnect, saveResource } from '../src/core/mutations';
import { flashes } from '../src/shared/ui/flash.svelte';

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
let keys: (string | null)[] = [];

beforeEach(() => {
	flashes.splice(0);
	keys = [];
	reconnect.windowMs = 2_000;
	reconnect.delayMs = 5;
});
afterEach(() => { reconnect.windowMs = 90_000; reconnect.delayMs = 2_000; });

const respond = (answers: (() => Response | Promise<Response>)[]) => {
	(globalThis as any).__mockFetch = async (_path: string, init: RequestInit = {}) => {
		keys.push(new Headers(init.headers).get('Idempotency-Key'));
		const next = answers.shift();
		if (!next) throw new Error('unexpected call');
		return next();
	};
};
const refused = () => { throw new TypeError('Failed to fetch'); };
const traefik404 = () => new Response('404 page not found', { status: 404, headers: { 'Content-Type': 'text/plain' } });

describe('a write while Traefik restarts', () => {
	it('is sent again with the same key until the Console answers', async () => {
		respond([refused, traefik404, () => json({ operation: { id: 9, status: 'succeeded' } })]);
		const res = await saveResource('/api/v1/resolvers/le', { type: 'acme' }, 0, { idempotencyKey: 'key-1234567890123' });
		expect((res as any).operation.id).toBe(9);
		expect(keys).toEqual(['key-1234567890123', 'key-1234567890123', 'key-1234567890123']);
		expect(flashes.some((f) => f.title === 'Traefik is restarting')).toBe(true);
	});
	it('does not resend a preview, a write without a key, or a real error from the Console', async () => {
		respond([refused]);
		await expect(saveResource('/api/v1/resolvers/le', {}, 0, { dryRun: true, idempotencyKey: 'key-1234567890123' })).rejects.toBeTruthy();
		respond([refused]);
		await expect(saveResource('/api/v1/resolvers/le', {}, 0)).rejects.toBeTruthy();
		respond([() => json({ code: 'ERR_VALIDATION', message: 'bad', operation_id: 'op', retryable: false }, 422)]);
		await expect(saveResource('/api/v1/resolvers/le', {}, 0, { idempotencyKey: 'key-1234567890123' })).rejects.toMatchObject({ code: 'ERR_VALIDATION' });
		expect(keys).toHaveLength(3);
	});
	it('gives up after the window', async () => {
		reconnect.windowMs = 30;
		respond(Array.from({ length: 50 }, () => refused));
		await expect(saveResource('/api/v1/resolvers/le', {}, 0, { idempotencyKey: 'key-1234567890123' })).rejects.toBeTruthy();
	});
});

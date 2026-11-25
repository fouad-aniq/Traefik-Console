import { describe, expect, it, vi } from 'vitest';
import { ApiClient, ApiError, NetworkError } from '../src/shared/api/client';
import { api } from '../src/shared/api/api';
import { MUTATION_RESPONSE_TIMEOUT_MS, saveResource } from '../src/core/mutations';
import { recoverOperation, restoreSnapshot } from '../src/features/changes/api';

describe('ApiClient', () => {
	it('attaches Accept-Language matching the selected language', async () => {
		let capturedHeaders: Headers | undefined;
		const mockFetch = vi.fn().mockImplementation(async (_path: string, init?: RequestInit) => {
			capturedHeaders = init?.headers as Headers;
			return new Response(JSON.stringify({ ok: true }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			});
		});

		let lang: 'en' | 'fr' = 'en';
		const client = new ApiClient(() => lang, mockFetch as unknown as typeof fetch);

		await client.get('/api/v1/domains');
		expect(capturedHeaders?.get('Accept-Language')).toBe('en');

		lang = 'fr';
		await client.get('/api/v1/domains');
		expect(capturedHeaders?.get('Accept-Language')).toBe('fr');
	});

	it('refuses non-manager API paths', async () => {
		const mockFetch = vi.fn();
		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);

		await expect(client.get('/api/broker/exec')).rejects.toThrow('refusing non-manager API path');
		await expect(client.get('/some/other/path')).rejects.toThrow('refusing non-manager API path');
		expect(mockFetch).not.toHaveBeenCalled();
	});

	it('supports cancellation and wraps aborted/failed requests as NetworkError', async () => {
		const mockFetch = vi.fn().mockImplementation(async () => {
			const err = new Error('The operation was aborted');
			err.name = 'AbortError';
			throw err;
		});

		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);
		const controller = new AbortController();
		controller.abort();

		await expect(
			client.get('/api/v1/domains', { signal: controller.signal })
		).rejects.toThrow(NetworkError);
	});

	it('decodes structured backend API errors into ApiError', async () => {
		const errorBody = {
			code: 'VALIDATION_FAILED',
			message: 'Domain name is invalid.',
			field_errors: { name: 'must be fully qualified' },
			operation_id: 'op-987654',
			retryable: false
		};
		const mockFetch = vi.fn().mockImplementation(async () => {
			return new Response(JSON.stringify(errorBody), {
				status: 400,
				headers: { 'Content-Type': 'application/json' }
			});
		});

		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);

		try {
			await client.post('/api/v1/domains', { name: 'bad' });
			expect.fail('should have thrown ApiError');
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			const apiErr = err as ApiError;
			expect(apiErr.code).toBe('VALIDATION_FAILED');
			expect(apiErr.message).toBe('Domain name is invalid.');
			expect(apiErr.fieldErrors).toEqual({ name: 'must be fully qualified' });
			expect(apiErr.operationId).toBe('op-987654');
			expect(apiErr.status).toBe(400);
			expect(apiErr.retryable).toBe(false);
		}
	});

	it('safely handles non-JSON error responses with generic fallback', async () => {
		const mockFetch = vi.fn().mockImplementation(async () => {
			return new Response('<html>502 Bad Gateway</html>', {
				status: 502,
				headers: { 'Content-Type': 'text/html' }
			});
		});

		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);

		try {
			await client.get('/api/v1/overview');
			expect.fail('should have thrown ApiError');
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			const apiErr = err as ApiError;
			expect(apiErr.code).toBe('INTERNAL_ERROR');
			expect(apiErr.status).toBe(502);
		}
	});

	it('flags 503 / 429 status as retryable', async () => {
		const mockFetch = vi.fn().mockImplementation(async () => {
			return new Response('Service Unavailable', { status: 503 });
		});

		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);

		try {
			await client.get('/api/v1/overview');
			expect.fail('should have thrown ApiError');
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			expect((err as ApiError).retryable).toBe(true);
		}
	});

	it('surfaces UNEXPECTED_RESPONSE for 200 with invalid JSON', async () => {
		const mockFetch = vi.fn().mockImplementation(async () => {
			return new Response('not valid json', { status: 200 });
		});

		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);

		try {
			await client.get('/api/v1/overview');
			expect.fail('should have thrown ApiError');
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			expect((err as ApiError).code).toBe('UNEXPECTED_RESPONSE');
		}
	});

	it('returns undefined for 204 No Content', async () => {
		const mockFetch = vi.fn().mockImplementation(async () => {
			return new Response(null, { status: 204 });
		});

		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);
		const result = await client.delete('/api/v1/domains/example.com');
		expect(result).toBeUndefined();
	});

	it('passes caller-supplied preconditions and headers through verbatim', async () => {
		let capturedHeaders: Headers | undefined;
		const mockFetch = vi.fn().mockImplementation(async (_path: string, init?: RequestInit) => {
			capturedHeaders = init?.headers as Headers;
			return new Response(JSON.stringify({ ok: true }), { status: 200 });
		});

		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);

		await client.put('/api/v1/drafts/current', { some: 'payload' }, {
			headers: {
				'If-Match': '"rev-104"',
				'Idempotency-Key': 'client-uuid-123',
				'X-CSRF-Token': 'csrf-token-abc'
			}
		});

		expect(capturedHeaders?.get('If-Match')).toBe('"rev-104"');
		expect(capturedHeaders?.get('Idempotency-Key')).toBe('client-uuid-123');
		expect(capturedHeaders?.get('X-CSRF-Token')).toBe('csrf-token-abc');
		expect(capturedHeaders?.get('Content-Type')).toBe('application/json');
	});

	it('does not perform automatic retry on failed mutations', async () => {
		let calls = 0;
		const mockFetch = vi.fn().mockImplementation(async () => {
			calls++;
			return new Response(JSON.stringify({ code: 'CONFLICT', message: 'Version mismatch' }), {
				status: 409
			});
		});

		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);

		await expect(client.post('/api/v1/drafts/apply', {})).rejects.toThrow(ApiError);
		expect(calls).toBe(1);
	});

	it('rejects dot-segment, encoded traversal, and backslash paths outside /api/v1', async () => {
		const mockFetch = vi.fn();
		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);

		const traversalPaths = [
			'/api/v1/../../broker/v1/crowdsec/status',
			'/api/v1/%2e%2e/broker',
			'/api/v1%2f%2e%2e/broker',
			'/api/v1\\..\\broker',
			'/api/v1/%2e%2e%2fbroker',
			'http://external.invalid/api/v1/health',
			'//external.invalid/api/v1/health'
		];

		for (const path of traversalPaths) {
			await expect(client.get(path)).rejects.toThrow('refusing non-manager API path');
		}
		expect(mockFetch).not.toHaveBeenCalled();
	});

	it('aborts and throws NetworkError when response body streaming stalls past timeout', async () => {
		let signal: AbortSignal | undefined;
		const mockFetch = vi.fn().mockImplementation(async (_path: string, init?: RequestInit) => {
			signal = init?.signal as AbortSignal;
			return new Response(
				new ReadableStream({
					start(controller) {
						setTimeout(() => {
							controller.enqueue(new TextEncoder().encode('{"delayed": true}'));
							controller.close();
						}, 100);
					}
				}),
				{ status: 200 }
			);
		});

		const client = new ApiClient(() => 'en', mockFetch as unknown as typeof fetch);

		await expect(client.get('/api/v1/health', { timeoutMs: 15 })).rejects.toThrow(NetworkError);
		expect(signal?.aborted).toBe(true);
	});

	it('gives real mutations and recovery enough time for a structured rollback response', async () => {
		expect(MUTATION_RESPONSE_TIMEOUT_MS).toBe(240_000);
		const put = vi.spyOn(api, 'put').mockResolvedValue({});
		const post = vi.spyOn(api, 'post').mockResolvedValue({});
		try {
			await saveResource('/api/v1/crowdsec/config', { enabled: true }, 1, { idempotencyKey: 'client-uuid-123' });
			expect(put).toHaveBeenLastCalledWith('/api/v1/crowdsec/config', { enabled: true }, expect.objectContaining({
				timeoutMs: MUTATION_RESPONSE_TIMEOUT_MS,
				signal: undefined
			}));

			await saveResource('/api/v1/crowdsec/config', { enabled: true }, 1, { dryRun: true });
			expect(put).toHaveBeenLastCalledWith('/api/v1/crowdsec/config?dry_run=true', { enabled: true }, expect.objectContaining({ timeoutMs: 30_000 }));

			await restoreSnapshot(22, { restore_token: 'token', idempotency_key: 'client-uuid-456' });
			expect(post).toHaveBeenLastCalledWith('/api/v1/snapshots/22/restore', expect.anything(), expect.objectContaining({ timeoutMs: MUTATION_RESPONSE_TIMEOUT_MS }));

			await recoverOperation(22);
			expect(post).toHaveBeenLastCalledWith('/api/v1/operations/22/recover', undefined, expect.objectContaining({ timeoutMs: MUTATION_RESPONSE_TIMEOUT_MS }));
		} finally {
			put.mockRestore();
			post.mockRestore();
		}
	});

	it('localizes fallback error messages for non-JSON errors and unexpected response in FR', async () => {
		const nonJsonErrorFetch = vi.fn().mockImplementation(async () => {
			return new Response('<html>500 Error</html>', { status: 500 });
		});
		const frClient = new ApiClient(() => 'fr', nonJsonErrorFetch as unknown as typeof fetch);

		try {
			await frClient.get('/api/v1/overview');
			expect.fail('should have thrown ApiError');
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			expect((err as ApiError).message).toBe('Échec de la requête.');
		}

		const unexpectedFetch = vi.fn().mockImplementation(async () => {
			return new Response('non-json ok body', { status: 200 });
		});
		const frClientUnexpected = new ApiClient(() => 'fr', unexpectedFetch as unknown as typeof fetch);

		try {
			await frClientUnexpected.get('/api/v1/overview');
			expect.fail('should have thrown ApiError');
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			expect((err as ApiError).code).toBe('UNEXPECTED_RESPONSE');
			expect((err as ApiError).message).toBe('Le serveur a renvoyé un format de réponse inattendu.');
		}
	});
});

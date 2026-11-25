// Shared HTTP transport for the console API.
//
// Contract notes (verified in F00-R2/R3, see .scratch/frontend-f00-r2/
// capability-api-matrix.json):
// - Same-origin relative paths only. The browser never calls the broker.
// - Session cookie is HttpOnly and attached by the browser itself; the CSRF
//   token comes from the session (auth/me or login response) and is supplied
//   by callers as the X-CSRF-Token header. This client never invents one.
// - Preconditions and retry identity (If-Match, If-None-Match, Idempotency-Key) are
//   caller-supplied headers, passed through verbatim. This client performs no
//   automatic mutation retry and adds no guessed idempotency headers.
// - Backend error bodies follow the stable APIError shape
//   {code, message, field_errors, operation_id, retryable}; non-JSON and
//   network failures decode to a safe generic shape instead of throwing raw.
// - Accept-Language follows the selected UI language; responses may carry
//   Content-Language/Vary (F00 verified backend localization).

export interface ApiErrorShape {
	code: string;
	message: string;
	field_errors?: Record<string, string>;
	operation_id?: string;
	retryable?: boolean;
	status: number;
	retryAfter?: number;
}

export class ApiError extends Error {
	readonly code: string;
	readonly status: number;
	readonly fieldErrors: Record<string, string>;
	readonly operationId?: string;
	readonly retryable: boolean;
	readonly retryAfter?: number;

	constructor(shape: ApiErrorShape) {
		super(shape.message);
		this.name = 'ApiError';
		this.code = shape.code;
		this.status = shape.status;
		this.fieldErrors = shape.field_errors ?? {};
		this.operationId = shape.operation_id;
		this.retryable = shape.retryable ?? false;
		this.retryAfter = shape.retryAfter;
	}
}

export class NetworkError extends Error {
	readonly cause?: unknown;

	constructor(cause?: unknown) {
		super('network_error');
		this.name = 'NetworkError';
		this.cause = cause;
	}
}

export interface RequestOptions {
	/** Relative API path, e.g. "/api/v1/snapshots". Must not target the broker. */
	path: string;
	method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
	body?: unknown;
	/** Caller-supplied headers (CSRF, If-Match, Idempotency-Key, …). Passed through verbatim. */
	headers?: Record<string, string>;
	signal?: AbortSignal;
	/** Absolute request timeout in milliseconds; uses AbortController internally. */
	timeoutMs?: number;
	/**
	 * A 401 response invokes the registered unauthorized handler (session ended)
	 * unless this is false. Sign-in endpoints under /api/v1/auth/ never invoke it.
	 */
	protected?: boolean;
}

const DEFAULT_TIMEOUT_MS = 15_000;

function assertManagerPath(path: string): void {
	if (typeof path !== 'string') {
		throw new Error(`refusing non-manager API path: ${path}`);
	}
	if (!path.startsWith('/api/v1/') && path !== '/api/v1') {
		throw new Error(`refusing non-manager API path: ${path}`);
	}
	if (path.includes('\\') || /%5c/i.test(path)) {
		throw new Error(`refusing non-manager API path: ${path}`);
	}
	let resolved: URL;
	try {
		resolved = new URL(path, 'http://localhost');
	} catch {
		throw new Error(`refusing non-manager API path: ${path}`);
	}
	if (resolved.origin !== 'http://localhost') {
		throw new Error(`refusing non-manager API path: ${path}`);
	}
	if (resolved.pathname !== '/api/v1' && !resolved.pathname.startsWith('/api/v1/')) {
		throw new Error(`refusing non-manager API path: ${path}`);
	}
	try {
		const decoded = decodeURIComponent(path);
		if (decoded.includes('\\')) {
			throw new Error(`refusing non-manager API path: ${path}`);
		}
		const decodedResolved = new URL(decoded, 'http://localhost');
		if (decodedResolved.pathname !== '/api/v1' && !decodedResolved.pathname.startsWith('/api/v1/')) {
			throw new Error(`refusing non-manager API path: ${path}`);
		}
	} catch (err) {
		if (err instanceof Error && err.message.startsWith('refusing non-manager')) {
			throw err;
		}
		throw new Error(`refusing non-manager API path: ${path}`);
	}
}

function buildHeaders(method: string, explicit: Record<string, string> | undefined): Headers {
	const headers = new Headers(explicit);
	if (!headers.has('Accept')) headers.set('Accept', 'application/json');
	if (method !== 'GET' && method !== 'DELETE' && !headers.has('Content-Type')) {
		headers.set('Content-Type', 'application/json');
	}
	return headers;
}

async function readResponseText(response: Response, signal: AbortSignal): Promise<string> {
	if (signal.aborted) {
		throw new NetworkError(signal.reason ?? new Error('The operation was aborted'));
	}
	return new Promise<string>((resolve, reject) => {
		let settled = false;
		const onAbort = () => {
			if (!settled) {
				settled = true;
				if (response.body && typeof response.body.cancel === 'function') {
					response.body.cancel().catch(() => {});
				}
				reject(new NetworkError(signal.reason ?? new Error('The operation was aborted')));
			}
		};
		signal.addEventListener('abort', onAbort, { once: true });
		response.text().then(
			(text) => {
				if (!settled) {
					settled = true;
					signal.removeEventListener('abort', onAbort);
					resolve(text);
				}
			},
			(err) => {
				if (!settled) {
					settled = true;
					signal.removeEventListener('abort', onAbort);
					reject(new NetworkError(err));
				}
			}
		);
	});
}

async function decodeError(
	response: Response,
	signal: AbortSignal,
	lang: 'en' | 'fr'
): Promise<ApiErrorShape> {
	let code = 'INTERNAL_ERROR';
	let message = lang === 'fr' ? 'Échec de la requête.' : 'Request failed.';
	let fieldErrors: Record<string, string> | undefined;
	let operationId: string | undefined;
	let retryable = response.status === 503 || response.status === 429;
	try {
		const text = await readResponseText(response, signal);
		if (text) {
			const parsed = JSON.parse(text) as Partial<ApiErrorShape>;
			if (typeof parsed.code === 'string') code = parsed.code;
			if (typeof parsed.message === 'string') message = parsed.message;
			if (parsed.field_errors && typeof parsed.field_errors === 'object') {
				fieldErrors = parsed.field_errors as Record<string, string>;
			}
			if (typeof parsed.operation_id === 'string') operationId = parsed.operation_id;
			if (typeof parsed.retryable === 'boolean') retryable = parsed.retryable;
		}
	} catch (cause) {
		if (cause instanceof NetworkError || signal.aborted) {
			throw cause instanceof NetworkError ? cause : new NetworkError(cause);
		}
		// non-JSON body: keep the safe generic shape
	}
	let retryAfter: number | undefined;
	const retryAfterHeader = response.headers.get('Retry-After');
	if (retryAfterHeader) {
		const parsedInt = /^\d+$/.test(retryAfterHeader.trim()) ? Number(retryAfterHeader) : NaN;
		if (!Number.isNaN(parsedInt) && parsedInt > 0) {
			retryAfter = Math.min(parsedInt, 3600);
		} else {
			const date = /^(Mon|Tue|Wed|Thu|Fri|Sat|Sun),/.test(retryAfterHeader) ? Date.parse(retryAfterHeader) : NaN;
			if (!Number.isNaN(date)) {
				const diffSeconds = Math.ceil((date - Date.now()) / 1000);
				if (diffSeconds > 0) {
					retryAfter = Math.min(diffSeconds, 3600);
				}
			}
		}
	}
	return { code, message, field_errors: fieldErrors, operation_id: operationId, retryable, status: response.status, retryAfter };
}

export class ApiClient {
	private readonly fetchImpl: typeof fetch;
	private unauthorizedHandler?: (error: ApiError) => void;
	private headerProvider?: (method: string) => Record<string, string>;

	constructor(
		private readonly language: () => 'en' | 'fr',
		fetchImpl: typeof fetch = fetch
	) {
		this.fetchImpl = typeof fetchImpl?.bind === 'function' ? fetchImpl.bind(globalThis) : fetchImpl;
	}

	setUnauthorizedHandler(handler: (error: ApiError) => void): void {
		this.unauthorizedHandler = handler;
	}

	/**
	 * Session-scoped headers (the CSRF token) added to every request unless the
	 * caller already set the same header. Values live in memory only.
	 */
	setHeaderProvider(provider: (method: string) => Record<string, string>): void {
		this.headerProvider = provider;
	}

	async request<T>(options: RequestOptions): Promise<T> {
		assertManagerPath(options.path);
		const method = options.method ?? 'GET';
		const headers = buildHeaders(method, options.headers);
		for (const [name, value] of Object.entries(this.headerProvider?.(method) ?? {})) {
			if (value && !headers.has(name)) headers.set(name, value);
		}
		const lang = this.language();
		headers.set('Accept-Language', lang);

		const timeout = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
		const timeoutController = new AbortController();
		const timer = setTimeout(() => timeoutController.abort(), timeout);
		const signal = options.signal
			? AbortSignal.any([options.signal, timeoutController.signal])
			: timeoutController.signal;

		try {
			const response = await this.fetchImpl(options.path, {
				method,
				headers,
				credentials: 'same-origin',
				body: options.body === undefined ? undefined : JSON.stringify(options.body),
				signal
			});

			if (!response.ok) {
				const errShape = await decodeError(response, signal, lang);
				if (response.status === 401 && options.protected !== false && !options.path.startsWith('/api/v1/auth/')) {
					this.unauthorizedHandler?.(new ApiError(errShape));
				}
				throw new ApiError(errShape);
			}
			if (response.status === 204) {
				return undefined as T;
			}
			const text = await readResponseText(response, signal);
			if (!text) return undefined as T;
			try {
				return JSON.parse(text) as T;
			} catch {
				// 2xx with a non-JSON body is unexpected; surface a safe error.
				throw new ApiError({
					code: 'UNEXPECTED_RESPONSE',
					message: lang === 'fr'
						? 'Le serveur a renvoyé un format de réponse inattendu.'
						: 'The server returned an unexpected response format.',
					status: response.status,
					retryable: false
				});
			}
		} catch (cause) {
			if (cause instanceof ApiError) {
				throw cause;
			}
			if (cause instanceof NetworkError) {
				throw cause;
			}
			throw new NetworkError(cause);
		} finally {
			clearTimeout(timer);
		}
	}

	get<T>(path: string, options?: Partial<RequestOptions>): Promise<T> {
		return this.request<T>({ ...options, path, method: 'GET' });
	}

	post<T>(path: string, body?: unknown, options?: Partial<RequestOptions>): Promise<T> {
		return this.request<T>({ ...options, path, method: 'POST', body });
	}

	put<T>(path: string, body?: unknown, options?: Partial<RequestOptions>): Promise<T> {
		return this.request<T>({ ...options, path, method: 'PUT', body });
	}

	patch<T>(path: string, body?: unknown, options?: Partial<RequestOptions>): Promise<T> {
		return this.request<T>({ ...options, path, method: 'PATCH', body });
	}

	delete<T>(path: string, options?: Partial<RequestOptions>): Promise<T> {
		return this.request<T>({ ...options, path, method: 'DELETE' });
	}
}

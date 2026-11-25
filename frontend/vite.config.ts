/// <reference types="vitest" />
import { defineConfig, loadEnv } from 'vite';
import { svelte, vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { readFileSync } from 'node:fs';

// The console version shown in the sidebar footer comes from package.json.
const { version } = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8')) as { version: string };

// Dev: the SPA calls same-origin `/api/v1/*`; Vite proxies to core-v2
// (default http://127.0.0.1:8081). Override with CORE_V2_URL.
// core-v2 enforces host/origin policy, so keep `changeOrigin` and set
// CONSOLE_ORIGIN on the backend to the Vite origin if the browser hits it directly.
export default defineConfig(({ mode }) => {
	const env = loadEnv(mode, process.cwd(), '');
	const target = env.CORE_V2_URL || 'http://127.0.0.1:8081';
	return {
		cacheDir: env.VITE_CACHE_DIR || undefined,
		define: { __APP_VERSION__: JSON.stringify(version) },
		resolve: { conditions: mode === 'test' ? ['browser'] : undefined },
		plugins: [svelte({ preprocess: vitePreprocess({ script: true }) })],
		server: { proxy: { '/api': { target, changeOrigin: true } } },
		build: { outDir: 'dist', emptyOutDir: true },
		test: { environment: 'happy-dom', include: ['tests/**/*.test.ts', 'src/**/*.test.ts'] }
	};
});

/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_FIXTURE_MODE?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

/** Console version from package.json, injected by vite.config.ts. */
declare const __APP_VERSION__: string;

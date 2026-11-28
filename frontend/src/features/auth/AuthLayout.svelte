<script lang="ts">
	/**
	 * Frame for the signed-out screens (setup, sign-in, unavailable).
	 * Left: the console's subject drawn once — a request travelling from an
	 * entry point through a router to a service, in the configuration-map kind
	 * colours. Right: the form, left-aligned on the plain surface.
	 */
	import type { Snippet } from 'svelte';
	import TrafficMark from '../../shared/ui/TrafficMark.svelte';
	import {
		prefs,
		getResolvedTheme,
		setLanguage,
		setThemePreference,
	} from '../../shared/preferences/preferences.svelte';
	import { t } from '../../shared/localization/i18n';

	let {
		title,
		lead = '',
		centered = false,
		below,
		children,
	}: {
		title: string;
		lead?: string;
		/** centre the block vertically too (sign-in); setup stays top-anchored because its steps change height */
		centered?: boolean;
		/** content that opens under the centred block without moving it (the password-reset help) */
		below?: Snippet;
		children: Snippet;
	} = $props();

	const dark = $derived(getResolvedTheme() === 'dark');
</script>

<div class="auth-frame">
	<aside class="scene" aria-hidden="true">
		<div class="brand">
			<TrafficMark size={28} />
			<span>Traefik Console</span>
		</div>
		<svg class="route" viewBox="0 0 360 300" preserveAspectRatio="xMidYMid meet">
			<!-- links attach at node edges -->
			<path class="link" d="M106 70 C 136 70, 120 150, 146 150" />
			<path class="link" d="M106 230 C 136 230, 120 150, 146 150" />
			<path class="link" d="M214 150 C 240 150, 230 90, 256 90" />
			<path class="link" d="M214 150 C 240 150, 230 210, 256 210" />
			<!-- the request being routed: :443 → Host → app -->
			<path class="request" pathLength="1" d="M106 70 C 136 70, 120 150, 146 150 L 214 150 C 240 150, 230 90, 256 90" />
			<!-- entry points -->
			<g class="node entry"><rect x="34" y="52" width="72" height="36" rx="6" /><text x="70" y="75">:443</text></g>
			<g class="node entry dim"><rect x="34" y="212" width="72" height="36" rx="6" /><text x="70" y="235">:80</text></g>
			<!-- router -->
			<g class="node router">
				<rect x="146" y="130" width="68" height="40" rx="20" /><text x="180" y="155">Host</text>
			</g>
			<!-- services -->
			<g class="node service"><rect x="256" y="72" width="72" height="36" rx="6" /><text x="292" y="95">app</text></g>
			<g class="node service dim">
				<rect x="256" y="192" width="72" height="36" rx="6" /><text x="292" y="215">api</text>
			</g>
			<circle class="pulse" cx="256" cy="90" r="4.5" />
		</svg>
		<p class="caption">{t('authScreens.caption')}</p>
	</aside>

	<main class="panel" class:centered>
		<div class="prefs">
			<button
				type="button"
				class="pref"
				onclick={() => setThemePreference(dark ? 'light' : 'dark')}
				aria-label={t('shell.theme')}
			>
				<span aria-hidden="true">{dark ? '🌙' : '☀️'}</span>
			</button>
			<button
				type="button"
				class="pref"
				onclick={() => setLanguage(prefs.language === 'en' ? 'fr' : 'en')}
				aria-label={`${t('shell.language')}: ${prefs.language.toUpperCase()}`}
			>
				{prefs.language === 'en' ? 'FR' : 'EN'}
			</button>
		</div>
		<div class="content">
			<div class="mobile-brand"><TrafficMark size={24} /><span>Traefik Console</span></div>
			<h1>{title}</h1>
			{#if lead}<p class="lead">{lead}</p>{/if}
			{@render children()}
			{#if below}<div class="below">{@render below()}</div>{/if}
		</div>
	</main>
</div>

<style>
	.auth-frame {
		min-height: 100svh;
		display: grid;
		grid-template-columns: minmax(320px, 44%) 1fr;
		background: var(--surface);
	}

	.scene {
		position: relative;
		display: flex;
		flex-direction: column;
		justify-content: space-between;
		gap: 24px;
		padding: 40px;
		background: var(--canvas);
		border-right: 1px solid var(--border);
		overflow: hidden;
	}
	.brand {
		display: flex;
		align-items: center;
		gap: 10px;
		font-weight: 600;
		font-size: 17px;
	}
	.route {
		width: 100%;
		max-width: 460px;
		align-self: center;
	}
	.caption {
		margin: 0;
		max-width: 34ch;
		color: var(--text-muted);
		font-size: 14px;
		line-height: 21px;
	}

	.link {
		fill: none;
		stroke: var(--border);
		stroke-width: 2;
	}
	.request {
		fill: none;
		stroke: var(--connection);
		stroke-width: 3;
		stroke-linecap: round;
		stroke-dasharray: 1;
		stroke-dashoffset: 1;
		animation: travel 1.4s 0.3s cubic-bezier(0.6, 0, 0.2, 1) forwards;
	}
	.node rect {
		fill: var(--surface);
		stroke-width: 1.75;
	}
	.node text {
		font-size: 12px;
		text-anchor: middle;
		fill: var(--text);
		font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
	}
	.entry rect {
		stroke: var(--kind-entrypoint);
	}
	.router rect {
		stroke: var(--kind-route);
		fill: var(--accent-soft);
	}
	.service rect {
		stroke: var(--kind-service);
	}
	.dim {
		opacity: 0.45;
	}
	.pulse {
		fill: var(--accent);
		opacity: 0;
		animation: arrive 0.4s 1.6s ease-out forwards;
	}

	@keyframes travel {
		to {
			stroke-dashoffset: 0;
		}
	}
	@keyframes arrive {
		to {
			opacity: 1;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.request {
			animation: none;
			stroke-dashoffset: 0;
		}
		.pulse {
			animation: none;
			opacity: 1;
		}
	}

	/* Anchored from the top (not vertically centred) so content that opens below,
	   such as the password-reset help, never moves the fields above it. */
	.panel {
		position: relative;
		display: flex;
		align-items: flex-start;
		justify-content: center;
		padding: clamp(56px, 16vh, 168px) 48px 64px;
	}
	.content {
		position: relative;
		width: 100%;
		max-width: 400px;
		margin-inline: auto;
	}
	/* Vertically centred around the sign-in block only: the content in .below hangs
	   underneath it, out of the flow, so opening it never moves the fields. */
	.panel.centered {
		align-items: center;
		padding-block: 64px;
	}
	.below {
		position: absolute;
		top: 100%;
		left: 0;
		right: 0;
		padding-bottom: 48px;
	}
	.content h1 {
		margin: 0;
		font-size: 28px;
		line-height: 36px;
		font-weight: 600;
		letter-spacing: -0.02em;
	}
	.lead {
		margin: 8px 0 0;
		color: var(--text-muted);
		font-size: 15px;
		line-height: 23px;
	}
	.mobile-brand {
		display: none;
	}

	.prefs {
		position: absolute;
		top: 16px;
		right: 16px;
		display: flex;
		gap: 4px;
	}
	.pref {
		min-width: 40px;
		min-height: 40px;
		border: 1px solid transparent;
		border-radius: var(--radius-control);
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
		font-size: 13px;
		font-weight: 600;
	}
	.pref:hover {
		border-color: var(--border);
		color: var(--text);
	}

	@media (max-width: 899px) {
		.auth-frame {
			grid-template-columns: 1fr;
		}
		.scene {
			display: none;
		}
		.panel,
		.panel.centered {
			align-items: flex-start;
			padding: 72px 20px 40px;
		}
		.below {
			position: static;
			padding-bottom: 0;
		}
		.content {
			margin-inline: auto;
		}
		.mobile-brand {
			display: flex;
			align-items: center;
			gap: 8px;
			margin-bottom: 32px;
			font-weight: 600;
		}
	}
</style>

/**
 * Splitting a backend into the fields the editor shows, and joining them back. Values are kept
 * as typed (no URL normalisation), so what is shown is what is sent.
 *
 *   HTTP:     scheme :// host [: port] [path]
 *   TCP/UDP:  host : port
 *
 * Hosts that are IPv6 literals are shown without brackets and bracketed when joined.
 */
export interface UrlParts {
	scheme: string;
	host: string;
	port: string;
	path: string;
}
export interface AddressParts {
	host: string;
	port: string;
}

const KNOWN_SCHEME = /^(https?|h2c):\/\//i;
const ANY_SCHEME = /^[a-z][a-z0-9+.-]*:\/\//i;

/** host[:port][path] with an optional [IPv6] host. */
function splitAuthority(text: string): { host: string; port: string; path: string } {
	const m = /^(\[[^\]]*\]|[^:/?#]*)(?::([^/?#]*))?(.*)$/s.exec(text) ?? ['', text, '', ''];
	const host = m[1].startsWith('[') && m[1].endsWith(']') ? m[1].slice(1, -1) : m[1];
	return { host, port: m[2] ?? '', path: m[3] ?? '' };
}

/** A bare IPv6 literal (two or more colons, no brackets) is a host on its own. */
const bareIPv6 = (text: string) => !text.includes('[') && (text.match(/:/g)?.length ?? 0) >= 2 && !text.includes('/');

export function parseUrl(value: string, fallbackScheme = 'http'): UrlParts {
	const v = value.trim();
	const known = KNOWN_SCHEME.exec(v);
	if (known) return { scheme: known[1].toLowerCase(), ...splitAuthority(v.slice(known[0].length)) };
	if (ANY_SCHEME.test(v)) return { scheme: fallbackScheme, host: v, port: '', path: '' }; // shown as typed; validation names it
	if (bareIPv6(v)) return { scheme: fallbackScheme, host: v, port: '', path: '' };
	return { scheme: fallbackScheme, ...splitAuthority(v) };
}

const bracket = (host: string) => (host.includes(':') && !host.startsWith('[') ? `[${host}]` : host);

export function joinUrl(p: UrlParts): string {
	const host = p.host.trim();
	if (!host) return '';
	if (ANY_SCHEME.test(host)) return host; // an unsupported scheme typed into the host field
	const path = p.path.trim();
	return `${p.scheme}://${bracket(host)}${p.port.trim() ? `:${p.port.trim()}` : ''}${path && !/^[/?#]/.test(path) ? `/${path}` : path}`;
}

/** host:port; a pasted tcp:// or udp:// prefix is dropped. */
export function parseAddress(value: string): AddressParts {
	const v = value.trim().replace(/^(tcp|udp):\/\//i, '');
	if (bareIPv6(v)) return { host: v, port: '' };
	const { host, port, path } = splitAuthority(v);
	return { host: host + path, port };
}

export function joinAddress(p: AddressParts): string {
	const host = p.host.trim();
	const port = p.port.trim();
	if (!host && !port) return '';
	return `${bracket(host)}:${port}`;
}

/** True when typed or pasted text into the host field carries more than a host. */
export const carriesMore = (text: string) =>
	ANY_SCHEME.test(text.trim()) || (/[:/]/.test(text) && !bareIPv6(text.trim()));

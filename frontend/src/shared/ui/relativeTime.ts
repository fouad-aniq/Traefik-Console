export function relativeDate(value: string | null | undefined, language: string, now: number, justNow: string): string {
	const timestamp = value ? Date.parse(value) : NaN;
	if (!Number.isFinite(timestamp)) return '—';
	const seconds = (timestamp - now) / 1000;
	if (Math.abs(seconds) < 60) return justNow;
	const units: [Intl.RelativeTimeFormatUnit, number][] = [
		['year', 31536000],
		['month', 2592000],
		['day', 86400],
		['hour', 3600],
		['minute', 60],
	];
	const [unit, duration] = units.find(([, size]) => Math.abs(seconds) >= size) ?? units[units.length - 1]!;
	return new Intl.RelativeTimeFormat(language, { numeric: 'auto' }).format(Math.trunc(seconds / duration), unit);
}

export function fullDate(value: string | null | undefined, language: string): string {
	const timestamp = value ? Date.parse(value) : NaN;
	if (!Number.isFinite(timestamp)) return '';
	return new Intl.DateTimeFormat(language, {
		year: 'numeric',
		month: 'long',
		day: 'numeric',
		hour: 'numeric',
		minute: '2-digit',
		second: '2-digit',
		timeZoneName: 'short',
	}).format(timestamp);
}

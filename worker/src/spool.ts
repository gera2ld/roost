// Spool key and metadata contract shared with server/internal/spool (keep in sync).

/** Custom metadata keys holding the SMTP envelope. */
export const META_ENVELOPE_FROM = 'envelope-from';
export const META_ENVELOPE_TO = 'envelope-to';

const pad = (n: number, width = 2) => String(n).padStart(width, '0');

/** Time-ordered key: YYYY/MM/DD/HHMMSS-<uuid>.eml in UTC, so lexical order = arrival order. */
export function spoolKey(now: Date, uuid: string): string {
	const date = `${now.getUTCFullYear()}/${pad(now.getUTCMonth() + 1)}/${pad(now.getUTCDate())}`;
	const time = `${pad(now.getUTCHours())}${pad(now.getUTCMinutes())}${pad(now.getUTCSeconds())}`;
	return `${date}/${time}-${uuid}.eml`;
}

export type Segment = { text: string; href?: string };

// http(s) only; trailing punctuation is left out of the link.
const urlPattern = /\bhttps?:\/\/[^\s<>"'`]+/gi;
const trailing = `.,;:!?)]}'"`;

/** Splits plain text into text and link segments. Rendered as text nodes, never HTML. */
export function linkify(text: string): Segment[] {
	const out: Segment[] = [];
	let last = 0;
	for (const m of text.matchAll(urlPattern)) {
		const url = trimUrl(m[0]);
		const start = m.index;
		if (start > last) out.push({ text: text.slice(last, start) });
		out.push({ text: url, href: safeHref(url) });
		last = start + url.length;
	}
	if (last < text.length) out.push({ text: text.slice(last) });
	return out.filter((s) => s.text !== '');
}

function trimUrl(url: string): string {
	while (url && trailing.includes(url.at(-1)!)) {
		// Keep a closing paren that the URL itself opened (wiki links).
		if (url.endsWith(')') && count(url, '(') >= count(url, ')')) break;
		url = url.slice(0, -1);
	}
	return url;
}

function count(s: string, ch: string): number {
	return s.split(ch).length - 1;
}

function safeHref(url: string): string | undefined {
	try {
		const u = new URL(url);
		return u.protocol === 'http:' || u.protocol === 'https:' ? u.href : undefined;
	} catch {
		return undefined;
	}
}

export function personLabel(a: { name?: string; address: string }): string {
	return a.name?.trim() || a.address;
}

/** Short list date: time today, "Mon D" this year, else a numeric date. */
export function shortDate(iso: string, now = new Date()): string {
	const d = new Date(iso.replace(' ', 'T'));
	if (Number.isNaN(d.getTime())) return '';
	if (d.toDateString() === now.toDateString()) {
		return d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
	}
	if (d.getFullYear() === now.getFullYear()) {
		return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
	}
	return d.toLocaleDateString();
}

export function longDate(iso: string): string {
	const d = new Date(iso.replace(' ', 'T'));
	return Number.isNaN(d.getTime())
		? ''
		: d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

export function fileSize(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
	return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

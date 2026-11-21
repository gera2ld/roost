import { defaultQuery } from './folders.js';

/** The mail view (or a thread in it) for a query; "" lists all mail. */
export function mailHref(query: string, thread = ''): string {
	const path = thread ? `/mail/${encodeURIComponent(thread)}` : '/mail';
	return `${path}?q=${encodeURIComponent(query)}`;
}

/** The query a mail URL shows: its q, or the inbox when it names none. */
export function urlQuery(url: { searchParams: { get(name: string): string | null } }): string {
	return url.searchParams.get('q') ?? defaultQuery;
}

/** The drafts list, optionally narrowed to one sending address or domain. */
export function draftsHref(mailbox = ''): string {
	return mailbox ? `/drafts?mb=${encodeURIComponent(mailbox)}` : '/drafts';
}

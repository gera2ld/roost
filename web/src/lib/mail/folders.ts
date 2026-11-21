// The sidebar folders. Every folder but Drafts is a search query: clicking one
// puts its term in the search box, and the sidebar highlights whichever folder
// the query names (see queryScope in search.ts). Drafts are messages without a
// thread, so they keep a list of their own.
export const folders = [
	{ id: 'inbox', label: 'Inbox', query: 'in:inbox' },
	{ id: 'starred', label: 'Starred', query: 'is:starred' },
	{ id: 'sent', label: 'Sent', query: 'in:sent' },
	{ id: 'drafts', label: 'Drafts', query: null },
	{ id: 'archive', label: 'Archive', query: 'in:archive' },
	{ id: 'trash', label: 'Trash', query: 'in:trash' },
] as const;

export type FolderId = (typeof folders)[number]['id'];

/** What the mail view shows when the URL names no query. */
export const defaultQuery = 'in:inbox';

type Filter = (raw: string, params?: Record<string, unknown>) => string;

/** Unsent outbound messages (drafts and failed sends), optionally from one address or domain. */
export function draftFilter(mailbox: string, filter: Filter): string {
	const base = 'direction = "outbound" && (send_status = "draft" || send_status = "failed")';
	if (!mailbox) return base;
	return mailbox.startsWith('@')
		? `${base} && ${filter('from_address ~ {:d}', { d: `%${mailbox.toLowerCase()}` })}`
		: `${base} && ${filter('from_address = {:a}', { a: mailbox.toLowerCase() })}`;
}

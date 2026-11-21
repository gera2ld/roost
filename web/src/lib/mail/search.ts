import {
	isTerm,
	parseQuery,
	querySegments,
	stringifyQuery,
	type QueryItem,
	type QuerySegment,
	type QueryTerm,
} from '@gera2ld/filter-query';
import { folders, type FolderId } from './folders.js';

// Spaces mean AND, so a folder plus anything typed after it narrows the folder;
// { a b } is the way to say OR.
const syntax = { join: 'and' } as const;

// The fields the server binds (server/internal/store/query.go). This copy only
// colours the search box and fills the help menu; the server decides, and
// searches an unknown field as the literal text "name:value".
export const searchFields = [
	{ name: 'in', hint: 'inbox, starred, sent, archive, trash', example: 'in:inbox' },
	{ name: 'from', hint: 'Sender name or address', example: 'from:pat' },
	{ name: 'to', hint: 'A To recipient', example: 'to:me@x.com' },
	{ name: 'cc', hint: 'A Cc recipient', example: 'cc:team' },
	{ name: 'subject', hint: 'Subject only', example: 'subject:invoice' },
	{ name: 'body', hint: 'Message text only', example: 'body:"see you"' },
	{ name: 'mailbox', hint: 'Receiving address, or shop@ on any domain', example: 'mailbox:shop@' },
	{ name: 'domain', hint: 'Receiving domain', example: 'domain:x.com' },
	{ name: 'filename', hint: 'Attachment name', example: 'filename:pdf' },
	{ name: 'has', hint: 'attachment', example: 'has:attachment' },
	{
		name: 'is',
		hint: 'unread, read, starred, sent, archived, trash, bounced',
		example: 'is:unread',
	},
	{ name: 'label', hint: 'Label name', example: 'label:work' },
	{ name: 'after', hint: 'On or after a date or age', example: 'after:2026-01-31' },
	{ name: 'before', hint: 'Before a date or age', example: 'before:30d' },
] as const;

const known = new Set<string>(searchFields.map((f) => f.name));

export function isSearchField(field: string): boolean {
	return known.has(field.toLowerCase());
}

/** The search box text as the JSON tree `/api/roost/search` takes. */
export function searchTree(text: string): string {
	return JSON.stringify(parseQuery(text, syntax));
}

const inFolders: Record<string, FolderId> = {
	inbox: 'inbox',
	starred: 'starred',
	sent: 'sent',
	archive: 'archive',
	trash: 'trash',
};
const isFolders: Record<string, FolderId> = {
	starred: 'starred',
	sent: 'sent',
	archived: 'archive',
	trash: 'trash',
};

/** The folder a required term names (in:inbox, is:starred, …), if any. */
function folderOf(item: QueryItem): FolderId | null {
	if (!isTerm(item) || item.negate || !item.field) return null;
	const field = item.field.toLowerCase();
	const value = item.query.toLowerCase();
	if (field === 'in') return inFolders[value] ?? null;
	if (field === 'is') return isFolders[value] ?? null;
	return null;
}

/** The mailbox a required term names: "me@x.test" or "@x.test" for a domain. */
function mailboxOf(item: QueryItem): string | null {
	if (!isTerm(item) || item.negate || !item.field || !item.query) return null;
	const field = item.field.toLowerCase();
	const value = item.query.toLowerCase();
	if (field === 'mailbox') return value;
	if (field === 'domain') return `@${value.replace(/^@/, '')}`;
	return null;
}

export type Scope = { folder: FolderId | null; mailbox: string | null };

/**
 * Best effort: the sidebar folder and mailbox a query names. Only top-level,
 * non-negated terms count (top-level terms are all required), and the first
 * of each kind wins.
 */
export function queryScope(text: string): Scope {
	const items = parseQuery(text, syntax);
	return {
		folder: items.map(folderOf).find(Boolean) ?? null,
		mailbox: items.map(mailboxOf).find(Boolean) ?? null,
	};
}

/**
 * The query with its folder and/or mailbox swapped (null removes it; a key
 * left out keeps what the query has). Everything else typed stays.
 */
export function withScope(
	text: string,
	scope: { folder?: FolderId | null; mailbox?: string | null },
): string {
	const items = parseQuery(text, syntax);
	let folderTerms = items.filter((it) => folderOf(it));
	let mailboxTerms = items.filter((it) => mailboxOf(it));
	const rest = items.filter((it) => !folderOf(it) && !mailboxOf(it));
	if (scope.folder !== undefined) {
		const query = folders.find((f) => f.id === scope.folder)?.query;
		folderTerms = query ? parseQuery(query, syntax) : [];
	}
	if (scope.mailbox !== undefined) {
		const mailbox = scope.mailbox;
		const term: QueryTerm | null = !mailbox
			? null
			: mailbox.startsWith('@')
				? { field: 'domain', query: mailbox.slice(1) }
				: { field: 'mailbox', query: mailbox };
		mailboxTerms = term ? [term] : [];
	}
	return stringifyQuery([...folderTerms, ...mailboxTerms, ...rest], syntax);
}

export type SearchSegment = { text: string; kind: 'field' | 'negate' | 'group' | 'text' };

/**
 * Lossless syntax colouring for the search box. An unknown prefix is searched
 * as text, so it is coloured as text rather than greyed out as a dead clause.
 */
export function searchSegments(text: string): SearchSegment[] {
	return querySegments(text, isSearchField).map((segment: QuerySegment) => ({
		text: segment.text,
		kind:
			segment.kind === 'name'
				? 'field'
				: segment.kind === 'negate' || segment.kind === 'group'
					? segment.kind
					: 'text',
	}));
}

import { parseQuery, querySegments, type QuerySegment } from '@gera2ld/filter-query';

// The fields the server binds (server/internal/store/query.go). This copy only
// colours the search box and fills the help menu; the server decides, and
// searches an unknown field as the literal text "name:value".
export const searchFields = [
	{ name: 'from', hint: 'Sender name or address', example: 'from:pat' },
	{ name: 'to', hint: 'A To recipient', example: 'to:me@x.com' },
	{ name: 'cc', hint: 'A Cc recipient', example: 'cc:team' },
	{ name: 'subject', hint: 'Subject only', example: 'subject:invoice' },
	{ name: 'body', hint: 'Message text only', example: 'body:"see you"' },
	{ name: 'mailbox', hint: 'Receiving address', example: 'mailbox:shop@' },
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
	return JSON.stringify(parseQuery(text));
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

export const folders = [
	{ id: 'inbox', label: 'Inbox' },
	{ id: 'starred', label: 'Starred' },
	{ id: 'sent', label: 'Sent' },
	{ id: 'drafts', label: 'Drafts' },
	{ id: 'archive', label: 'Archive' },
	{ id: 'trash', label: 'Trash' },
] as const;

export type FolderId = (typeof folders)[number]['id'];

export function isFolder(id: string): id is FolderId {
	return folders.some((f) => f.id === id);
}

type Filter = (raw: string, params?: Record<string, unknown>) => string;

const folderFilters: Record<FolderId, string | null> = {
	inbox: 'has_inbound = true && is_archived = false && trashed_at = ""',
	starred: 'has_starred = true && trashed_at = ""',
	sent: 'has_outbound = true && trashed_at = ""',
	drafts: null, // drafts are messages, not threads: see draftFilter
	archive: 'is_archived = true && trashed_at = ""',
	trash: 'trashed_at != ""',
};

/**
 * The PocketBase filter for a folder, optionally narrowed to one receiving
 * address ("me@x.com") or a whole domain ("@x.com"). Returns null for folders
 * with nothing to list yet. `filter` is pb.filter, which escapes the params.
 */
export function threadFilter(folder: FolderId, mailbox: string, filter: Filter): string | null {
	const base = folderFilters[folder];
	if (base === null || !mailbox) return base;
	// mailboxes is a JSON array of strings: anchor on the quotes around each entry.
	const needle = mailbox.startsWith('@') ? `${mailbox}"` : `"${mailbox}"`;
	return `(${base}) && ${filter('mailboxes ~ {:mb}', { mb: needle.toLowerCase() })}`;
}

/** Unsent outbound messages (drafts and failed sends), optionally from one address or domain. */
export function draftFilter(mailbox: string, filter: Filter): string {
	const base = 'direction = "outbound" && (send_status = "draft" || send_status = "failed")';
	if (!mailbox) return base;
	return mailbox.startsWith('@')
		? `${base} && ${filter('from_address ~ {:d}', { d: `%${mailbox.toLowerCase()}` })}`
		: `${base} && ${filter('from_address = {:a}', { a: mailbox.toLowerCase() })}`;
}

import type { FolderId } from './folders.js';

/** Link to a folder (or a thread in it), keeping the mailbox filter. */
export function folderHref(folder: FolderId, mailbox = '', thread = ''): string {
	const path = thread ? `/${folder}/${encodeURIComponent(thread)}` : `/${folder}`;
	return mailbox ? `${path}?mb=${encodeURIComponent(mailbox)}` : path;
}

import { redirect } from '@sveltejs/kit';
import { mailHref } from '#lib/mail/nav.js';
import { withScope } from '#lib/mail/search.js';
import type { FolderId } from '#lib/mail/folders.js';

// Old thread links (/inbox/<id>, as earlier Telegram alerts carry) open the thread.
export function load({ params, url }) {
	const mailbox = url.searchParams.get('mb');
	const query = withScope('', { folder: params.folder as FolderId, mailbox });
	redirect(307, mailHref(query, params.thread));
}

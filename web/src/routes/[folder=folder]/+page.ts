import { redirect } from '@sveltejs/kit';
import { mailHref } from '#lib/mail/nav.js';
import { withScope } from '#lib/mail/search.js';
import type { FolderId } from '#lib/mail/folders.js';

// Old folder paths (/inbox?mb=…) live on as queries.
export function load({ params, url }) {
	const mailbox = url.searchParams.get('mb');
	redirect(307, mailHref(withScope('', { folder: params.folder as FolderId, mailbox })));
}

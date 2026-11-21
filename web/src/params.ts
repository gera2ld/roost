import { defineParams } from '@sveltejs/kit/params';

// Self-contained: the build loads this file with plain Node, which cannot follow
// #lib imports to .ts sources. folders.test.ts keeps the list in sync.
// These are the old folder paths (/inbox, /sent/<thread>, …), kept so bookmarks
// and alert links redirect to their query; Drafts is a route of its own.
export const folderIds = ['inbox', 'starred', 'sent', 'archive', 'trash'] as const;

export const params = defineParams({
	folder: (param) => ((folderIds as readonly string[]).includes(param) ? param : undefined),
});

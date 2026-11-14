import { defineParams } from '@sveltejs/kit/params';

// Self-contained: the build loads this file with plain Node, which cannot follow
// #lib imports to .ts sources. folders.test.ts keeps the list in sync.
export const folderIds = ['inbox', 'starred', 'sent', 'drafts', 'archive', 'trash'] as const;

export const params = defineParams({
	folder: (param) => ((folderIds as readonly string[]).includes(param) ? param : undefined),
});

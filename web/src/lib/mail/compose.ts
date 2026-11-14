import { goto } from '$app/navigation';
import { toast } from 'svelte-sonner';
import { createDraft, type DraftMode } from '#lib/pb.js';
import { errorMessage } from './errors.js';

/** Starts a draft on the server and opens it in the composer. */
export async function startDraft(mode: DraftMode, source = '') {
	try {
		const draft = await createDraft(mode, source);
		await goto(`/compose/${draft.id}`);
	} catch (err) {
		toast.error(errorMessage(err, 'Could not start a draft'));
	}
}

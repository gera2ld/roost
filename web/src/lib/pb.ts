import PocketBase, { type RecordModel } from 'pocketbase';

// Same origin in production (PocketBase serves the app); Vite proxies /api in dev.
export const pb = new PocketBase('/');
pb.autoCancellation(false);

export type Address = { name?: string; address: string };

export interface Thread extends RecordModel {
	subject: string;
	participants: Address[];
	latest_at: string;
	message_count: number;
	unread_count: number;
	snippet: string;
	is_archived: boolean;
	trashed_at: string;
	has_inbound: boolean;
	has_outbound: boolean;
	has_starred: boolean;
	mailboxes: string[];
}

export interface Message extends RecordModel {
	subject: string;
	from_name: string;
	from_address: string;
	to: Address[] | null;
	cc: Address[] | null;
	reply_to: Address[] | null;
	received_at: string;
	direction: 'inbound' | 'outbound';
	text_body: string;
	body_source: string;
	is_read: boolean;
	is_starred: boolean;
	thread: string;
	mailbox: string;
	in_reply_to: string;
	bcc: Address[] | null;
	send_status: '' | SendStatus;
	delivery_status: '' | 'delivered' | 'delayed' | 'bounced' | 'complained';
	delivery_at: string;
	send_error: string;
	forward_attachments: string[] | null;
}

export type SendStatus = 'draft' | 'sending' | 'sent' | 'failed';
export type DraftMode = 'new' | 'reply' | 'reply_all' | 'forward';

export interface DraftPatch {
	from_name?: string;
	from_address?: string;
	to?: string;
	cc?: string;
	bcc?: string;
	subject?: string;
	text_body?: string;
	forward_attachments?: string[];
}

export interface Attachment extends RecordModel {
	message: string;
	filename: string;
	content_type: string;
	size: number;
	disposition: string;
}

export interface Mailbox {
	address: string;
	domain: string;
	total: number;
	unread: number;
}

export interface Domain extends RecordModel {
	domain: string;
	send_enabled: boolean;
}

export interface Identity extends RecordModel {
	address: string;
	display_name: string;
	signature: string;
}

export function listDomains() {
	return pb.collection('domains').getFullList<Domain>({ sort: 'domain' });
}

export function listIdentities() {
	return pb.collection('identities').getFullList<Identity>({ sort: 'address' });
}

export function addDomain(domain: string) {
	return pb.send<Domain>('/api/roost/domains', { method: 'POST', body: { domain } });
}

export function setDomainSending(id: string, sendEnabled: boolean) {
	return pb.send<Domain>(`/api/roost/domains/${encodeURIComponent(id)}`, {
		method: 'PATCH',
		body: { send_enabled: sendEnabled },
	});
}

export function saveIdentity(identity: Pick<Identity, 'address' | 'display_name' | 'signature'>) {
	return pb.send<Identity>('/api/roost/identities', { method: 'PUT', body: identity });
}

export function deleteIdentity(id: string) {
	return pb.send(`/api/roost/identities/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export type ThreadAction =
	| 'read'
	| 'unread'
	| 'archive'
	| 'unarchive'
	| 'trash'
	| 'restore'
	| 'purge';
export type MessageAction = 'read' | 'unread' | 'star' | 'unstar';

export function threadAction(id: string, action: ThreadAction) {
	return pb.send<Thread | null>(`/api/roost/threads/${encodeURIComponent(id)}/actions`, {
		method: 'POST',
		body: { action },
	});
}

export function messageAction(id: string, action: MessageAction) {
	return pb.send<Message>(`/api/roost/messages/${encodeURIComponent(id)}/actions`, {
		method: 'POST',
		body: { action },
	});
}

export function createDraft(mode: DraftMode, source = '') {
	const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
	return pb.send<Message>('/api/roost/drafts', { method: 'POST', body: { mode, source, tz } });
}

export function updateDraft(id: string, patch: DraftPatch) {
	return pb.send<Message>(`/api/roost/drafts/${encodeURIComponent(id)}`, {
		method: 'PATCH',
		body: patch,
	});
}

export function deleteDraft(id: string) {
	return pb.send(`/api/roost/drafts/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export function sendDraft(id: string) {
	return pb.send<Message>(`/api/roost/drafts/${encodeURIComponent(id)}/send`, { method: 'POST' });
}

export function searchThreads(q: string) {
	return pb.send<Thread[]>('/api/roost/search', { query: { q } });
}

export function listMailboxes() {
	return pb.send<Mailbox[]>('/api/roost/mailboxes', {});
}

// Downloads go through fetch with the auth header (never a token in the URL),
// then save as a blob. The server always sends application/octet-stream.
export async function downloadAttachment(att: Attachment) {
	const res = await fetch(pb.buildURL(`/api/roost/attachments/${encodeURIComponent(att.id)}`), {
		headers: { Authorization: pb.authStore.token },
	});
	if (!res.ok) throw new Error(`Download failed (${res.status})`);
	const url = URL.createObjectURL(await res.blob());
	const a = document.createElement('a');
	a.href = url;
	a.download = att.filename || 'attachment';
	a.click();
	setTimeout(() => URL.revokeObjectURL(url), 1000);
}

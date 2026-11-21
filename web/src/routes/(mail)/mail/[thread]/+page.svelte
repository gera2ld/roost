<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { toast } from 'svelte-sonner';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import ArchiveIcon from '@lucide/svelte/icons/archive';
	import ArchiveRestoreIcon from '@lucide/svelte/icons/archive-restore';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import MailIcon from '@lucide/svelte/icons/mail';
	import CircleXIcon from '@lucide/svelte/icons/circle-x';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Skeleton } from '#lib/components/ui/skeleton/index.js';
	import MessageView from '#lib/components/mail/message-view.svelte';
	import { mailHref, urlQuery } from '#lib/mail/nav.js';
	import { live } from '#lib/mail/session.svelte.js';
	import {
		pb,
		threadAction,
		type Attachment,
		type Message,
		type Thread,
		type ThreadAction,
	} from '#lib/pb.js';

	const id = $derived(page.params.thread ?? '');
	const back = $derived(mailHref(urlQuery(page.url)));

	let thread = $state<Thread | null>(null);
	let messages = $state<Message[]>([]);
	let attachments = $state<Record<string, Attachment[]>>({});
	let missing = $state(false);
	let markedRead = '';

	$effect(() => {
		void live.version;
		load(id);
	});

	let seq = 0;
	async function load(threadId: string) {
		const mine = ++seq;
		try {
			const [t, msgs, atts] = await Promise.all([
				pb.collection('threads').getOne<Thread>(threadId),
				pb.collection('messages').getFullList<Message>({
					filter: pb.filter('thread = {:id}', { id: threadId }),
					sort: 'received_at',
				}),
				pb.collection('attachments').getFullList<Attachment>({
					filter: pb.filter('message.thread = {:id} && disposition != "inline"', { id: threadId }),
					sort: 'filename',
				}),
			]);
			if (mine !== seq) return;
			thread = t;
			messages = msgs;
			const byMessage: Record<string, Attachment[]> = {};
			for (const a of atts) (byMessage[a.message] ??= []).push(a);
			attachments = byMessage;
			missing = false;
			// Opening a thread reads it, once per visit (so "mark unread" sticks).
			if (t.unread_count > 0 && markedRead !== threadId) {
				markedRead = threadId;
				await threadAction(threadId, 'read');
			}
		} catch {
			if (mine === seq) missing = true;
		}
	}

	async function act(action: ThreadAction, done: string, leave = true) {
		try {
			await threadAction(id, action);
			toast.success(done);
			if (leave) await goto(back);
		} catch (err) {
			toast.error(err instanceof Error ? err.message : 'Action failed');
		}
	}

	function purge() {
		if (confirm('Delete this conversation forever? This cannot be undone.')) {
			act('purge', 'Deleted forever');
		}
	}

	const trashed = $derived(Boolean(thread?.trashed_at));
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<header class="flex items-center gap-1 border-b p-2">
		<Button variant="ghost" size="icon" class="md:hidden" href={back} aria-label="Back">
			<ArrowLeftIcon />
		</Button>
		{#if thread}
			{#if trashed}
				<Button
					variant="ghost"
					size="sm"
					aria-label="Restore"
					onclick={() => act('restore', 'Restored')}
				>
					<RotateCcwIcon /><span class="hidden sm:inline">Restore</span>
				</Button>
				<Button
					variant="ghost"
					size="sm"
					class="text-destructive"
					aria-label="Delete forever"
					onclick={purge}
				>
					<CircleXIcon /><span class="hidden sm:inline">Delete forever</span>
				</Button>
			{:else}
				{#if thread.is_archived}
					<Button
						variant="ghost"
						size="sm"
						aria-label="Unarchive"
						onclick={() => act('unarchive', 'Moved to inbox')}
					>
						<ArchiveRestoreIcon /><span class="hidden sm:inline">Unarchive</span>
					</Button>
				{:else}
					<Button
						variant="ghost"
						size="sm"
						aria-label="Archive"
						onclick={() => act('archive', 'Archived')}
					>
						<ArchiveIcon /><span class="hidden sm:inline">Archive</span>
					</Button>
				{/if}
				<Button
					variant="ghost"
					size="sm"
					aria-label="Trash"
					onclick={() => act('trash', 'Moved to trash')}
				>
					<TrashIcon /><span class="hidden sm:inline">Trash</span>
				</Button>
			{/if}
			<Button
				variant="ghost"
				size="sm"
				class="ml-auto"
				aria-label="Mark unread"
				onclick={() => act('unread', 'Marked unread')}
				disabled={!thread.has_inbound}
			>
				<MailIcon /><span class="hidden sm:inline">Mark unread</span>
			</Button>
		{/if}
	</header>

	<div class="flex-1 overflow-y-auto">
		{#if missing}
			<p class="p-6 text-sm text-muted-foreground">This conversation is gone.</p>
		{:else if !thread}
			<div class="flex flex-col gap-3 p-6">
				<Skeleton class="h-6 w-1/2" />
				<Skeleton class="h-24 w-full" />
			</div>
		{:else}
			<article class="mx-auto flex max-w-3xl flex-col gap-4 p-4 md:p-6">
				<h1 class="text-xl font-semibold break-words">{thread.subject || '(no subject)'}</h1>
				{#if trashed}
					<p class="text-sm text-muted-foreground">
						In trash: deleted forever 30 days after it was trashed.
					</p>
				{/if}
				{#each messages as m (m.id)}
					<MessageView message={m} attachments={attachments[m.id] ?? []} />
				{/each}
			</article>
		{/if}
	</div>
</div>

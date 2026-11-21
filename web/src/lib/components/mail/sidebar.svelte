<script lang="ts">
	import InboxIcon from '@lucide/svelte/icons/inbox';
	import StarIcon from '@lucide/svelte/icons/star';
	import SendIcon from '@lucide/svelte/icons/send';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import ArchiveIcon from '@lucide/svelte/icons/archive';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import { startDraft } from '#lib/mail/compose.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Separator } from '#lib/components/ui/separator/index.js';
	import { folders, type FolderId } from '#lib/mail/folders.js';
	import { draftsHref, mailHref } from '#lib/mail/nav.js';
	import { withScope, type Scope } from '#lib/mail/search.js';
	import { live, session } from '#lib/mail/session.svelte.js';
	import { listMailboxes, type Mailbox } from '#lib/pb.js';
	import { cn } from '#lib/utils.js';

	// Every entry is a query (Drafts aside): a link swaps its folder or mailbox
	// term in the current query and keeps the rest. Highlighting follows the
	// query (scope), at best effort.
	let { query, scope, onnavigate }: { query: string; scope: Scope; onnavigate?: () => void } =
		$props();

	const drafts = $derived(scope.folder === 'drafts');
	// On Drafts there is no query; keep its mailbox when leaving for a folder.
	const base = $derived(drafts ? withScope('', { mailbox: scope.mailbox }) : query);

	function folderLink(id: FolderId): string {
		if (id === 'drafts') return draftsHref(scope.mailbox ?? '');
		return mailHref(withScope(base, { folder: id }));
	}

	function mailboxLink(mailbox: string | null): string {
		return drafts ? draftsHref(mailbox ?? '') : mailHref(withScope(query, { mailbox }));
	}

	const icons = {
		inbox: InboxIcon,
		starred: StarIcon,
		sent: SendIcon,
		drafts: FileTextIcon,
		archive: ArchiveIcon,
		trash: TrashIcon,
	};

	let boxes = $state<Mailbox[]>([]);
	$effect(() => {
		void live.version;
		listMailboxes()
			.then((b) => (boxes = b))
			.catch(() => {});
	});

	const domains = $derived.by(() => {
		const map = new Map<string, { unread: number; boxes: Mailbox[] }>();
		for (const b of boxes) {
			const d = map.get(b.domain) ?? { unread: 0, boxes: [] };
			d.unread += b.unread;
			d.boxes.push(b);
			map.set(b.domain, d);
		}
		return [...map];
	});

	const itemClass = (active: boolean) =>
		cn(
			'flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted',
			active && 'bg-muted font-medium',
		);
</script>

<nav class="flex h-full flex-col gap-1 p-2" aria-label="Folders">
	<div class="px-2 py-3 text-lg font-semibold">Roost</div>
	<Button
		class="mb-2 justify-start"
		onclick={() => {
			onnavigate?.();
			startDraft('new');
		}}
	>
		<PencilIcon /> Compose
	</Button>
	{#each folders as f (f.id)}
		{@const Icon = icons[f.id]}
		<a
			href={folderLink(f.id)}
			class={itemClass(f.id === scope.folder)}
			aria-current={f.id === scope.folder ? 'page' : undefined}
			onclick={onnavigate}
		>
			<Icon class="size-4 text-muted-foreground" />
			{f.label}
		</a>
	{/each}

	{#if domains.length}
		<Separator class="my-2" />
		<a href={mailboxLink(null)} class={itemClass(!scope.mailbox)} onclick={onnavigate}
			>All mailboxes</a
		>
		{#each domains as [domain, d] (domain)}
			<a
				href={mailboxLink('@' + domain)}
				class={itemClass(scope.mailbox === '@' + domain)}
				onclick={onnavigate}
			>
				<span class="truncate">@{domain}</span>
				{#if d.unread}<span class="ml-auto text-xs text-muted-foreground">{d.unread}</span>{/if}
			</a>
			{#each d.boxes as b (b.address)}
				<a
					href={mailboxLink(b.address)}
					class={cn(itemClass(scope.mailbox === b.address), 'pl-5 text-muted-foreground')}
					onclick={onnavigate}
				>
					<span class="truncate">{b.address.split('@')[0]}</span>
					{#if b.unread}<span class="ml-auto text-xs">{b.unread}</span>{/if}
				</a>
			{/each}
		{/each}
	{/if}

	<div class="mt-auto">
		<Button variant="ghost" class="w-full justify-start" href="/settings" onclick={onnavigate}>
			<SettingsIcon /> Settings
		</Button>
		<Button variant="ghost" class="w-full justify-start" onclick={() => session.signOut()}>
			<LogOutIcon /> Sign out
		</Button>
	</div>
</nav>

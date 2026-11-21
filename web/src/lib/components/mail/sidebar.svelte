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
	import MailsIcon from '@lucide/svelte/icons/mails';
	import PanelLeftCloseIcon from '@lucide/svelte/icons/panel-left-close';
	import PanelLeftOpenIcon from '@lucide/svelte/icons/panel-left-open';
	import type { Component } from 'svelte';
	import IconButton from '#lib/components/mail/icon-button.svelte';
	import { startDraft } from '#lib/mail/compose.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Separator } from '#lib/components/ui/separator/index.js';
	import * as Tooltip from '#lib/components/ui/tooltip/index.js';
	import { folders, type FolderId } from '#lib/mail/folders.js';
	import { draftsHref, mailHref } from '#lib/mail/nav.js';
	import { withScope, type Scope } from '#lib/mail/search.js';
	import { live, session } from '#lib/mail/session.svelte.js';
	import { listMailboxes, type Mailbox } from '#lib/pb.js';
	import { cn } from '#lib/utils.js';

	// Every entry is a query (Drafts aside): a link swaps its folder or mailbox
	// term in the current query and keeps the rest. Highlighting follows the
	// query (scope), at best effort. Collapsed, it shows icons only, with the
	// labels as tooltips; ontoggle adds the button that collapses or expands it.
	let {
		query,
		scope,
		collapsed = false,
		ontoggle,
		onnavigate,
	}: {
		query: string;
		scope: Scope;
		collapsed?: boolean;
		ontoggle?: () => void;
		onnavigate?: () => void;
	} = $props();

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
			collapsed && 'relative size-9 justify-center p-0',
			active && 'bg-muted font-medium',
		);
	function compose() {
		onnavigate?.();
		startDraft('new');
	}
</script>

<!--
	A sidebar link. Collapsed, it shows its icon (or the label's first letter)
	and a dot for unread mail, with the label as a tooltip.
-->
{#snippet item(
	href: string,
	label: string,
	active: boolean,
	icon: Component | null,
	unread = 0,
	nested = false,
)}
	{@const Icon = icon}
	<Tooltip.Root disabled={!collapsed}>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<a
					{...props}
					{href}
					class={cn(itemClass(active), nested && !collapsed && 'pl-5 text-muted-foreground')}
					aria-current={active ? 'page' : undefined}
					aria-label={collapsed ? label : undefined}
					onclick={onnavigate}
				>
					{#if Icon}
						<Icon class="size-4 text-muted-foreground" />
					{:else if collapsed}
						<span class="text-xs font-semibold uppercase">{label.replace('@', '')[0]}</span>
					{/if}
					{#if collapsed}
						{#if unread}
							<span class="absolute top-1.5 right-1.5 size-1.5 rounded-full bg-primary"></span>
						{/if}
					{:else}
						<span class="truncate">{label}</span>
						{#if unread}
							<span class={cn('ml-auto text-xs', !nested && 'text-muted-foreground')}>{unread}</span
							>
						{/if}
					{/if}
				</a>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content side="right">{label}</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

<nav class={cn('flex h-full flex-col gap-1 p-2', collapsed && 'items-center')} aria-label="Folders">
	<div class={cn('flex h-12 items-center', !collapsed && 'pl-2')}>
		{#if !collapsed}<span class="text-lg font-semibold">Roost</span>{/if}
		{#if ontoggle}
			<IconButton
				label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
				side="right"
				class={cn(!collapsed && 'ml-auto text-muted-foreground')}
				aria-expanded={!collapsed}
				onclick={ontoggle}
			>
				{#if collapsed}<PanelLeftOpenIcon />{:else}<PanelLeftCloseIcon />{/if}
			</IconButton>
		{/if}
	</div>
	{#if collapsed}
		<IconButton label="Compose" side="right" variant="default" class="mb-2" onclick={compose}>
			<PencilIcon />
		</IconButton>
	{:else}
		<Button class="mb-2 justify-start" onclick={compose}>
			<PencilIcon /> Compose
		</Button>
	{/if}
	{#each folders as f (f.id)}
		{@render item(folderLink(f.id), f.label, f.id === scope.folder, icons[f.id])}
	{/each}

	{#if domains.length}
		<Separator class="my-2" />
		{@render item(mailboxLink(null), 'All mailboxes', !scope.mailbox, collapsed ? MailsIcon : null)}
		{#each domains as [domain, d] (domain)}
			{@render item(
				mailboxLink('@' + domain),
				'@' + domain,
				scope.mailbox === '@' + domain,
				null,
				d.unread,
			)}
			<!-- Collapsed, single addresses are left out: a letter each would not tell them apart. -->
			{#if !collapsed}
				{#each d.boxes as b (b.address)}
					{@render item(
						mailboxLink(b.address),
						b.address.split('@')[0],
						scope.mailbox === b.address,
						null,
						b.unread,
						true,
					)}
				{/each}
			{/if}
		{/each}
	{/if}

	<div class={cn('mt-auto', collapsed && 'flex flex-col items-center gap-1')}>
		{#if collapsed}
			<IconButton label="Settings" side="right" href="/settings" onclick={onnavigate}>
				<SettingsIcon />
			</IconButton>
			<IconButton label="Sign out" side="right" onclick={() => session.signOut()}>
				<LogOutIcon />
			</IconButton>
		{:else}
			<Button variant="ghost" class="w-full justify-start" href="/settings" onclick={onnavigate}>
				<SettingsIcon /> Settings
			</Button>
			<Button variant="ghost" class="w-full justify-start" onclick={() => session.signOut()}>
				<LogOutIcon /> Sign out
			</Button>
		{/if}
	</div>
</nav>

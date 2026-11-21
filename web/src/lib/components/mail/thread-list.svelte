<script lang="ts">
	import StarIcon from '@lucide/svelte/icons/star';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Skeleton } from '#lib/components/ui/skeleton/index.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { draftFilter, folders } from '#lib/mail/folders.js';
	import { formatList } from '#lib/mail/address.js';
	import { mailHref } from '#lib/mail/nav.js';
	import { live } from '#lib/mail/session.svelte.js';
	import { personLabel, shortDate } from '#lib/mail/text.js';
	import { pb, searchThreads, type Message, type Thread } from '#lib/pb.js';
	import { cn } from '#lib/utils.js';

	// Every mail list is a search; Drafts (messages, not threads) is listed apart.
	let {
		drafts: draftsView,
		mailbox,
		query,
		selected,
	}: { drafts: boolean; mailbox: string; query: string; selected: string } = $props();

	// A bare folder (or no query) is a place, not a search that found nothing.
	const isFolder = $derived(!query.trim() || folders.some((f) => f.query === query.trim()));

	const perPage = 50;
	let threads = $state<Thread[]>([]);
	let drafts = $state<Message[] | null>(null); // set only in the Drafts folder
	let pages = $state(1); // pages loaded; reloads keep them
	let more = $state(false);
	let loading = $state(true);
	let error = $state('');

	// Reset paging when the view changes.
	$effect(() => {
		void [draftsView, mailbox, query];
		pages = 1;
		threads = [];
		drafts = null;
		loading = true;
	});

	$effect(() => {
		void live.version;
		load({ drafts: draftsView, mailbox, query, pages });
	});

	let seq = 0;
	async function load(view: { drafts: boolean; mailbox: string; query: string; pages: number }) {
		const mine = ++seq;
		const limit = perPage * view.pages;
		try {
			if (view.drafts) {
				const res = await pb.collection('messages').getList<Message>(1, limit, {
					filter: draftFilter(view.mailbox, pb.filter.bind(pb)),
					sort: '-updated',
					skipTotal: true,
				});
				if (mine !== seq) return;
				drafts = res.items;
				threads = [];
				more = res.items.length === limit;
			} else {
				const items = await searchThreads(view.query, limit);
				if (mine !== seq) return;
				drafts = null;
				threads = items;
				more = items.length === limit;
			}
			error = '';
		} catch (err) {
			if (mine === seq) error = err instanceof Error ? err.message : 'Could not load mail';
		} finally {
			if (mine === seq) loading = false;
		}
	}

	function people(t: Thread): string {
		if (!t.participants?.length) return t.has_outbound ? 'Me' : '(unknown)';
		return t.participants
			.slice(0, 3)
			.map((p) => personLabel(p).split(/[\s@]/)[0])
			.join(', ');
	}
</script>

<div class="flex-1 overflow-y-auto" aria-busy={loading}>
	{#if loading && !threads.length}
		{#each { length: 6 }, i (i)}
			<div class="flex flex-col gap-2 border-b p-3">
				<Skeleton class="h-4 w-1/3" />
				<Skeleton class="h-4 w-2/3" />
			</div>
		{/each}
	{:else if error}
		<p class="p-4 text-sm text-destructive" role="alert">{error}</p>
	{:else if drafts}
		{#if !drafts.length}
			<p class="p-6 text-center text-sm text-muted-foreground">No drafts.</p>
		{:else}
			<ul>
				{#each drafts as d (d.id)}
					<li>
						<a
							href="/compose/{d.id}"
							class="flex flex-col gap-0.5 border-b px-3 py-2.5 hover:bg-muted/60"
						>
							<div class="flex items-baseline gap-2">
								<span class="truncate text-sm">{formatList(d.to) || '(no recipients)'}</span>
								{#if d.send_status === 'failed'}<Badge variant="destructive">Not sent</Badge>{/if}
								<span class="ml-auto shrink-0 text-xs text-muted-foreground"
									>{shortDate(d.updated)}</span
								>
							</div>
							<div class="truncate text-sm text-foreground/80">{d.subject || '(no subject)'}</div>
							<div class="truncate text-xs text-muted-foreground">
								{d.text_body.trim().slice(0, 140)}
							</div>
						</a>
					</li>
				{/each}
			</ul>
		{/if}
	{:else if !threads.length}
		<p class="p-6 text-center text-sm text-muted-foreground">
			{#if isFolder}Nothing here.{:else}No mail matches “{query}”.{/if}
		</p>
	{:else}
		<ul>
			{#each threads as t (t.id)}
				{@const unread = t.unread_count > 0}
				<li>
					<a
						href={mailHref(query, t.id)}
						class={cn(
							'flex flex-col gap-0.5 border-b px-3 py-2.5 hover:bg-muted/60',
							t.id === selected && 'bg-muted',
						)}
						aria-current={t.id === selected ? 'true' : undefined}
					>
						<div class="flex items-baseline gap-2">
							<span class={cn('truncate text-sm', unread && 'font-semibold')}>{people(t)}</span>
							{#if t.message_count > 1}
								<span class="text-xs text-muted-foreground">{t.message_count}</span>
							{/if}
							{#if t.has_starred}
								<StarIcon
									class="size-3.5 shrink-0 fill-amber-400 text-amber-400"
									aria-label="Starred"
								/>
							{/if}
							<span class="ml-auto shrink-0 text-xs text-muted-foreground">
								{shortDate(t.latest_at)}
							</span>
						</div>
						<div class={cn('truncate text-sm', unread ? 'font-semibold' : 'text-foreground/80')}>
							{t.subject || '(no subject)'}
						</div>
						<div class="truncate text-xs text-muted-foreground">{t.snippet}</div>
					</a>
				</li>
			{/each}
		</ul>
		{#if more}
			<div class="p-3 text-center">
				<Button variant="ghost" onclick={() => pages++}>Load more</Button>
			</div>
		{/if}
	{/if}
</div>

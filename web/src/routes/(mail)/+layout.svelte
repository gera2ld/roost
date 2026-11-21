<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import MenuIcon from '@lucide/svelte/icons/menu';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Sheet from '#lib/components/ui/sheet/index.js';
	import SearchBox from '#lib/components/mail/search-box.svelte';
	import Sidebar from '#lib/components/mail/sidebar.svelte';
	import ThreadList from '#lib/components/mail/thread-list.svelte';
	import { mailHref, urlQuery } from '#lib/mail/nav.js';
	import { queryScope, type Scope } from '#lib/mail/search.js';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	// Mail lists are queries (/mail?q=…); Drafts is the one list that is not.
	const drafts = $derived(page.route.id === '/(mail)/drafts');
	const query = $derived(drafts ? '' : urlQuery(page.url));
	const scope: Scope = $derived(
		drafts
			? { folder: 'drafts', mailbox: page.url.searchParams.get('mb') || null }
			: queryScope(query),
	);
	const threadOpen = $derived(Boolean(page.params.thread));

	let menuOpen = $state(false);
	let draft = $state('');
	$effect(() => {
		draft = query;
	});

	function search() {
		goto(mailHref(draft.trim()));
	}

	function clearSearch() {
		draft = '';
		goto(mailHref(''));
	}
</script>

<div class="flex h-dvh overflow-hidden">
	<aside class="hidden w-60 shrink-0 border-r lg:block">
		<Sidebar {query} {scope} />
	</aside>

	<div class="flex min-w-0 flex-1 flex-col">
		<!-- Full width beside the sidebar: the query language deserves the room. -->
		<header class={['items-center gap-2 border-b p-2', threadOpen ? 'hidden md:flex' : 'flex']}>
			<Sheet.Root bind:open={menuOpen}>
				<Sheet.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="ghost" size="icon" class="lg:hidden" aria-label="Menu">
							<MenuIcon />
						</Button>
					{/snippet}
				</Sheet.Trigger>
				<Sheet.Content side="left" class="w-64 p-0">
					<Sheet.Title class="sr-only">Folders</Sheet.Title>
					<Sidebar {query} {scope} onnavigate={() => (menuOpen = false)} />
				</Sheet.Content>
			</Sheet.Root>
			<SearchBox
				bind:value={draft}
				active={Boolean(query)}
				onsearch={search}
				onclear={clearSearch}
			/>
		</header>

		<div class="flex min-h-0 flex-1">
			<section
				class={[
					'flex min-w-0 flex-col border-r md:w-96 md:shrink-0',
					threadOpen ? 'hidden md:flex' : 'flex flex-1',
				]}
			>
				<ThreadList
					{drafts}
					{query}
					mailbox={scope.mailbox ?? ''}
					selected={page.params.thread ?? ''}
				/>
			</section>

			<main class={['min-w-0 flex-1', threadOpen ? 'flex' : 'hidden md:flex']}>
				{@render children()}
			</main>
		</div>
	</div>
</div>

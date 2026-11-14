<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import MenuIcon from '@lucide/svelte/icons/menu';
	import SearchIcon from '@lucide/svelte/icons/search';
	import XIcon from '@lucide/svelte/icons/x';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import * as Sheet from '#lib/components/ui/sheet/index.js';
	import Sidebar from '#lib/components/mail/sidebar.svelte';
	import ThreadList from '#lib/components/mail/thread-list.svelte';
	import { folders, type FolderId } from '#lib/mail/folders.js';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	const folder = $derived(page.params.folder as FolderId);
	const mailbox = $derived(page.url.searchParams.get('mb') ?? '');
	const query = $derived(page.url.searchParams.get('q') ?? '');
	const threadOpen = $derived(Boolean(page.params.thread));
	const title = $derived(folders.find((f) => f.id === folder)?.label ?? '');

	let menuOpen = $state(false);
	let draft = $state('');
	$effect(() => {
		draft = query;
	});

	function search(event: SubmitEvent) {
		event.preventDefault();
		const url = new URL(page.url.href);
		url.pathname = `/${folder}`;
		if (draft.trim()) url.searchParams.set('q', draft.trim());
		else url.searchParams.delete('q');
		goto(url.pathname + url.search);
	}

	function clearSearch() {
		draft = '';
		const url = new URL(page.url.href);
		url.searchParams.delete('q');
		goto(url.pathname + url.search);
	}
</script>

<div class="flex h-dvh overflow-hidden">
	<aside class="hidden w-60 shrink-0 border-r lg:block">
		<Sidebar {folder} {mailbox} />
	</aside>

	<section
		class={[
			'flex min-w-0 flex-col border-r md:w-96 md:shrink-0',
			threadOpen ? 'hidden md:flex' : 'flex flex-1',
		]}
	>
		<header class="flex items-center gap-2 border-b p-2">
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
					<Sidebar {folder} {mailbox} onnavigate={() => (menuOpen = false)} />
				</Sheet.Content>
			</Sheet.Root>
			<form class="relative flex-1" role="search" onsubmit={search}>
				<SearchIcon
					class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
				/>
				<Input
					class="pr-8 pl-8"
					type="text"
					enterkeyhint="search"
					placeholder="Search all mail"
					aria-label="Search mail"
					bind:value={draft}
				/>
				{#if query}
					<button
						type="button"
						class="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground"
						aria-label="Clear search"
						onclick={clearSearch}
					>
						<XIcon class="size-4" />
					</button>
				{/if}
			</form>
		</header>
		<ThreadList {folder} {mailbox} {query} selected={page.params.thread ?? ''} />
	</section>

	<main class={['min-w-0 flex-1', threadOpen ? 'flex' : 'hidden md:flex']}>
		{@render children()}
	</main>
</div>

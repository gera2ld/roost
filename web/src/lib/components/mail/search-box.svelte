<script lang="ts">
	import SearchIcon from '@lucide/svelte/icons/search';
	import XIcon from '@lucide/svelte/icons/x';
	import CircleHelpIcon from '@lucide/svelte/icons/circle-help';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { searchFields, searchSegments } from '#lib/mail/search.js';

	let {
		value = $bindable(''),
		active,
		onsearch,
		onclear,
	}: { value: string; active: boolean; onsearch: () => void; onclear: () => void } = $props();

	let input = $state<HTMLInputElement | null>(null);
	let scroll = $state(0);
	const segments = $derived(searchSegments(value));

	const colour = {
		field: 'text-sky-700 dark:text-sky-400',
		negate: 'text-rose-600 dark:text-rose-400',
		group: 'text-muted-foreground',
		text: 'text-foreground',
	};

	// The colouring is drawn behind transparent input text, so it follows the
	// input's horizontal scroll. Inputs don't reliably fire scroll events (caret
	// moves, selection drags), so poll once a frame while focused.
	function sync() {
		scroll = input?.scrollLeft ?? 0;
	}

	let frame = 0;
	function follow() {
		sync();
		frame = requestAnimationFrame(follow);
	}
	function unfollow() {
		cancelAnimationFrame(frame);
		requestAnimationFrame(sync);
	}
	$effect(() => () => cancelAnimationFrame(frame));

	function submit(event: SubmitEvent) {
		event.preventDefault();
		onsearch();
	}

	function insert(example: string) {
		const prefix = example.slice(0, example.indexOf(':') + 1);
		value = value.trim() ? `${value.trimEnd()} ${prefix}` : prefix;
		requestAnimationFrame(() => {
			input?.focus();
			input?.setSelectionRange(value.length, value.length);
			sync();
		});
	}
</script>

<form class="relative min-w-0 flex-1" role="search" onsubmit={submit}>
	<SearchIcon
		class="pointer-events-none absolute top-1/2 left-2.5 z-10 size-4 -translate-y-1/2 text-muted-foreground"
	/>
	<Input
		bind:ref={input}
		bind:value
		class="pr-16 pl-8 text-transparent caret-foreground"
		type="text"
		enterkeyhint="search"
		autocomplete="off"
		spellcheck={false}
		placeholder="Search all mail — try from:pat is:unread after:7d"
		aria-label="Search mail"
		oninput={sync}
		onfocus={follow}
		onblur={unfollow}
	/>
	<div
		aria-hidden="true"
		class="pointer-events-none absolute inset-y-0 right-16 left-8 flex items-center overflow-hidden border border-transparent text-base whitespace-pre md:text-sm"
	>
		<span style:transform="translateX({-scroll}px)">
			{#each segments as segment, i (i)}<span class={colour[segment.kind]}>{segment.text}</span
				>{/each}
		</span>
	</div>
	<div class="absolute top-1/2 right-2 flex -translate-y-1/2 items-center gap-1.5">
		{#if active || value}
			<button
				type="button"
				class="text-muted-foreground hover:text-foreground"
				aria-label="Clear search"
				onclick={onclear}
			>
				<XIcon class="size-4" />
			</button>
		{/if}
		<DropdownMenu.Root>
			<DropdownMenu.Trigger
				class="text-muted-foreground hover:text-foreground"
				aria-label="Search syntax"
			>
				<CircleHelpIcon class="size-4" />
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="end" class="w-80">
				<DropdownMenu.Label>Search fields</DropdownMenu.Label>
				{#each searchFields as field (field.name)}
					<DropdownMenu.Item onSelect={() => insert(field.example)} class="items-baseline">
						<code class="w-32 shrink-0 font-mono text-xs text-sky-700 dark:text-sky-400"
							>{field.example}</code
						>
						<span class="text-xs text-muted-foreground">{field.hint}</span>
					</DropdownMenu.Item>
				{/each}
				<DropdownMenu.Separator />
				<div class="space-y-0.5 px-2 py-1.5 text-xs text-muted-foreground">
					<p><code>a b</code> either term · <code>(a b)</code> both terms</p>
					<p><code>!a</code> without · <code>"a b"</code> exact text</p>
					<p><code>from:(pat sam)</code> applies a field to a group</p>
					<p>Dates are YYYY-MM-DD or ages like 7d, 2w, 3m, 1y</p>
				</div>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
</form>

<script lang="ts">
	import './layout.css';
	import { ModeWatcher } from 'mode-watcher';
	import { Toaster } from '#lib/components/ui/sonner/index.js';
	import Login from '#lib/components/mail/login.svelte';
	import { live, session } from '#lib/mail/session.svelte.js';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	session.refresh();

	// Realtime thread updates while signed in.
	$effect(() => {
		if (!session.user) return;
		const pending = live.subscribe();
		return () => {
			pending.then((unsubscribe) => unsubscribe()).catch(() => {});
		};
	});
</script>

<svelte:head>
	<title>Roost</title>
</svelte:head>

<ModeWatcher />
<Toaster />

{#if session.user}
	{@render children()}
{:else}
	<Login />
{/if}

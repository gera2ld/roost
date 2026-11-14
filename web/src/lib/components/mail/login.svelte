<script lang="ts">
	import { ClientResponseError } from 'pocketbase';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Label } from '#lib/components/ui/label/index.js';
	import { Separator } from '#lib/components/ui/separator/index.js';
	import { session } from '#lib/mail/session.svelte.js';

	type Provider = { name: string; displayName: string };

	let email = $state('');
	let password = $state('');
	let error = $state('');
	let busy = $state(false);
	let loaded = $state(false);
	// If the server can't say which methods are on, offer the password form.
	let passwordEnabled = $state(true);
	let providers = $state<Provider[]>([]);

	$effect(() => {
		session
			.methods()
			.then((methods) => {
				passwordEnabled = methods.password.enabled;
				providers = methods.oauth2.enabled ? methods.oauth2.providers : [];
			})
			.catch(() => {})
			.finally(() => (loaded = true));
	});

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		busy = true;
		error = '';
		try {
			await session.signIn(email, password);
		} catch {
			error = 'Wrong email or password.';
		} finally {
			busy = false;
		}
	}

	async function signInWith(provider: Provider) {
		busy = true;
		error = '';
		try {
			await session.signInWith(provider.name);
		} catch (err) {
			if (err instanceof ClientResponseError && err.isAbort) return;
			error =
				err instanceof ClientResponseError && err.status === 403
					? err.message
					: `Couldn't sign in with ${provider.displayName}.`;
		} finally {
			busy = false;
		}
	}
</script>

<main class="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-6 px-4">
	<h1 class="text-2xl font-semibold">Roost</h1>
	{#if !loaded}
		<!-- Waiting for the sign-in methods, so a turned-off password form never flashes. -->
	{:else if providers.length > 0}
		<div class="flex flex-col gap-2">
			{#each providers as provider (provider.name)}
				<Button variant="outline" disabled={busy} onclick={() => signInWith(provider)}>
					Continue with {provider.displayName}
				</Button>
			{/each}
		</div>
		{#if passwordEnabled}
			<div class="flex items-center gap-3 text-xs text-muted-foreground">
				<Separator class="flex-1" />or<Separator class="flex-1" />
			</div>
		{/if}
	{/if}
	{#if loaded && passwordEnabled}
		<form class="flex flex-col gap-4" onsubmit={submit}>
			<div class="flex flex-col gap-2">
				<Label for="email">Email</Label>
				<Input id="email" type="email" autocomplete="username" required bind:value={email} />
			</div>
			<div class="flex flex-col gap-2">
				<Label for="password">Password</Label>
				<Input
					id="password"
					type="password"
					autocomplete="current-password"
					required
					bind:value={password}
				/>
			</div>
			<Button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</Button>
		</form>
	{/if}
	{#if error}
		<p class="text-sm text-destructive" role="alert">{error}</p>
	{/if}
</main>

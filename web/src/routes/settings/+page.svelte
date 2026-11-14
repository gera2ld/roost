<script lang="ts">
	import { toast } from 'svelte-sonner';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Label } from '#lib/components/ui/label/index.js';
	import { Skeleton } from '#lib/components/ui/skeleton/index.js';
	import { Switch } from '#lib/components/ui/switch/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { errorMessage } from '#lib/mail/errors.js';
	import {
		addDomain,
		deleteIdentity,
		listDomains,
		listIdentities,
		saveIdentity,
		setDomainSending,
		type Domain,
	} from '#lib/pb.js';

	type Draft = { id: string; address: string; display_name: string; signature: string };

	let loaded = $state(false);
	let domains = $state<Domain[]>([]);
	let identities = $state<Draft[]>([]);
	let saved = $state<Record<string, string>>({}); // id → JSON as stored, to show unsaved edits
	let newDomain = $state('');
	let newLocal = $state('');
	let newDomainOf = $state('');
	let busy = $state('');

	const snapshot = (i: Draft) => JSON.stringify([i.display_name, i.signature]);

	async function load() {
		try {
			const [doms, ids] = await Promise.all([listDomains(), listIdentities()]);
			domains = doms;
			identities = ids.map((i) => ({
				id: i.id,
				address: i.address,
				display_name: i.display_name,
				signature: i.signature,
			}));
			saved = Object.fromEntries(identities.map((i) => [i.id, snapshot(i)]));
			newDomainOf ||= doms.find((d) => d.send_enabled)?.domain ?? doms[0]?.domain ?? '';
			loaded = true;
		} catch (err) {
			toast.error(errorMessage(err, 'Could not load settings'));
		}
	}
	load();

	async function toggle(d: Domain, on: boolean) {
		busy = d.id;
		try {
			const up = await setDomainSending(d.id, on);
			domains = domains.map((x) => (x.id === up.id ? up : x));
		} catch (err) {
			toast.error(errorMessage(err, 'Could not change sending'));
		} finally {
			busy = '';
		}
	}

	async function add(e: SubmitEvent) {
		e.preventDefault();
		busy = 'domain';
		try {
			const d = await addDomain(newDomain);
			if (!domains.some((x) => x.id === d.id))
				domains = [...domains, d].toSorted((a, b) => a.domain.localeCompare(b.domain));
			newDomain = '';
			newDomainOf ||= d.domain;
		} catch (err) {
			toast.error(errorMessage(err, 'Could not add the domain'));
		} finally {
			busy = '';
		}
	}

	async function save(i: Draft) {
		busy = i.id || 'new';
		try {
			const rec = await saveIdentity(i);
			const next = {
				id: rec.id,
				address: rec.address,
				display_name: rec.display_name,
				signature: rec.signature,
			};
			const rest = identities.filter((x) => x.id !== rec.id && x.address !== rec.address);
			identities = [...rest, next].toSorted((a, b) => a.address.localeCompare(b.address));
			saved = { ...saved, [rec.id]: snapshot(next) };
			toast.success(`Saved ${rec.address}`);
			return true;
		} catch (err) {
			toast.error(errorMessage(err, 'Could not save the identity'));
			return false;
		} finally {
			busy = '';
		}
	}

	async function remove(i: Draft) {
		busy = i.id;
		try {
			await deleteIdentity(i.id);
			identities = identities.filter((x) => x.id !== i.id);
		} catch (err) {
			toast.error(errorMessage(err, 'Could not delete the identity'));
		} finally {
			busy = '';
		}
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		const address = `${newLocal.trim()}@${newDomainOf}`.toLowerCase();
		const existing = identities.find((i) => i.address === address);
		if (existing) {
			toast.info(`${address} already has an identity below`);
			return;
		}
		if (await save({ id: '', address, display_name: '', signature: '' })) newLocal = '';
	}

	const sectionClass = 'flex flex-col gap-3 border-b px-4 py-6';
	const hintClass = 'text-sm text-muted-foreground';
</script>

<svelte:head><title>Settings · Roost</title></svelte:head>

<div class="mx-auto flex min-h-dvh max-w-3xl flex-col md:border-x">
	<header class="flex items-center gap-1 border-b p-2">
		<Button variant="ghost" size="icon" aria-label="Back to mail" href="/inbox">
			<ArrowLeftIcon />
		</Button>
		<h1 class="text-base font-semibold">Settings</h1>
	</header>

	{#if !loaded}
		<div class="flex flex-col gap-3 p-4">
			<Skeleton class="h-8 w-full" />
			<Skeleton class="h-8 w-full" />
			<Skeleton class="h-32 w-full" />
		</div>
	{:else}
		<section class={sectionClass} aria-labelledby="domains-heading">
			<div>
				<h2 id="domains-heading" class="font-medium">Domains</h2>
				<p class={hintClass}>
					Mail for a domain adds it here. Turn sending on only for domains verified in Resend;
					receive-only domains never appear in From.
				</p>
			</div>
			<ul class="flex flex-col divide-y rounded-md border">
				{#each domains as d (d.id)}
					<li class="flex items-center gap-3 px-3 py-2">
						<span class="min-w-0 flex-1 truncate">{d.domain}</span>
						<Label for="send-{d.id}" class="text-sm text-muted-foreground">
							{d.send_enabled ? 'Sends' : 'Receive only'}
						</Label>
						<Switch
							id="send-{d.id}"
							checked={d.send_enabled}
							disabled={busy === d.id}
							onCheckedChange={(on) => toggle(d, on)}
						/>
					</li>
				{:else}
					<li class="px-3 py-2 {hintClass}">No domains yet.</li>
				{/each}
			</ul>
			<form class="flex gap-2" onsubmit={add}>
				<Input
					class="flex-1"
					placeholder="example.com"
					aria-label="New domain"
					autocomplete="off"
					bind:value={newDomain}
				/>
				<Button type="submit" variant="outline" disabled={!newDomain.trim() || busy === 'domain'}>
					Add domain
				</Button>
			</form>
		</section>

		<section class={sectionClass} aria-labelledby="identities-heading">
			<div>
				<h2 id="identities-heading" class="font-medium">Identities</h2>
				<p class={hintClass}>
					The name and signature used when you send from an address. New messages start from the
					first identity on a domain that sends.
				</p>
			</div>

			{#each identities as i (i.id)}
				<form
					class="flex flex-col gap-2 rounded-md border p-3"
					onsubmit={(e) => {
						e.preventDefault();
						save(i);
					}}
				>
					<div class="flex items-center gap-2">
						<span class="min-w-0 flex-1 truncate font-medium">{i.address}</span>
						<Button
							variant="ghost"
							size="icon"
							aria-label="Delete identity {i.address}"
							disabled={busy === i.id}
							onclick={() => remove(i)}
						>
							<TrashIcon />
						</Button>
					</div>
					<Label for="name-{i.id}">Display name</Label>
					<Input id="name-{i.id}" placeholder="Your name" bind:value={i.display_name} />
					<Label for="sig-{i.id}">Signature</Label>
					<Textarea
						id="sig-{i.id}"
						rows={4}
						placeholder="Added below your message, after a “-- ” line"
						bind:value={i.signature}
					/>
					<div class="flex justify-end">
						<Button type="submit" disabled={busy === i.id || saved[i.id] === snapshot(i)}>
							Save
						</Button>
					</div>
				</form>
			{/each}

			{#if domains.length}
				<form class="flex flex-wrap items-center gap-2" onsubmit={create}>
					<div class="flex min-w-0 flex-1 items-center">
						<Input
							class="min-w-16 flex-1 text-right"
							placeholder="you"
							aria-label="New identity address"
							autocomplete="off"
							bind:value={newLocal}
						/>
						<span class="px-1 text-muted-foreground">@</span>
						<select
							class="h-9 max-w-48 truncate rounded-md border bg-transparent px-2 text-sm"
							aria-label="New identity domain"
							bind:value={newDomainOf}
						>
							{#each domains as d (d.id)}<option value={d.domain}>{d.domain}</option>{/each}
						</select>
					</div>
					<Button type="submit" variant="outline" disabled={!newLocal.trim() || busy === 'new'}>
						Add identity
					</Button>
				</form>
			{/if}
		</section>
	{/if}
</div>

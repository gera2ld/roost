<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import XIcon from '@lucide/svelte/icons/x';
	import SendIcon from '@lucide/svelte/icons/send';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import PaperclipIcon from '@lucide/svelte/icons/paperclip';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Skeleton } from '#lib/components/ui/skeleton/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { formatList, splitAddress } from '#lib/mail/address.js';
	import { errorMessage } from '#lib/mail/errors.js';
	import { live } from '#lib/mail/session.svelte.js';
	import { swapSignature } from '#lib/mail/signature.js';
	import { fileSize } from '#lib/mail/text.js';
	import {
		deleteDraft,
		listDomains,
		listIdentities,
		listMailboxes,
		pb,
		sendDraft,
		updateDraft,
		type Attachment,
		type DraftPatch,
		type Identity,
		type Message,
	} from '#lib/pb.js';

	const id = $derived(page.params.draft ?? '');

	let loaded = $state(false);
	let missing = $state(false);
	let status = $state<'saved' | 'saving' | 'error' | ''>('');
	let sending = $state(false);
	let failure = $state('');

	let fromName = $state('');
	let fromLocal = $state('');
	let fromDomain = $state('');
	let to = $state('');
	let cc = $state('');
	let bcc = $state('');
	let subject = $state('');
	let body = $state('');
	let forwardIds = $state<string[]>([]);
	let showCc = $state(false);
	let showBcc = $state(false);

	let domains = $state<string[]>([]);
	let identities = $state<Identity[]>([]);
	let receiveOnly = $state(''); // the draft's domain, when it cannot send
	let attachments = $state<Attachment[]>([]);

	const fromAddress = $derived(
		fromLocal && fromDomain ? `${fromLocal}@${fromDomain}`.toLowerCase() : '',
	);
	const identityOf = (address: string) => identities.find((i) => i.address === address);

	// A new From brings its own signature and display name, unless they were edited.
	let prevFrom = '';
	$effect(() => {
		const now = fromAddress;
		if (!loaded || now === prevFrom) return;
		const old = identityOf(prevFrom);
		const next = identityOf(now);
		prevFrom = now;
		body = swapSignature(body, old?.signature ?? '', next?.signature ?? '');
		if (next?.display_name) {
			if (!fromName || !old?.display_name || fromName === old.display_name)
				fromName = next.display_name;
		} else if (old?.display_name && fromName === old.display_name) {
			fromName = '';
		}
	});

	const patch = (): DraftPatch => ({
		from_name: fromName,
		from_address: fromLocal && fromDomain ? `${fromLocal}@${fromDomain}` : '',
		to,
		cc,
		bcc,
		subject,
		text_body: body,
		forward_attachments: forwardIds,
	});
	let lastSaved = '';

	$effect(() => {
		load(id);
	});

	async function load(draftId: string) {
		loaded = false;
		try {
			const [d, doms, ids, boxes] = await Promise.all([
				pb.collection('messages').getOne<Message>(draftId),
				listDomains(),
				listIdentities(),
				listMailboxes(),
			]);
			if (d.direction !== 'outbound' || !['draft', 'failed'].includes(d.send_status)) {
				// Already sent (e.g. from another tab): show it where it lives.
				await goto(d.thread ? `/sent/${d.thread}` : '/drafts', { replaceState: true });
				return;
			}
			domains = doms.filter((x) => x.send_enabled).map((x) => x.domain);
			identities = ids;
			const from = d.from_address || boxes.find((b) => domains.includes(b.domain))?.address || '';
			({ local: fromLocal, domain: fromDomain } = splitAddress(from));
			receiveOnly = fromDomain && !domains.includes(fromDomain) ? fromDomain : '';
			// Fall back to a domain that sends; with none, keep the address as it was.
			if (!domains.includes(fromDomain) && domains.length) fromDomain = domains[0];
			fromName = d.from_name;
			to = formatList(d.to);
			cc = formatList(d.cc);
			bcc = formatList(d.bcc);
			showCc = Boolean(cc);
			showBcc = Boolean(bcc);
			subject = d.subject;
			body = d.text_body;
			forwardIds = d.forward_attachments ?? [];
			failure = d.send_status === 'failed' ? d.send_error : '';
			attachments = forwardIds.length
				? await pb.collection('attachments').getFullList<Attachment>({
						filter: forwardIds.map((a) => pb.filter('id = {:a}', { a })).join(' || '),
					})
				: [];
			// A From filled in here (new draft) is an edit: let autosave store it.
			lastSaved = d.from_address && !receiveOnly ? JSON.stringify(patch()) : '';
			prevFrom = fromAddress;
			loaded = true;
		} catch {
			missing = true;
		}
	}

	// Autosave: debounce edits; saves run one after another on a promise chain.
	let timer: ReturnType<typeof setTimeout> | undefined;
	let chain: Promise<void> = Promise.resolve();
	$effect(() => {
		const snapshot = JSON.stringify(patch());
		if (!loaded || snapshot === lastSaved) return;
		status = '';
		clearTimeout(timer);
		timer = setTimeout(save, 700);
	});

	function save(): Promise<void> {
		clearTimeout(timer);
		chain = chain.then(saveNow);
		return chain;
	}

	async function saveNow() {
		const current = patch();
		const snapshot = JSON.stringify(current);
		if (!loaded || snapshot === lastSaved) return;
		status = 'saving';
		try {
			await updateDraft(id, current);
			lastSaved = snapshot;
			status = 'saved';
		} catch {
			status = 'error';
		}
	}

	onDestroy(() => {
		if (loaded && JSON.stringify(patch()) !== lastSaved) void save();
	});

	async function send() {
		sending = true;
		try {
			await save();
			if (status === 'error') throw new Error('Could not save the draft');
			const sent = await sendDraft(id);
			loaded = false; // nothing left to autosave
			toast.success('Sent');
			live.bump();
			await goto(sent.thread ? `/sent/${sent.thread}` : '/sent');
		} catch (err) {
			failure = errorMessage(err, 'Sending failed');
			toast.error(failure);
		} finally {
			sending = false;
		}
	}

	async function discard() {
		loaded = false;
		try {
			await deleteDraft(id);
			toast.success('Draft discarded');
			await close();
		} catch (err) {
			loaded = true;
			toast.error(errorMessage(err, 'Could not discard'));
		}
	}

	async function close() {
		if (history.length > 1) history.back();
		else await goto('/drafts');
	}

	function removeAttachment(attId: string) {
		forwardIds = forwardIds.filter((x) => x !== attId);
		attachments = attachments.filter((a) => a.id !== attId);
	}

	const rowClass = 'flex items-center gap-2 border-b px-3 md:px-4';
	const labelClass = 'w-14 shrink-0 text-sm text-muted-foreground';
	const fieldClass =
		'h-10 border-0 bg-transparent px-0 shadow-none focus-visible:ring-0 dark:bg-transparent';
</script>

<svelte:head><title>{subject || 'New message'} · Roost</title></svelte:head>

<div class="mx-auto flex h-dvh max-w-3xl flex-col md:border-x">
	<header class="flex items-center gap-1 border-b p-2">
		<Button variant="ghost" size="icon" aria-label="Close" onclick={close}><XIcon /></Button>
		<span class="text-sm text-muted-foreground" aria-live="polite">
			{status === 'saving'
				? 'Saving…'
				: status === 'saved'
					? 'Saved'
					: status === 'error'
						? 'Not saved'
						: ''}
		</span>
		<div class="ml-auto flex items-center gap-1">
			<Button
				variant="ghost"
				size="icon"
				aria-label="Discard draft"
				onclick={discard}
				disabled={!loaded || sending}
			>
				<TrashIcon />
			</Button>
			<Button onclick={send} disabled={!loaded || sending}>
				<SendIcon />{sending ? 'Sending…' : 'Send'}
			</Button>
		</div>
	</header>

	{#if missing}
		<p class="p-6 text-sm text-muted-foreground">This draft is gone.</p>
	{:else if !loaded}
		<div class="flex flex-col gap-3 p-4">
			<Skeleton class="h-8 w-full" />
			<Skeleton class="h-8 w-full" />
			<Skeleton class="h-40 w-full" />
		</div>
	{:else}
		{#if failure}
			<p class="border-b bg-destructive/10 px-4 py-2 text-sm text-destructive" role="alert">
				Not sent: {failure}
			</p>
		{/if}
		<div class={rowClass}>
			<label for="from-local" class={labelClass}>From</label>
			<div class="flex min-w-0 flex-1 flex-wrap items-center gap-x-2">
				<Input
					class="{fieldClass} w-32 grow-0"
					placeholder="Your name"
					aria-label="Sender name"
					bind:value={fromName}
				/>
				<div class="flex min-w-0 flex-1 items-center">
					<Input
						id="from-local"
						class="{fieldClass} min-w-16 flex-1 text-right"
						placeholder="you"
						bind:value={fromLocal}
						autocomplete="off"
					/>
					<span class="px-0.5 text-muted-foreground">@</span>
					<select
						class="h-10 max-w-48 truncate bg-transparent text-sm outline-none"
						aria-label="Sending domain"
						bind:value={fromDomain}
					>
						{#each domains as d (d)}<option value={d}>{d}</option>{/each}
					</select>
				</div>
			</div>
		</div>
		{#if !domains.length}
			<p class="border-b px-3 py-2 text-sm text-muted-foreground md:px-4">
				None of your domains can send yet. Turn sending on in
				<a href="/settings" class="underline">Settings</a>.
			</p>
		{:else if receiveOnly}
			<p class="border-b px-3 py-2 text-sm text-muted-foreground md:px-4">
				@{receiveOnly} is receive-only, so this sends from another domain. Change it in
				<a href="/settings" class="underline">Settings</a>.
			</p>
		{/if}
		<div class={rowClass}>
			<label for="to" class={labelClass}>To</label>
			<Input
				id="to"
				class={fieldClass}
				type="text"
				inputmode="email"
				autocomplete="off"
				bind:value={to}
			/>
			{#if !showCc}<button
					type="button"
					class="text-sm text-muted-foreground"
					onclick={() => (showCc = true)}>Cc</button
				>{/if}
			{#if !showBcc}<button
					type="button"
					class="text-sm text-muted-foreground"
					onclick={() => (showBcc = true)}>Bcc</button
				>{/if}
		</div>
		{#if showCc}
			<div class={rowClass}>
				<label for="cc" class={labelClass}>Cc</label>
				<Input
					id="cc"
					class={fieldClass}
					type="text"
					inputmode="email"
					autocomplete="off"
					bind:value={cc}
				/>
			</div>
		{/if}
		{#if showBcc}
			<div class={rowClass}>
				<label for="bcc" class={labelClass}>Bcc</label>
				<Input
					id="bcc"
					class={fieldClass}
					type="text"
					inputmode="email"
					autocomplete="off"
					bind:value={bcc}
				/>
			</div>
		{/if}
		<div class={rowClass}>
			<label for="subject" class={labelClass}>Subject</label>
			<Input id="subject" class={fieldClass} bind:value={subject} />
		</div>
		<Textarea
			class="flex-1 resize-none rounded-none border-0 p-3 shadow-none focus-visible:ring-0 md:p-4 dark:bg-transparent"
			aria-label="Message"
			bind:value={body}
		/>
		{#if attachments.length}
			<ul class="flex flex-wrap gap-2 border-t p-3" aria-label="Forwarded attachments">
				{#each attachments as a (a.id)}
					<li class="flex items-center gap-1 rounded-md border px-2 py-1 text-sm">
						<PaperclipIcon class="size-3.5 text-muted-foreground" />
						<span class="max-w-48 truncate">{a.filename || 'attachment'}</span>
						<span class="text-muted-foreground">{fileSize(a.size)}</span>
						<button
							type="button"
							class="ml-1 text-muted-foreground"
							aria-label="Remove {a.filename}"
							onclick={() => removeAttachment(a.id)}
						>
							<XIcon class="size-3.5" />
						</button>
					</li>
				{/each}
			</ul>
		{/if}
	{/if}
</div>

<script lang="ts">
	import { toast } from 'svelte-sonner';
	import StarIcon from '@lucide/svelte/icons/star';
	import PaperclipIcon from '@lucide/svelte/icons/paperclip';
	import ReplyIcon from '@lucide/svelte/icons/reply';
	import ReplyAllIcon from '@lucide/svelte/icons/reply-all';
	import ForwardIcon from '@lucide/svelte/icons/forward';
	import { startDraft } from '#lib/mail/compose.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import { fileSize, linkify, longDate, personLabel } from '#lib/mail/text.js';
	import {
		downloadAttachment,
		messageAction,
		type Address,
		type Attachment,
		type Message,
	} from '#lib/pb.js';
	import { cn } from '#lib/utils.js';

	let { message, attachments }: { message: Message; attachments: Attachment[] } = $props();

	let starred = $state(false);
	$effect(() => {
		starred = message.is_starred;
	});

	const delivery: Record<string, { label: string; bad?: boolean }> = {
		sent: { label: 'Sent' },
		delivered: { label: 'Delivered' },
		delayed: { label: 'Delayed' },
		bounced: { label: 'Bounced', bad: true },
		complained: { label: 'Marked as spam', bad: true },
	};
	const hasOthers = $derived((message.to?.length ?? 0) + (message.cc?.length ?? 0) > 1);

	const segments = $derived(linkify(message.text_body ?? ''));
	const list = (a: Address[] | null) => (a ?? []).map(personLabel).join(', ');

	async function toggleStar() {
		const next = !starred;
		starred = next;
		try {
			await messageAction(message.id, next ? 'star' : 'unstar');
		} catch {
			starred = !next;
			toast.error('Could not update star');
		}
	}

	async function download(a: Attachment) {
		try {
			await downloadAttachment(a);
		} catch (err) {
			toast.error(err instanceof Error ? err.message : 'Download failed');
		}
	}
</script>

<section class="rounded-lg border p-4">
	<header class="mb-3 flex items-start gap-3">
		<div class="min-w-0 flex-1 text-sm">
			<div class="flex flex-wrap items-baseline gap-x-2">
				<span class="font-medium">{message.from_name || message.from_address}</span>
				{#if message.from_name}
					<span class="text-muted-foreground">&lt;{message.from_address}&gt;</span>
				{/if}
				{#if message.direction === 'outbound'}
					{@const d = delivery[message.delivery_status || 'sent']}
					<Badge
						variant={d.bad ? 'destructive' : 'secondary'}
						title={message.send_error || undefined}
					>
						{d.label}
					</Badge>
				{/if}
			</div>
			<div class="truncate text-muted-foreground">
				to {list(message.to) || message.mailbox}{#if message.cc?.length}, cc {list(message.cc)}{/if}
			</div>
		</div>
		<time class="shrink-0 text-xs text-muted-foreground" datetime={message.received_at}>
			{longDate(message.received_at)}
		</time>
		<Button
			variant="ghost"
			size="icon-sm"
			aria-label={starred ? 'Unstar' : 'Star'}
			aria-pressed={starred}
			onclick={toggleStar}
		>
			<StarIcon class={cn(starred && 'fill-amber-400 text-amber-400')} />
		</Button>
	</header>

	<!-- Untrusted mail: plain text nodes only; links are http(s) and never send a referrer. -->
	<div class="text-sm leading-relaxed break-words whitespace-pre-wrap">
		{#each segments as s, i (i)}{#if s.href}<a
					href={s.href}
					target="_blank"
					rel="noopener noreferrer nofollow"
					class="text-primary underline underline-offset-2">{s.text}</a
				>{:else}{s.text}{/if}{/each}
	</div>

	{#if message.delivery_status === 'bounced' || message.delivery_status === 'complained'}
		{#if message.send_error}<p class="mt-3 text-xs text-destructive">{message.send_error}</p>{/if}
	{/if}

	{#if message.body_source === 'html'}
		<p class="mt-3 text-xs text-muted-foreground">
			Converted from HTML; images and styling are not shown.
		</p>
	{/if}

	{#if attachments.length}
		<ul class="mt-4 flex flex-wrap gap-2">
			{#each attachments as a (a.id)}
				<li>
					<Button variant="outline" size="sm" onclick={() => download(a)}>
						<PaperclipIcon />
						<span class="max-w-48 truncate">{a.filename || 'attachment'}</span>
						<span class="text-muted-foreground">{fileSize(a.size)}</span>
					</Button>
				</li>
			{/each}
		</ul>
	{/if}

	<footer class="mt-4 flex flex-wrap gap-2">
		<Button variant="outline" size="sm" onclick={() => startDraft('reply', message.id)}>
			<ReplyIcon /> Reply
		</Button>
		{#if hasOthers}
			<Button variant="outline" size="sm" onclick={() => startDraft('reply_all', message.id)}>
				<ReplyAllIcon /> Reply all
			</Button>
		{/if}
		<Button variant="outline" size="sm" onclick={() => startDraft('forward', message.id)}>
			<ForwardIcon /> Forward
		</Button>
	</footer>
</section>

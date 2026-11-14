import { alert, spoolFailureText, type AlertConfig } from './alert';
import { nudge, type NudgeConfig } from './nudge';
import { META_ENVELOPE_FROM, META_ENVELOPE_TO, spoolKey } from './spool';

export interface RoostEnv extends NudgeConfig, AlertConfig {
	SPOOL: Pick<R2Bucket, 'put'>;
}

export interface Deps {
	now?: () => Date;
	uuid?: () => string;
	fetch?: typeof fetch;
	sleep?: (ms: number) => Promise<void>;
}

/** Waits between R2 write attempts; three tries in all. */
export const PUT_RETRY_DELAYS_MS = [250, 1000];

/**
 * Accept one inbound message.
 *
 * 1. Store the raw bytes in R2 with the envelope as metadata, retrying briefly.
 *    This is the only step that must succeed. If it keeps failing, alert the
 *    owner on Telegram and throw, so Email Routing does not accept the message
 *    silently.
 * 2. Nudge the backend in the background; failures are ignored (the sweep covers them).
 */
export async function handleEmail(
	message: ForwardableEmailMessage,
	env: RoostEnv,
	ctx: Pick<ExecutionContext, 'waitUntil'>,
	deps: Deps = {},
): Promise<string> {
	const now = deps.now?.() ?? new Date();
	const key = spoolKey(now, deps.uuid?.() ?? crypto.randomUUID());
	const sleep = deps.sleep ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));

	try {
		const raw = await new Response(message.raw).arrayBuffer();
		for (let attempt = 0; ; attempt++) {
			try {
				await env.SPOOL.put(key, raw, {
					httpMetadata: { contentType: 'message/rfc822' },
					customMetadata: { [META_ENVELOPE_FROM]: message.from, [META_ENVELOPE_TO]: message.to },
				});
				break;
			} catch (err) {
				if (attempt >= PUT_RETRY_DELAYS_MS.length) throw err;
				await sleep(PUT_RETRY_DELAYS_MS[attempt]);
			}
		}
	} catch (err) {
		console.error(`spool write failed for ${message.to}`, err);
		await alert(env, spoolFailureText(message.from, message.to, message.rawSize, err), deps.fetch);
		throw err;
	}

	ctx.waitUntil(nudge(env, key, deps.fetch));
	return key;
}

// Best-effort nudge to the backend. Signing must match server/internal/ingest/nudge.go:
//   X-Roost-Timestamp: <unix seconds>
//   X-Roost-Signature: hex(HMAC-SHA256(secret, "<timestamp>.<raw body>"))

export interface NudgeConfig {
	NUDGE_URL?: string;
	NUDGE_SECRET?: string;
	/** Cloudflare Access service token, when the route sits behind Access. */
	ACCESS_CLIENT_ID?: string;
	ACCESS_CLIENT_SECRET?: string;
}

export const NUDGE_TIMEOUT_MS = 5000;

export async function sign(secret: string, timestamp: string, body: string): Promise<string> {
	const enc = new TextEncoder();
	const key = await crypto.subtle.importKey(
		'raw',
		enc.encode(secret),
		{ name: 'HMAC', hash: 'SHA-256' },
		false,
		['sign'],
	);
	const mac = await crypto.subtle.sign('HMAC', key, enc.encode(`${timestamp}.${body}`));
	return [...new Uint8Array(mac)].map((b) => b.toString(16).padStart(2, '0')).join('');
}

/** Tells the backend a key is ready. Never throws: the sweep covers a lost nudge. */
export async function nudge(
	config: NudgeConfig,
	key: string,
	fetchImpl: typeof fetch = fetch,
	now: number = Date.now(),
): Promise<void> {
	if (!config.NUDGE_URL || !config.NUDGE_SECRET) return;
	try {
		const body = JSON.stringify({ key });
		const timestamp = String(Math.floor(now / 1000));
		const headers: Record<string, string> = {
			'Content-Type': 'application/json',
			'X-Roost-Timestamp': timestamp,
			'X-Roost-Signature': await sign(config.NUDGE_SECRET, timestamp, body),
		};
		if (config.ACCESS_CLIENT_ID && config.ACCESS_CLIENT_SECRET) {
			headers['CF-Access-Client-Id'] = config.ACCESS_CLIENT_ID;
			headers['CF-Access-Client-Secret'] = config.ACCESS_CLIENT_SECRET;
		}
		const res = await fetchImpl(config.NUDGE_URL, {
			method: 'POST',
			headers,
			body,
			signal: AbortSignal.timeout(NUDGE_TIMEOUT_MS),
		});
		if (!res.ok) console.warn(`nudge for ${key} got HTTP ${res.status}`);
	} catch (err) {
		console.warn(`nudge for ${key} failed`, err);
	}
}

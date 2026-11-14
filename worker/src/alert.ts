// Telegram alert for mail the Worker could not spool. Same bot and chat as the
// backend's alerts (ROOST_TELEGRAM_* there), sent directly because the backend
// may be the thing that is down. Never throws.

export interface AlertConfig {
	TELEGRAM_BOT_TOKEN?: string;
	TELEGRAM_CHAT_ID?: string;
}

export const ALERT_TIMEOUT_MS = 5000;

const escapeHtml = (s: string) =>
	s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');

export function spoolFailureText(from: string, to: string, size: number, err: unknown): string {
	const reason = (err instanceof Error ? err.message : String(err)).slice(0, 300);
	return [
		'⚠️ <b>Inbound mail was not stored</b>',
		`from ${escapeHtml(from)}`,
		`to ${escapeHtml(to)} (${Math.ceil(size / 1024)} KiB)`,
		escapeHtml(reason),
		'Roost has no copy. If the sender does not retry, ask them to resend.',
	].join('\n');
}

export async function alert(
	config: AlertConfig,
	text: string,
	fetchImpl: typeof fetch = fetch,
): Promise<void> {
	if (!config.TELEGRAM_BOT_TOKEN || !config.TELEGRAM_CHAT_ID) return;
	try {
		const res = await fetchImpl(
			`https://api.telegram.org/bot${config.TELEGRAM_BOT_TOKEN}/sendMessage`,
			{
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					chat_id: config.TELEGRAM_CHAT_ID,
					text,
					parse_mode: 'HTML',
					link_preview_options: { is_disabled: true },
				}),
				signal: AbortSignal.timeout(ALERT_TIMEOUT_MS),
			},
		);
		if (!res.ok) console.error(`telegram alert failed: ${res.status}`);
	} catch {
		// The URL holds the bot token: log nothing from the error.
		console.error('telegram alert failed: network error');
	}
}

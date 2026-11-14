import { describe, expect, it, vi } from 'vitest';
import { handleEmail, type RoostEnv } from './handler';
import { nudge, sign } from './nudge';
import { spoolKey } from './spool';

const UUID = '00000000-0000-4000-8000-000000000001';
const NOW = new Date(Date.UTC(2026, 10, 13, 9, 30, 0));
const KEY = `2026/11/13/093000-${UUID}.eml`;
const RAW = 'From: a@example.com\r\nTo: me@roost.test\r\nSubject: hi\r\n\r\nbody\r\n';

function fakeMessage() {
	return {
		from: 'a@example.com',
		to: 'me@roost.test',
		raw: new Response(RAW).body!,
		rawSize: RAW.length,
		headers: new Headers(),
		setReject: vi.fn(),
		forward: vi.fn(),
		reply: vi.fn(),
	} as unknown as ForwardableEmailMessage;
}

function fakeEnv(overrides: Partial<RoostEnv> = {}) {
	const put = vi.fn(async () => null);
	return { env: { SPOOL: { put }, ...overrides } as RoostEnv, put };
}

function fakeCtx() {
	const pending: Promise<unknown>[] = [];
	return { ctx: { waitUntil: (p: Promise<unknown>) => void pending.push(p) }, pending };
}

const deps = (fetchImpl?: typeof fetch) => ({ now: () => NOW, uuid: () => UUID, fetch: fetchImpl });

describe('spoolKey', () => {
	it('is time-ordered UTC', () => {
		expect(spoolKey(NOW, UUID)).toBe(KEY);
		expect(spoolKey(new Date(Date.UTC(2026, 0, 2, 3, 4, 5)), UUID)).toBe(
			`2026/01/02/030405-${UUID}.eml`,
		);
	});
});

describe('handleEmail', () => {
	it('stores raw bytes with envelope metadata, then nudges', async () => {
		const { env, put } = fakeEnv({ NUDGE_URL: 'https://api.test/nudge', NUDGE_SECRET: 's' });
		const { ctx, pending } = fakeCtx();
		const fetchImpl = vi.fn(async () => new Response(null, { status: 202 }));

		const key = await handleEmail(fakeMessage(), env, ctx, deps(fetchImpl));
		await Promise.all(pending);

		expect(key).toBe(KEY);
		const [putKey, body, opts] = put.mock.calls[0] as unknown as [
			string,
			ArrayBuffer,
			R2PutOptions,
		];
		expect(putKey).toBe(KEY);
		expect(new TextDecoder().decode(body)).toBe(RAW);
		expect(opts.customMetadata).toEqual({
			'envelope-from': 'a@example.com',
			'envelope-to': 'me@roost.test',
		});
		expect(fetchImpl).toHaveBeenCalledOnce();
	});

	it('retries a failed R2 write', async () => {
		const { env, put } = fakeEnv();
		put.mockRejectedValueOnce(new Error('blip'));
		const sleep = vi.fn(async () => {});
		await expect(
			handleEmail(fakeMessage(), env, fakeCtx().ctx, { ...deps(), sleep }),
		).resolves.toBe(KEY);
		expect(put).toHaveBeenCalledTimes(2);
		expect(sleep).toHaveBeenCalledOnce();
	});

	it('alerts and throws when the R2 write keeps failing', async () => {
		const { env, put } = fakeEnv({
			TELEGRAM_BOT_TOKEN: '1:tok',
			TELEGRAM_CHAT_ID: '42',
			NUDGE_URL: 'https://api.test/nudge',
			NUDGE_SECRET: 's',
		});
		put.mockRejectedValue(new Error('r2 <down>'));
		const { ctx, pending } = fakeCtx();
		const fetchImpl = vi.fn(
			async (_url: RequestInfo | URL, _init?: RequestInit) => new Response('{}', { status: 200 }),
		);

		await expect(
			handleEmail(fakeMessage(), env, ctx, {
				...deps(fetchImpl as unknown as typeof fetch),
				sleep: async () => {},
			}),
		).rejects.toThrow('r2 <down>');
		expect(put).toHaveBeenCalledTimes(3);
		expect(pending).toHaveLength(0); // no nudge for a key that does not exist
		const [url, init] = fetchImpl.mock.calls[0];
		expect(url).toBe('https://api.telegram.org/bot1:tok/sendMessage');
		const body = JSON.parse(init!.body as string);
		expect(body.chat_id).toBe('42');
		expect(body.text).toContain('from a@example.com');
		expect(body.text).toContain('r2 &lt;down&gt;');
	});

	it('still throws when the alert itself fails', async () => {
		const { env, put } = fakeEnv({ TELEGRAM_BOT_TOKEN: '1:tok', TELEGRAM_CHAT_ID: '42' });
		put.mockRejectedValue(new Error('r2 down'));
		const fetchImpl = vi.fn(async () => {
			throw new Error('offline');
		});
		await expect(
			handleEmail(fakeMessage(), env, fakeCtx().ctx, { ...deps(fetchImpl), sleep: async () => {} }),
		).rejects.toThrow('r2 down');
	});

	it('accepts mail even when the nudge fails', async () => {
		const { env } = fakeEnv({ NUDGE_URL: 'https://api.test/nudge', NUDGE_SECRET: 's' });
		const { ctx, pending } = fakeCtx();
		const fetchImpl = vi.fn(async () => {
			throw new Error('backend down');
		});

		await expect(handleEmail(fakeMessage(), env, ctx, deps(fetchImpl))).resolves.toBe(KEY);
		await expect(Promise.all(pending)).resolves.toBeDefined();
	});
});

describe('nudge', () => {
	it('signs exactly like the Go server (shared test vector)', async () => {
		const body = JSON.stringify({ key: KEY });
		expect(await sign('test-secret', '1800000000', body)).toBe(
			'79c0fe375034cfcaa93edc4bae5bd7d95045bf058c206e1a3cc6a8091d402e91',
		);
	});

	it('sends signed headers and the Access service token', async () => {
		const fetchImpl = vi.fn(
			async (_url: RequestInfo | URL, _init?: RequestInit) => new Response(null, { status: 202 }),
		);
		await nudge(
			{
				NUDGE_URL: 'https://api.test/nudge',
				NUDGE_SECRET: 'test-secret',
				ACCESS_CLIENT_ID: 'id',
				ACCESS_CLIENT_SECRET: 'secret',
			},
			KEY,
			fetchImpl as unknown as typeof fetch,
			1_800_000_000_000,
		);
		const [url, init] = fetchImpl.mock.calls[0];
		const headers = init!.headers as Record<string, string>;
		expect(url).toBe('https://api.test/nudge');
		expect(init!.body).toBe(JSON.stringify({ key: KEY }));
		expect(headers['X-Roost-Timestamp']).toBe('1800000000');
		expect(headers['X-Roost-Signature']).toBe(
			'79c0fe375034cfcaa93edc4bae5bd7d95045bf058c206e1a3cc6a8091d402e91',
		);
		expect(headers['CF-Access-Client-Id']).toBe('id');
	});

	it('is a no-op without a URL or secret', async () => {
		const fetchImpl = vi.fn();
		await nudge({}, KEY, fetchImpl as unknown as typeof fetch);
		expect(fetchImpl).not.toHaveBeenCalled();
	});
});

import { describe, expect, it } from 'vitest';
import { draftFilter, folders } from './folders.js';

const filter = (raw: string, params: Record<string, unknown> = {}) =>
	raw.replace(/\{:(\w+)\}/g, (_, k) => JSON.stringify(params[k]));

describe('legacy route matcher', () => {
	it('accepts exactly the folders that are queries', async () => {
		const { folderIds } = await import('../../params.js');
		expect([...folderIds]).toEqual(folders.filter((f) => f.query).map((f) => f.id));
	});
});

describe('draftFilter', () => {
	it('lists unsent outbound mail, narrowed by sender', () => {
		expect(draftFilter('', filter)).toContain('send_status = "failed"');
		expect(draftFilter('Me@X.test', filter)).toContain('from_address = "me@x.test"');
		expect(draftFilter('@x.test', filter)).toContain('from_address ~ "%@x.test"');
	});
});

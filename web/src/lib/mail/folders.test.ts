import { describe, expect, it } from 'vitest';
import { draftFilter, folders, threadFilter } from './folders.js';

const filter = (raw: string, params: Record<string, unknown> = {}) =>
	raw.replace(/\{:(\w+)\}/g, (_, k) => JSON.stringify(params[k]));

describe('threadFilter', () => {
	it('returns the folder filter alone', () => {
		expect(threadFilter('trash', '', filter)).toBe('trashed_at != ""');
	});
	it('anchors a full address on both quotes', () => {
		expect(threadFilter('inbox', 'Me@X.test', filter)).toContain(`mailboxes ~ "\\"me@x.test\\""`);
	});
	it('anchors a domain on the closing quote', () => {
		expect(threadFilter('inbox', '@x.test', filter)).toContain(`mailboxes ~ "@x.test\\""`);
	});
	it('has nothing for drafts yet', () => {
		expect(threadFilter('drafts', '', filter)).toBeNull();
	});
});

describe('route matcher', () => {
	it('accepts exactly the sidebar folders', async () => {
		const { folderIds } = await import('../../params.js');
		expect([...folderIds]).toEqual(folders.map((f) => f.id));
	});
});

describe('draftFilter', () => {
	it('lists unsent outbound mail, narrowed by sender', () => {
		expect(draftFilter('', filter)).toContain('send_status = "failed"');
		expect(draftFilter('Me@X.test', filter)).toContain('from_address = "me@x.test"');
		expect(draftFilter('@x.test', filter)).toContain('from_address ~ "%@x.test"');
	});
});

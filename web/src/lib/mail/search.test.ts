import { describe, expect, it } from 'vitest';
import { isSearchField, queryScope, searchSegments, searchTree, withScope } from './search.js';

describe('searchTree', () => {
	it('sends the parsed tree', () => {
		expect(JSON.parse(searchTree('from:pat !is:read'))).toEqual([
			{ field: 'from', query: 'pat' },
			{ field: 'is', query: 'read', negate: true },
		]);
	});
	it('keeps unknown fields for the server to read as text', () => {
		expect(JSON.parse(searchTree('10:30'))).toEqual([{ field: '10', query: '30' }]);
	});
});

const kinds = (text: string) => searchSegments(text).map((s) => s.kind);

describe('searchSegments', () => {
	it('is lossless', () => {
		const text = 'from:(pat !sam) "a b" !(x y) https://x.test';
		expect(
			searchSegments(text)
				.map((s) => s.text)
				.join(''),
		).toBe(text);
	});
	it('colours known fields only, ignoring case', () => {
		expect(kinds('From:pat')).toEqual(['field', 'text', 'text']);
		expect(kinds('https://x')).not.toContain('field');
		expect(isSearchField('SUBJECT')).toBe(true);
	});
});

describe('queryScope', () => {
	it('reads the folder and mailbox a query requires', () => {
		expect(queryScope('in:inbox mailbox:Me@X.test invoice')).toEqual({
			folder: 'inbox',
			mailbox: 'me@x.test',
		});
		expect(queryScope('is:starred domain:@x.test')).toEqual({
			folder: 'starred',
			mailbox: '@x.test',
		});
		expect(queryScope('IS:Archived')).toEqual({ folder: 'archive', mailbox: null });
	});
	it('ignores negated, nested and unrelated terms', () => {
		expect(queryScope('!in:inbox')).toEqual({ folder: null, mailbox: null });
		expect(queryScope('{in:inbox in:sent} (mailbox:a@x.test b)')).toEqual({
			folder: null,
			mailbox: null,
		});
		expect(queryScope('is:unread invoice')).toEqual({ folder: null, mailbox: null });
		expect(queryScope('')).toEqual({ folder: null, mailbox: null });
	});
});

describe('withScope', () => {
	it('swaps the folder and keeps the rest', () => {
		expect(withScope('in:inbox invoice', { folder: 'sent' })).toBe('in:sent invoice');
		expect(withScope('is:archived from:pat', { folder: 'trash' })).toBe('in:trash from:pat');
		expect(withScope('invoice', { folder: 'starred' })).toBe('is:starred invoice');
	});
	it('swaps or removes the mailbox', () => {
		expect(withScope('in:inbox invoice', { mailbox: 'me@x.test' })).toBe(
			'in:inbox mailbox:me@x.test invoice',
		);
		expect(withScope('in:inbox mailbox:a@x.test', { mailbox: '@y.test' })).toBe(
			'in:inbox domain:y.test',
		);
		expect(withScope('in:inbox domain:y.test', { mailbox: null })).toBe('in:inbox');
	});
	it('leaves negated scope terms alone', () => {
		expect(withScope('!in:trash', { folder: 'inbox' })).toBe('in:inbox !in:trash');
	});
	it('round-trips with queryScope', () => {
		const q = withScope('', { folder: 'archive', mailbox: '@x.test' });
		expect(queryScope(q)).toEqual({ folder: 'archive', mailbox: '@x.test' });
	});
});

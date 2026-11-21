import { describe, expect, it } from 'vitest';
import { isSearchField, searchSegments, searchTree } from './search.js';

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

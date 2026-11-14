import { describe, expect, it } from 'vitest';
import { linkify, shortDate } from './text.js';

describe('linkify', () => {
	it('splits links out of text', () => {
		expect(linkify('see https://example.com/a?b=1, ok')).toEqual([
			{ text: 'see ' },
			{ text: 'https://example.com/a?b=1', href: 'https://example.com/a?b=1' },
			{ text: ', ok' },
		]);
	});
	it('ignores non-http schemes', () => {
		expect(linkify('javascript:alert(1) data:text/html,x')).toEqual([
			{ text: 'javascript:alert(1) data:text/html,x' },
		]);
	});
	it('does not swallow quotes or angle brackets', () => {
		const segs = linkify('<https://x.test/p>"');
		expect(segs.find((s) => s.href)?.href).toBe('https://x.test/p');
	});
	it('keeps balanced parens', () => {
		const segs = linkify('(https://en.wikipedia.org/wiki/Foo_(bar))');
		expect(segs.find((s) => s.href)?.text).toBe('https://en.wikipedia.org/wiki/Foo_(bar)');
	});
	it('returns plain text unchanged', () => {
		expect(linkify('hello')).toEqual([{ text: 'hello' }]);
	});
});

describe('shortDate', () => {
	it('is empty for garbage', () => {
		expect(shortDate('nope')).toBe('');
	});
	it('accepts PocketBase date strings', () => {
		const now = new Date('2026-11-14T12:00:00Z');
		expect(shortDate('2025-01-02 03:04:05.000Z', now)).not.toBe('');
	});
});

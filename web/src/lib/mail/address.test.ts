import { describe, expect, it } from 'vitest';
import { formatAddress, formatList, splitAddress } from './address.js';

describe('formatAddress', () => {
	it('quotes names that would split the field', () => {
		expect(formatAddress({ name: 'Doe, Jane', address: 'j@x.test' })).toBe(
			'"Doe, Jane" <j@x.test>',
		);
	});
	it('leaves plain names and bare addresses alone', () => {
		expect(formatList([{ name: 'Ann', address: 'a@x.test' }, { address: 'b@y.test' }])).toBe(
			'Ann <a@x.test>, b@y.test',
		);
	});
});

describe('splitAddress', () => {
	it('splits on the last @', () => {
		expect(splitAddress('me@roost.test')).toEqual({ local: 'me', domain: 'roost.test' });
		expect(splitAddress('nope')).toEqual({ local: 'nope', domain: '' });
	});
});

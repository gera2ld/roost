import { describe, expect, it } from 'vitest';
import { signatureBlock, swapSignature } from './signature.js';

const quote = 'On Fri, Nov 13, 2026 at 9:30 AM, Zhang <zhang@example.com> wrote:\n> Hi\n';

describe('signatureBlock', () => {
	it('matches the server block', () => {
		expect(signatureBlock('Me\nroost.test\n\n')).toBe('-- \nMe\nroost.test');
		expect(signatureBlock('  \n')).toBe('');
	});
});

describe('swapSignature', () => {
	it('replaces the old signature in place', () => {
		const body = 'Hello\n\n-- \nA\n\n' + quote;
		expect(swapSignature(body, 'A', 'B')).toBe('Hello\n\n-- \nB\n\n' + quote);
	});
	it('removes it when the new address has none', () => {
		expect(swapSignature('\n\n-- \nA\n\n' + quote, 'A', '')).toBe('\n\n' + quote);
		expect(swapSignature('\n\n-- \nA\n', 'A', '')).toBe('\n\n');
	});
	it('adds one above the quote when there was none', () => {
		expect(swapSignature('Hi\n\n' + quote, '', 'B')).toBe('Hi\n\n-- \nB\n\n' + quote);
		expect(swapSignature('Hi\n', '', 'B')).toBe('Hi\n\n-- \nB\n');
	});
	it('leaves a hand-edited signature alone', () => {
		const body = 'Hi\n\n-- \nA (edited)\n';
		expect(swapSignature(body, 'A', 'B')).toBe(body);
	});
});

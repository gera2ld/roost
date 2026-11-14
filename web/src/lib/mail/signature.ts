// Same block the server writes into new drafts (outbound.SignatureBlock): the
// RFC 3676 "-- " line, then the signature.
const delimiter = '-- \n';

/** The text a draft carries for a signature, or '' for none. */
export function signatureBlock(sig: string): string {
	const trimmed = sig.replace(/[ \n]+$/, '');
	return trimmed.trim() ? delimiter + trimmed : '';
}

// Where a new signature goes when the body has none: above the quote or the
// forwarded message, else at the end.
const quoteStart = /^(On .+ wrote:|---------- Forwarded message ----------)$/m;

/** Index of `block` as whole lines in body (starting a line, ending one), or -1. */
function findBlock(body: string, block: string): number {
	for (let i = body.indexOf(block); i >= 0; i = body.indexOf(block, i + 1)) {
		const end = i + block.length;
		if ((i === 0 || body[i - 1] === '\n') && (end === body.length || body[end] === '\n')) return i;
	}
	return -1;
}

/**
 * Replaces the signature for `oldSig` with the one for `newSig` (the From
 * address changed). A body whose signature was edited by hand keeps it, and a
 * new signature is only added when the body has none yet.
 */
export function swapSignature(body: string, oldSig: string, newSig: string): string {
	const from = signatureBlock(oldSig);
	const to = signatureBlock(newSig);
	if (from === to) return body;
	const at = from ? findBlock(body, from) : -1;
	if (at >= 0) {
		const before = body.slice(0, at);
		let after = body.slice(at + from.length);
		if (to) return before + to + after;
		// Drop the block and the blank line it brought with it.
		after = after.replace(/^\n\n?/, '');
		return before + after;
	}
	if (!to || body.includes(delimiter)) return body;
	const m = quoteStart.exec(body);
	if (m) return body.slice(0, m.index) + to + '\n\n' + body.slice(m.index);
	return body.replace(/\n*$/, '') + '\n\n' + to + '\n';
}

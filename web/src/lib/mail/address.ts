import type { Address } from '#lib/pb.js';

/** One address as typed in a recipient field; names with separators are quoted. */
export function formatAddress(a: Address): string {
	const name = a.name?.trim();
	if (!name) return a.address;
	const quoted = /[,;"<>@]/.test(name) ? `"${name.replaceAll('"', '')}"` : name;
	return `${quoted} <${a.address}>`;
}

export function formatList(list: Address[] | null | undefined): string {
	return (list ?? []).map(formatAddress).join(', ');
}

/** Splits an address into the local part and the domain. */
export function splitAddress(address: string): { local: string; domain: string } {
	const at = address.lastIndexOf('@');
	return at < 0
		? { local: address, domain: '' }
		: { local: address.slice(0, at), domain: address.slice(at + 1) };
}

/** The server's message for a failed request, else a generic fallback. */
export function errorMessage(err: unknown, fallback = 'Something went wrong'): string {
	if (err && typeof err === 'object' && 'response' in err) {
		const msg = (err as { response?: { message?: unknown } }).response?.message;
		if (typeof msg === 'string' && msg) return msg;
	}
	return err instanceof Error && err.message ? err.message : fallback;
}

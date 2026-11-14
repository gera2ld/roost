import type { AuthMethodsList, AuthRecord } from 'pocketbase';
import { pb } from '#lib/pb.js';

let user = $state<AuthRecord>(pb.authStore.isValid ? pb.authStore.record : null);
pb.authStore.onChange(() => {
	user = pb.authStore.isValid ? pb.authStore.record : null;
});

export const session = {
	get user() {
		return user;
	},
	async signIn(email: string, password: string) {
		await pb.collection('users').authWithPassword(email, password);
	},
	/** The sign-in methods the server has turned on (password, OAuth2 providers). */
	async methods(): Promise<AuthMethodsList> {
		return pb.collection('users').listAuthMethods();
	},
	/** Signs in through an OAuth2 provider in a popup; the account must already exist. */
	async signInWith(provider: string) {
		await pb.collection('users').authWithOAuth2({ provider });
	},
	signOut() {
		pb.realtime.unsubscribe();
		pb.authStore.clear();
	},
	/** Refreshes a stored token on start; drops it if the server rejects it. */
	async refresh() {
		if (!pb.authStore.isValid) return;
		try {
			await pb.collection('users').authRefresh();
		} catch {
			pb.authStore.clear();
		}
	},
};

let version = $state(0);
let timer: ReturnType<typeof setTimeout> | undefined;

/** A counter bumped (debounced) on every realtime thread change; views reload on it. */
export const live = {
	get version() {
		return version;
	},
	bump() {
		clearTimeout(timer);
		timer = setTimeout(() => version++, 150);
	},
	async subscribe() {
		return pb.collection('threads').subscribe('*', () => live.bump());
	},
};

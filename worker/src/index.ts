/**
 * Roost inbound Email Worker. Spools raw mail to R2 and nudges the backend.
 * Logic lives in handler.ts so it can be tested without the Workers runtime.
 */
import { handleEmail, type RoostEnv } from './handler';

export default {
	async email(message, env, ctx): Promise<void> {
		await handleEmail(message, env, ctx);
	},
} satisfies ExportedHandler<Env & RoostEnv>;

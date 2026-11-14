package api

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// noOAuth2Signup refuses an OAuth2 sign-in that would create a user. PocketBase signs
// up unknown OAuth2 users whatever the create rule says, and Roost has one user, made
// with `roost user upsert`: a provider account links to it by email.
func noOAuth2Signup(e *core.RecordAuthWithOAuth2RequestEvent) error {
	if e.IsNewRecord {
		return e.ForbiddenError("No Roost account uses this email. Sign in with the address of your Roost user.", nil)
	}
	return e.Next()
}

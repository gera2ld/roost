package api

import (
	"errors"
	"net/http"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
)

func TestNoOAuth2Signup(t *testing.T) {
	reached := false
	finish := func(*core.RecordAuthWithOAuth2RequestEvent) error { reached = true; return nil }

	signup := &core.RecordAuthWithOAuth2RequestEvent{RequestEvent: &core.RequestEvent{}, IsNewRecord: true}
	h := hook.Hook[*core.RecordAuthWithOAuth2RequestEvent]{}
	h.BindFunc(noOAuth2Signup)
	err := h.Trigger(signup, finish)
	var apiErr *router.ApiError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden || reached {
		t.Fatalf("signup: err %v, reached %v; want 403 before the sign-in", err, reached)
	}

	existing := &core.RecordAuthWithOAuth2RequestEvent{RequestEvent: &core.RequestEvent{}}
	if err := h.Trigger(existing, finish); err != nil || !reached {
		t.Fatalf("existing user: err %v, reached %v", err, reached)
	}
}

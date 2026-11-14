package api

import (
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// SecurityHeaders hardens every response. The page CSP for the web app comes from
// SvelteKit (a <meta> tag with script hashes); this header adds what a meta tag
// cannot carry. The PocketBase admin UI under /_/ keeps its own defaults.
func SecurityHeaders(e *core.RequestEvent) error {
	h := e.Response.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	if !strings.HasPrefix(e.Request.URL.Path, "/_/") {
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	}
	return e.Next()
}

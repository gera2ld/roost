package store

import (
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
)

func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestLoadAuthConfig(t *testing.T) {
	cfg, err := LoadAuthConfig(envOf(nil))
	if err != nil || cfg.Password != nil || cfg.Providers != nil {
		t.Fatalf("unset: %+v, %v", cfg, err)
	}

	cfg, err = LoadAuthConfig(envOf(map[string]string{
		"ROOST_PASSWORD_AUTH":               "false",
		"ROOST_OAUTH2_PROVIDERS":            " GitHub, oidc ,github",
		"ROOST_OAUTH2_GITHUB_CLIENT_ID":     "id",
		"ROOST_OAUTH2_GITHUB_CLIENT_SECRET": "secret",
		"ROOST_OAUTH2_OIDC_CLIENT_ID":       "id2",
		"ROOST_OAUTH2_OIDC_CLIENT_SECRET":   "secret2",
		"ROOST_OAUTH2_OIDC_AUTH_URL":        "https://id.example.com/authorize",
		"ROOST_OAUTH2_OIDC_DISPLAY_NAME":    "My SSO",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password == nil || *cfg.Password || len(cfg.Providers) != 2 || cfg.Providers[0].Name != "github" ||
		cfg.Providers[1].AuthURL != "https://id.example.com/authorize" || cfg.Providers[1].DisplayName != "My SSO" {
		t.Fatalf("got %+v", cfg)
	}

	cfg, err = LoadAuthConfig(envOf(map[string]string{"ROOST_OAUTH2_PROVIDERS": "none"}))
	if err != nil || cfg.Providers == nil || len(cfg.Providers) != 0 {
		t.Fatalf("none: %+v, %v", cfg, err)
	}

	for name, vars := range map[string]map[string]string{
		"bad bool":         {"ROOST_PASSWORD_AUTH": "nope"},
		"unknown provider": {"ROOST_OAUTH2_PROVIDERS": "myspace"},
		"missing secret":   {"ROOST_OAUTH2_PROVIDERS": "google", "ROOST_OAUTH2_GOOGLE_CLIENT_ID": "id"},
	} {
		if _, err := LoadAuthConfig(envOf(vars)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func usersAuth(t *testing.T, app core.App) *core.Collection {
	t.Helper()
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	return users
}

// Sign-in settings made in the admin UI survive a sync; the environment overrides
// each one it sets; an export never carries them.
func TestAuthConfigSync(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	if err := applySchema(app, AuthConfig{}); err != nil {
		t.Fatal(err)
	}

	// As the admin UI would: GitHub on, password off.
	users := usersAuth(t, app)
	users.PasswordAuth.Enabled = false
	users.OAuth2.Enabled = true
	users.OAuth2.Providers = []core.OAuth2ProviderConfig{{Name: "github", ClientId: "ui-id", ClientSecret: "ui-secret"}}
	if err := app.Save(users); err != nil {
		t.Fatal(err)
	}

	if err := applySchema(app, AuthConfig{}); err != nil {
		t.Fatal(err)
	}
	users = usersAuth(t, app)
	if users.PasswordAuth.Enabled || !users.OAuth2.Enabled || len(users.OAuth2.Providers) != 1 ||
		users.OAuth2.Providers[0].ClientSecret != "ui-secret" {
		t.Fatalf("no env: password %v, oauth2 %+v; want the admin settings kept", users.PasswordAuth.Enabled, users.OAuth2)
	}

	exported, err := ExportSchema(app)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(exported), "ui-secret") || strings.Contains(string(exported), "ui-id") {
		t.Fatal("export carries the OAuth2 client settings")
	}
	if string(exported) != string(schemaJSON) {
		t.Fatal("export with sign-in settings differs from schema.json")
	}

	// The environment wins for what it sets and keeps the rest.
	on := true
	if err := applySchema(app, AuthConfig{Password: &on}); err != nil {
		t.Fatal(err)
	}
	users = usersAuth(t, app)
	if !users.PasswordAuth.Enabled || len(users.OAuth2.Providers) != 1 {
		t.Fatalf("password env: password %v, providers %d", users.PasswordAuth.Enabled, len(users.OAuth2.Providers))
	}
	env := []core.OAuth2ProviderConfig{{Name: "google", ClientId: "env-id", ClientSecret: "env-secret"}}
	if err := applySchema(app, AuthConfig{Providers: env}); err != nil {
		t.Fatal(err)
	}
	users = usersAuth(t, app)
	if !users.OAuth2.Enabled || len(users.OAuth2.Providers) != 1 || users.OAuth2.Providers[0].Name != "google" {
		t.Fatalf("providers env: %+v", users.OAuth2)
	}
	if err := applySchema(app, AuthConfig{Providers: []core.OAuth2ProviderConfig{}}); err != nil {
		t.Fatal(err)
	}
	if users = usersAuth(t, app); users.OAuth2.Enabled || len(users.OAuth2.Providers) != 0 {
		t.Fatalf("providers none: %+v", users.OAuth2)
	}

	// No way to sign in is refused, and leaves the database as it was.
	off := false
	if err := applySchema(app, AuthConfig{Password: &off}); err == nil {
		t.Fatal("password off with no provider: want an error")
	}
	if !usersAuth(t, app).PasswordAuth.Enabled {
		t.Fatal("a refused sync changed the settings")
	}
}

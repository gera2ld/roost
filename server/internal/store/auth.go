package store

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/auth"
)

// AuthConfig is how users sign in. The snapshot is applied on every start and would
// reset the users collection's sign-in settings, so each one is carried over from the
// database (as set in the admin UI) unless the environment sets it. A nil field means
// "not set by the environment".
type AuthConfig struct {
	Password  *bool
	Providers []core.OAuth2ProviderConfig // nil: keep the database's providers
}

// LoadAuthConfig reads ROOST_PASSWORD_AUTH (true or false) and ROOST_OAUTH2_PROVIDERS,
// a comma-separated list of PocketBase provider names ("none" turns OAuth2 off). Each
// provider NAME needs ROOST_OAUTH2_<NAME>_CLIENT_ID and _CLIENT_SECRET; _AUTH_URL,
// _TOKEN_URL, _USER_INFO_URL and _DISPLAY_NAME are optional (oidc and self-hosted
// providers need the URLs).
func LoadAuthConfig(getenv func(string) string) (AuthConfig, error) {
	var cfg AuthConfig
	if raw := strings.TrimSpace(getenv("ROOST_PASSWORD_AUTH")); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("ROOST_PASSWORD_AUTH: %q is not true or false", raw)
		}
		cfg.Password = &enabled
	}
	raw := strings.TrimSpace(getenv("ROOST_OAUTH2_PROVIDERS"))
	if raw == "" {
		return cfg, nil
	}
	cfg.Providers = []core.OAuth2ProviderConfig{}
	if strings.EqualFold(raw, "none") {
		return cfg, nil
	}
	for name := range strings.SplitSeq(raw, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, ok := auth.Providers[name]; !ok {
			return cfg, fmt.Errorf("ROOST_OAUTH2_PROVIDERS: unknown provider %q", name)
		}
		if slices.ContainsFunc(cfg.Providers, func(p core.OAuth2ProviderConfig) bool { return p.Name == name }) {
			continue
		}
		prefix := "ROOST_OAUTH2_" + strings.ToUpper(name) + "_"
		provider := core.OAuth2ProviderConfig{
			Name:         name,
			ClientId:     getenv(prefix + "CLIENT_ID"),
			ClientSecret: getenv(prefix + "CLIENT_SECRET"),
			AuthURL:      getenv(prefix + "AUTH_URL"),
			TokenURL:     getenv(prefix + "TOKEN_URL"),
			UserInfoURL:  getenv(prefix + "USER_INFO_URL"),
			DisplayName:  getenv(prefix + "DISPLAY_NAME"),
		}
		if provider.ClientId == "" || provider.ClientSecret == "" {
			return cfg, fmt.Errorf("OAuth2 provider %s needs %sCLIENT_ID and %sCLIENT_SECRET", name, prefix, prefix)
		}
		cfg.Providers = append(cfg.Providers, provider)
	}
	return cfg, nil
}

// withAuthConfig returns the snapshot with the users collection's sign-in settings
// taken from cfg where set, else from current (the database's users collection, nil
// on a fresh database, which keeps the snapshot defaults).
func withAuthConfig(snapshot []byte, cfg AuthConfig, current *core.Collection) ([]byte, error) {
	password, oauth2Enabled := true, false
	var providers []core.OAuth2ProviderConfig
	if current != nil {
		password = current.PasswordAuth.Enabled
		oauth2Enabled, providers = current.OAuth2.Enabled, current.OAuth2.Providers
	}
	if cfg.Password != nil {
		password = *cfg.Password
	}
	if cfg.Providers != nil {
		providers = cfg.Providers
		oauth2Enabled = len(providers) > 0
	}
	if !password && (!oauth2Enabled || len(providers) == 0) {
		return nil, fmt.Errorf("password sign-in is off and no OAuth2 provider is set: set ROOST_PASSWORD_AUTH=true or ROOST_OAUTH2_PROVIDERS")
	}
	if providers == nil {
		providers = []core.OAuth2ProviderConfig{}
	}

	var entries []map[string]any
	if err := json.Unmarshal(snapshot, &entries); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry["name"] != usersCollection {
			continue
		}
		passwordAuth, _ := entry["passwordAuth"].(map[string]any)
		oauth2, _ := entry["oauth2"].(map[string]any)
		if passwordAuth == nil || oauth2 == nil {
			return nil, fmt.Errorf("users collection has no passwordAuth or oauth2 options")
		}
		passwordAuth["enabled"] = password
		oauth2["enabled"] = oauth2Enabled
		oauth2["providers"] = providers
		return json.Marshal(entries)
	}
	return nil, fmt.Errorf("snapshot has no %s collection", usersCollection)
}

const usersCollection = "users"

// resetAuthOptions puts the users collection's sign-in methods back to the snapshot
// defaults, so an export never carries environment settings or client secrets.
func resetAuthOptions(entry map[string]any) {
	if entry["name"] != usersCollection {
		return
	}
	if password, ok := entry["passwordAuth"].(map[string]any); ok {
		password["enabled"] = true
	}
	if oauth2, ok := entry["oauth2"].(map[string]any); ok {
		oauth2["enabled"] = false
		oauth2["providers"] = []any{}
	}
}

// envAuthConfig is LoadAuthConfig over the process environment.
func envAuthConfig() (AuthConfig, error) { return LoadAuthConfig(os.Getenv) }

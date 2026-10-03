//go:build gui

package gui

import (
	"context"

	"github.com/thedavidweng/tg-drive/internal/service"
	"github.com/thedavidweng/tg-drive/internal/version"
)

// Settings is the facade service for config and appearance preferences.
type Settings struct {
	opts service.Options
	// ThemeChanges announces the Omarchy theme after it changes, or is nil
	// off Omarchy. cmd/td-gui emits each value as OmarchyThemeChangedEvent.
	ThemeChanges <-chan OmarchyTheme
}

// ConfigEntry is one config key and its display value.
type ConfigEntry struct {
	Key string `json:"key"`
	// Value is redacted unless the caller revealed the secret.
	Value  any  `json:"value"`
	Secret bool `json:"secret"`
}

func configEntryOf(e service.ConfigEntry) ConfigEntry {
	return ConfigEntry{Key: e.Key, Value: e.Value, Secret: e.Secret}
}

// List returns every config key in config-file order, secrets redacted.
func (s *Settings) List(ctx context.Context) ([]ConfigEntry, error) {
	view, err := service.GetConfig(s.opts, service.ConfigGetOptions{})
	if err != nil {
		return nil, toError(err)
	}
	out := make([]ConfigEntry, 0, len(view.Entries))
	for _, e := range view.Entries {
		out = append(out, configEntryOf(e))
	}
	return out, nil
}

// Reveal returns one config entry with its secret value shown. confirmed is
// the frontend's explicit reveal click; without it the service's
// confirmation gate refuses, and the refusal reaches the frontend.
func (s *Settings) Reveal(ctx context.Context, key string, confirmed bool) (ConfigEntry, error) {
	view, err := service.GetConfig(s.opts, service.ConfigGetOptions{Key: key, ShowSecrets: true, Confirmed: confirmed})
	if err != nil {
		return ConfigEntry{}, toError(err)
	}
	return configEntryOf(view.Entries[0]), nil
}

// Set validates and saves one config key, returning the entry as it now
// displays (a secret stays redacted).
func (s *Settings) Set(ctx context.Context, key, value string) (ConfigEntry, error) {
	if _, err := service.SetConfig(s.opts, key, value); err != nil {
		return ConfigEntry{}, toError(err)
	}
	view, err := service.GetConfig(s.opts, service.ConfigGetOptions{Key: key})
	if err != nil {
		return ConfigEntry{}, toError(err)
	}
	return configEntryOf(view.Entries[0]), nil
}

// Versions are the versions the About rows show.
type Versions struct {
	GUI string `json:"gui"`
	CLI string `json:"cli"`
}

// Versions reports the version of both binaries. td and td-gui build from
// one module stamped with one version at release, so the running binary's
// stamp is the CLI's.
func (s *Settings) Versions(ctx context.Context) Versions {
	return Versions{GUI: version.Version, CLI: version.Version}
}

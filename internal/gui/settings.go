//go:build gui

package gui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/version"
)

// Settings is the facade service for config and appearance preferences. It
// reads and writes the config file the CLI shares, so it opens with no
// GUI-specific overrides: the GUI's own session path never reaches the
// file.
type Settings struct {
	state *appState
	opts  service.Options
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
	// RestartRequired marks a saved key the running GUI cannot apply: one
	// its Telegram client or database was opened with. It takes effect
	// when td-gui next starts; every other key applies at once.
	RestartRequired bool `json:"restart_required,omitempty"`
}

// appliesOnRestart reports whether key configures what the GUI opened once
// at start (the Telegram client, the Session lock, the database).
func appliesOnRestart(key string) bool {
	return strings.HasPrefix(key, "telegram.") || strings.HasPrefix(key, "storage.") ||
		strings.HasPrefix(key, "rate_limit.") || key == "locks.session_wait_seconds"
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
// displays (a secret stays redacted), and applies it to the running GUI
// unless it is RestartRequired.
func (s *Settings) Set(ctx context.Context, key, value string) (ConfigEntry, error) {
	if _, err := service.SetConfig(s.opts, key, value); err != nil {
		return ConfigEntry{}, toError(err)
	}
	view, err := service.GetConfig(s.opts, service.ConfigGetOptions{Key: key})
	if err != nil {
		return ConfigEntry{}, toError(err)
	}
	entry := configEntryOf(view.Entries[0])
	if appliesOnRestart(key) {
		entry.RestartRequired = true
		return entry, nil
	}
	if err := s.state.reloadConfig(); err != nil {
		return ConfigEntry{}, err
	}
	return entry, nil
}

// Versions are the versions the About rows show.
type Versions struct {
	GUI string `json:"gui"`
	CLI string `json:"cli"`
}

// Versions reports td-gui's own version and that of the td CLI installed
// beside it. The two install separately, so the CLI's version is asked of
// the td binary itself; CLI is empty when none is found or it does not
// answer.
func (s *Settings) Versions(ctx context.Context) Versions {
	return Versions{GUI: version.Version, CLI: installedCLIVersion(ctx)}
}

// installedCLIVersion runs `td version --json` on the first td found.
func installedCLIVersion(ctx context.Context) string {
	bin := findCLI()
	if bin == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version", "--json").Output()
	if err != nil {
		return ""
	}
	var env struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &env) != nil {
		return ""
	}
	return env.Data.Version
}

// findCLI locates td: on PATH, else beside td-gui, else in the per-user
// and package-manager directories the installers use. A GUI app launched
// from the desktop often gets a minimal PATH (macOS Finder never sees
// Homebrew's), so PATH alone would miss an installed CLI.
func findCLI() string {
	name := "td"
	if runtime.GOOS == "windows" {
		name = "td.exe"
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			dirs = append(dirs, filepath.Join(d, "tg-drive-cli", "bin"))
		}
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

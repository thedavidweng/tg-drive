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

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/internal/config"
	"github.com/thedavidweng/tg-drive/internal/service"
	"github.com/thedavidweng/tg-drive/internal/version"
)

// Settings is the facade service for config and appearance preferences.
type Settings struct {
	opts  service.Options
	state *appState
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
// at start (the Telegram client, the Session lock, the database), so a
// change to it cannot reach the running App.
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
// displays (a secret stays redacted). A key the GUI opened at start with —
// its Telegram client, Session lock, or database — cannot be reapplied to the
// running App, so it is reported RestartRequired instead of being claimed as
// live; the rest reach later calls at once.
// Live changes other than transfers.concurrency return ERR_OPERATION_LOCKED
// while GUI transfers are running; retry after they finish.
//
// The order is prepare, persist, apply: the value is validated against the
// running config before anything is written, and applying the prepared config
// cannot fail. A save that succeeded is never reported as a failed one while
// the disk and the App disagree.
func (s *Settings) Set(ctx context.Context, key, value string) (ConfigEntry, error) {
	s.state.lifecycle.Lock()
	defer s.state.lifecycle.Unlock()
	if s.state.closing {
		return ConfigEntry{}, toError(apperr.Cancelled())
	}
	concurrency := key == "transfers.concurrency"
	if !concurrency && !appliesOnRestart(key) && s.state.currentManager().Running() != 0 {
		return ConfigEntry{}, toError(apperr.New(apperr.ErrOperationLocked, "cannot change live settings while GUI transfers are running"))
	}
	cfg, shown, err := s.state.prepared(key, value)
	if err != nil {
		return ConfigEntry{}, err
	}
	// Compute the value List will display before saving, including overrides,
	// so reporting a successful save does not require another file read.
	fileCfg, err := config.LoadFile(config.ConfigPath(s.opts.ConfigPath))
	if err != nil {
		return ConfigEntry{}, toError(err)
	}
	if err := config.SetValue(&fileCfg, key, value); err != nil {
		return ConfigEntry{}, toError(err)
	}
	effective, err := config.ApplyOverrides(fileCfg, config.Overrides{
		ConfigPath: s.opts.ConfigPath, DBPath: s.opts.DBPath, SessionPath: s.opts.SessionPath, Channel: s.opts.Channel,
	})
	if err != nil {
		return ConfigEntry{}, toError(err)
	}
	display, err := config.GetValue(effective, key)
	if err != nil {
		return ConfigEntry{}, toError(err)
	}
	if _, err := service.SetConfig(s.opts, key, value); err != nil {
		return ConfigEntry{}, toError(err)
	}
	entry := ConfigEntry{Key: key, Value: config.RedactValue(key, display, false), Secret: config.IsSecret(key)}
	if appliesOnRestart(key) {
		entry.RestartRequired = true
		return entry, nil
	}
	if n, ok := shown.(int); ok && concurrency {
		s.state.limiter.SetLimit(n)
	}
	s.state.applyConfig(cfg, !concurrency)
	return entry, nil
}

// Versions are the versions the About rows show.
type Versions struct {
	GUI string `json:"gui"`
	CLI string `json:"cli"`
}

// Versions reports the running GUI's stamp and the version of the td CLI
// installed beside it. The two install separately, so the CLI's version
// comes from running the td binary found on PATH or in the installers'
// default directory; CLI is empty when none is found or it does not
// answer `td version --json` like td does.
func (s *Settings) Versions(ctx context.Context) Versions {
	return Versions{GUI: version.Version, CLI: installedCLIVersion(ctx)}
}

// cliProbeTimeout bounds the `td version --json` probe.
const cliProbeTimeout = 3 * time.Second

func installedCLIVersion(ctx context.Context) string {
	for _, bin := range cliCandidates() {
		if v := probeCLIVersion(ctx, bin); v != "" {
			return v
		}
	}
	return ""
}

// cliCandidates are the td binaries to probe, in order: PATH, then the
// install scripts' directories, which a GUI launched from the desktop
// usually lacks on its PATH.
func cliCandidates() []string {
	name := "td"
	if runtime.GOOS == "windows" {
		name = "td.exe"
	}
	var out []string
	if p, err := exec.LookPath(name); err == nil {
		out = append(out, p)
	}
	if dir := os.Getenv("TD_INSTALL_DIR"); dir != "" {
		out = append(out, filepath.Join(dir, name))
	}
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			out = append(out, filepath.Join(dir, "tg-drive", "bin", name))
		}
	} else if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".local", "bin", name))
	}
	return out
}

func probeCLIVersion(ctx context.Context, bin string) string {
	if st, err := os.Stat(bin); err != nil || st.IsDir() {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, cliProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version", "--json").Output()
	if err != nil {
		return ""
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &env) != nil || !env.OK {
		return ""
	}
	return env.Data.Version
}

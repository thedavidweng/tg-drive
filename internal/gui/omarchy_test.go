//go:build gui

package gui_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedOmarchy writes the Omarchy state td-gui reads: the current theme's
// colors.toml with its name beside it, and a Hyprland config that rounds
// windows. It points detection at the seeded files.
func seedOmarchy(t *testing.T, colors string) string {
	t.Helper()
	dir := t.TempDir()
	current := filepath.Join(dir, "state", "omarchy", "current")
	theme := filepath.Join(current, "theme")
	if err := os.MkdirAll(theme, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(theme, "colors.toml"), []byte(colors), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "theme.name"), []byte("test-night\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hypr := filepath.Join(dir, "config", "hypr")
	if err := os.MkdirAll(hypr, 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "general {\n    border_size = 3\n}\ndecoration {\n    rounding = 6 # round corners\n}\n"
	if err := os.WriteFile(filepath.Join(hypr, "hyprland.conf"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("TD_OMARCHY", "1")
	t.Setenv("TD_OMARCHY_THEME", theme)
	return theme
}

const omarchyColors = `mode = "dark"
background = "#1a1b26"
foreground = "#c0caf5"
accent = "#7aa2f7"
`

func TestOmarchyReadsTheSeededTheme(t *testing.T) {
	seedConfig(t, nil)
	seedOmarchy(t, omarchyColors)
	svc := openGUI(t)

	state, err := svc.Settings.Omarchy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Available || state.Theme == nil {
		t.Fatalf("Omarchy = %+v, want available with a theme", state)
	}
	th := state.Theme
	if th.Name != "test-night" || th.Mode != "dark" {
		t.Fatalf("theme = %+v, want test-night / dark", th)
	}
	wantVars := map[string]string{
		"--bg":        "#1a1b26",
		"--fg":        "#c0caf5",
		"--accent":    "#7aa2f7",
		"--om-radius": "6px",
		"--om-border": "3px",
		"--shadow":    "none",
		"--seg-edge":  "none",
		"--seg-track": "transparent",
	}
	for k, want := range wantVars {
		if th.Vars[k] != want {
			t.Fatalf("theme var %s = %q, want %q (all vars: %+v)", k, th.Vars[k], want, th.Vars)
		}
	}
}

func TestOmarchyOffWhenDisabled(t *testing.T) {
	seedConfig(t, nil)
	seedOmarchy(t, omarchyColors)
	t.Setenv("TD_OMARCHY", "0")
	svc := openGUI(t)

	state, err := svc.Settings.Omarchy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Available {
		t.Fatalf("Omarchy = %+v, want unavailable when TD_OMARCHY=0", state)
	}
	if svc.Settings.ThemeChanges != nil {
		t.Fatal("ThemeChanges channel exists although Omarchy is off")
	}
}

func TestOmarchyThemeChangeEmits(t *testing.T) {
	seedConfig(t, nil)
	theme := seedOmarchy(t, omarchyColors)
	t.Setenv("TD_OMARCHY_POLL", "10ms")
	svc := openGUI(t)
	if svc.Settings.ThemeChanges == nil {
		t.Fatal("no ThemeChanges channel on a detected Omarchy")
	}

	changed := omarchyColors + "green = \"#9ece6a\"\n"
	accent := "#ff0000"
	changed = changed[:len(changed)-1] + "\naccent = \"" + accent + "\"\n"
	if err := os.WriteFile(filepath.Join(theme, "colors.toml"), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case th := <-svc.Settings.ThemeChanges:
		if th.Vars["--accent"] != accent {
			t.Fatalf("theme-changed accent = %q, want %q", th.Vars["--accent"], accent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no theme-changed event after colors.toml changed")
	}
}

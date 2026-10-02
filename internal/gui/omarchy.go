//go:build gui

package gui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This file is what td-gui knows of Omarchy (omarchy.org), the Hyprland
// desktop: whether it runs on one, and the look its current theme gives
// every surface, so the window can take that look there and nowhere else.
// The mapping follows magpie's internal/omarchy, trimmed to what the design
// tokens need: the palette, the corners (Hyprland's decoration:rounding,
// read from hyprland.conf) and the border (general:border_size).
//
// Omarchy (4.x) keeps the current theme under
// ~/.local/state/omarchy/current/theme (before 4.0:
// ~/.config/omarchy/current/theme): colors.toml is the palette every app's
// theme is made from, shell.toml the tokens Omarchy's own shell draws with,
// and theme.name beside them the theme's name.
//
// TD_OMARCHY=1/0 forces detection on/off, TD_OMARCHY_THEME points at a
// theme folder directly, and TD_OMARCHY_POLL sets the theme-change poll
// interval; the three exist for development and tests.

// OmarchyThemeChangedEvent is the typed event cmd/td-gui emits with an
// OmarchyTheme when the Omarchy theme changes.
const OmarchyThemeChangedEvent = "omarchy:theme-changed"

// OmarchyTheme is the current Omarchy theme as the frontend draws with it.
type OmarchyTheme struct {
	Name  string            `json:"name"`  // tokyo-night
	Mode  string            `json:"mode"`  // dark or light
	Stamp string            `json:"stamp"` // changes when any of it does
	Vars  map[string]string `json:"vars"`  // CSS custom properties
}

// OmarchyState is whether the Omarchy look applies, and the theme to draw
// it with.
type OmarchyState struct {
	Available bool          `json:"available"`
	Theme     *OmarchyTheme `json:"theme,omitempty"`
}

// Omarchy reports whether td-gui runs on Omarchy with a current theme to
// adopt, and that theme.
func (s *Settings) Omarchy(ctx context.Context) (OmarchyState, error) {
	th, ok := currentOmarchyTheme()
	if !ok {
		return OmarchyState{}, nil
	}
	return OmarchyState{Available: true, Theme: &th}, nil
}

// omarchyDetect reports whether td-gui runs on Omarchy.
func omarchyDetect() bool {
	switch os.Getenv("TD_OMARCHY") {
	case "1":
		return true
	case "0":
		return false
	}
	return runtime.GOOS == "linux" && omarchyThemeDir() != ""
}

// omarchyThemeDir is the current theme's folder, or "" when there is none.
func omarchyThemeDir() string {
	if d := os.Getenv("TD_OMARCHY_THEME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	for _, d := range []string{
		filepath.Join(state, "omarchy", "current", "theme"),
		filepath.Join(home, ".config", "omarchy", "current", "theme"), // before 4.0
	} {
		if _, err := os.Stat(filepath.Join(d, "colors.toml")); err == nil {
			return d
		}
	}
	return ""
}

// watchOmarchy polls the theme every interval and sends it on the returned
// channel after it changes, until ctx is done. The theme at call time is
// the baseline: the frontend gets it at load from Omarchy, and every later
// state from the channel.
func watchOmarchy(ctx context.Context, interval time.Duration) <-chan OmarchyTheme {
	var last string
	if th, ok := currentOmarchyTheme(); ok {
		last = th.Stamp
	}
	out := make(chan OmarchyTheme, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			th, ok := currentOmarchyTheme()
			if !ok || th.Stamp == last {
				continue
			}
			last = th.Stamp
			select {
			case out <- th:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// omarchyPollInterval is how often watchOmarchy re-reads the theme.
func omarchyPollInterval() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("TD_OMARCHY_POLL")); err == nil && d > 0 {
		return d
	}
	return time.Second
}

var omarchyCache struct {
	mu    sync.Mutex
	seen  string
	theme OmarchyTheme
}

// currentOmarchyTheme is the theme as it is now: the files are re-read only
// when one of them changed.
func currentOmarchyTheme() (OmarchyTheme, bool) {
	dir := omarchyThemeDir()
	if !omarchyDetect() || dir == "" {
		return OmarchyTheme{}, false
	}
	stamp := stampOf(omarchyWatched(dir))
	omarchyCache.mu.Lock()
	defer omarchyCache.mu.Unlock()
	if stamp == omarchyCache.seen && omarchyCache.theme.Stamp != "" {
		return omarchyCache.theme, true
	}
	colors, _ := os.ReadFile(filepath.Join(dir, "colors.toml"))
	shell, _ := os.ReadFile(filepath.Join(dir, "shell.toml"))
	name, _ := os.ReadFile(filepath.Join(filepath.Dir(dir), "theme.name"))
	rounding, border := hyprlandLook()
	vars, mode := omarchyVars(parseTOML(colors), parseTOML(shell), rounding, border)
	omarchyCache.seen = stamp
	omarchyCache.theme = OmarchyTheme{
		Name: strings.TrimSpace(string(name)), Mode: mode, Vars: vars,
		Stamp: fmt.Sprintf("%x", fnv(stamp+strconv.Itoa(rounding)+strconv.Itoa(border))),
	}
	return omarchyCache.theme, true
}

// omarchyWatched are the files whose change changes the look: the theme's
// and Hyprland's.
func omarchyWatched(dir string) []string {
	home, _ := os.UserHomeDir()
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	files := []string{
		filepath.Join(dir, "colors.toml"),
		filepath.Join(dir, "shell.toml"),
		filepath.Join(filepath.Dir(dir), "theme.name"),
	}
	hypr, _ := filepath.Glob(filepath.Join(cfg, "hypr", "*.conf"))
	return append(files, hypr...)
}

func stampOf(files []string) string {
	var b strings.Builder
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", filepath.Base(f), fi.ModTime().UnixNano(), fi.Size())
		}
	}
	return b.String()
}

func fnv(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// parseTOML reads a TOML file's key = value lines, a section's keys as
// "section.key": the little of TOML the theme files use (strings, numbers,
// booleans, comments).
func parseTOML(b []byte) map[string]string {
	out := map[string]string{}
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			section = strings.Trim(line, "[] ")
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) {
			if end := strings.Index(v[1:], `"`); end >= 0 {
				v = v[1 : end+1]
			}
		} else if i := strings.Index(v, "#"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if section != "" {
			k = section + "." + k
		}
		out[k] = v
	}
	return out
}

// hyprlandLook reads Hyprland's window rounding and border width from
// hyprland.conf and the files it sources (0 and 2, Hyprland's defaults,
// when they say nothing).
func hyprlandLook() (rounding, border int) {
	rounding, border = 0, 2
	home, _ := os.UserHomeDir()
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	readHyprland(filepath.Join(cfg, "hypr", "hyprland.conf"), 0, &rounding, &border)
	return rounding, border
}

func readHyprland(path string, depth int, rounding, border *int) {
	if depth > 8 {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if i := strings.Index(v, "#"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			switch {
			case k == "source":
				// source may glob and use ~ or $VARS.
				v = os.ExpandEnv(v)
				if strings.HasPrefix(v, "~/") {
					home, _ := os.UserHomeDir()
					v = filepath.Join(home, v[2:])
				}
				matches, _ := filepath.Glob(v)
				for _, m := range matches {
					readHyprland(m, depth+1, rounding, border)
				}
			case k == "decoration:rounding", section == "decoration" && k == "rounding":
				if n, err := strconv.Atoi(v); err == nil {
					*rounding = n
				}
			case k == "general:border_size", section == "general" && k == "border_size":
				if n, err := strconv.Atoi(v); err == nil {
					*border = n
				}
			}
			continue
		}
		if strings.HasSuffix(line, "{") {
			section = strings.TrimSpace(strings.TrimSuffix(line, "{"))
		} else if line == "}" {
			section = ""
		}
	}
}

// omarchyColor is an RGBA colour, each part 0..1.
type omarchyColor struct{ r, g, b, a float64 }

// parseColor reads #rgb, #rrggbb, #rrggbbaa and CSS's rgb(r, g, b) and
// rgba(r, g, b, a).
func parseColor(s string) (omarchyColor, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	hex := func(h string) (omarchyColor, bool) {
		switch len(h) {
		case 3:
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		case 6:
		case 8:
		default:
			return omarchyColor{}, false
		}
		n, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return omarchyColor{}, false
		}
		if len(h) == 6 {
			n = n<<8 | 0xff
		}
		return omarchyColor{float64(n>>24&0xff) / 255, float64(n>>16&0xff) / 255, float64(n>>8&0xff) / 255, float64(n&0xff) / 255}, true
	}
	switch {
	case strings.HasPrefix(s, "#"):
		return hex(s[1:])
	case strings.HasPrefix(s, "rgb"):
		open, end := strings.Index(s, "("), strings.LastIndex(s, ")")
		if open < 0 || end < open {
			return omarchyColor{}, false
		}
		in := strings.TrimSpace(s[open+1 : end])
		if !strings.Contains(in, ",") {
			return hex(in)
		}
		parts := strings.Split(in, ",")
		if len(parts) < 3 {
			return omarchyColor{}, false
		}
		var v [4]float64
		v[3] = 1
		for i, p := range parts[:min(len(parts), 4)] {
			f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return omarchyColor{}, false
			}
			if i < 3 {
				f /= 255
			}
			v[i] = f
		}
		return omarchyColor{v[0], v[1], v[2], v[3]}, true
	}
	return omarchyColor{}, false
}

func (c omarchyColor) css() string {
	to := func(f float64) int { return int(max(0, min(255, f*255+0.5))) }
	if c.a >= 0.999 {
		return fmt.Sprintf("#%02x%02x%02x", to(c.r), to(c.g), to(c.b))
	}
	return fmt.Sprintf("rgba(%d, %d, %d, %s)", to(c.r), to(c.g), to(c.b), strconv.FormatFloat(max(0, min(1, c.a)), 'f', 3, 64))
}

// over is c at alpha a laid over the opaque bg, as one opaque colour.
func (c omarchyColor) over(bg omarchyColor, a float64) omarchyColor {
	return omarchyColor{bg.r + (c.r-bg.r)*a, bg.g + (c.g-bg.g)*a, bg.b + (c.b-bg.b)*a, 1}
}

func (c omarchyColor) alpha(a float64) omarchyColor { c.a = a; return c }

// luminance is c's relative luminance, to tell a light theme from a dark
// one that doesn't say.
func (c omarchyColor) luminance() float64 { return 0.2126*c.r + 0.7152*c.g + 0.0722*c.b }

// omarchyVars turns the theme's palette (colors.toml) and shell tokens
// (shell.toml) into the frontend's CSS custom properties, the corners'
// radius and the border's width given.
func omarchyVars(colors, shell map[string]string, rounding, border int) (map[string]string, string) {
	get := func(m map[string]string, fallback omarchyColor, keys ...string) omarchyColor {
		for _, k := range keys {
			v := m[k]
			// a shell token may name another: "hyprland.active-border"
			for range 3 {
				if ref, ok := shell[v]; ok {
					v = ref
				}
			}
			if c, ok := parseColor(v); ok {
				return c
			}
		}
		return fallback
	}
	num := func(k string, fallback float64) float64 {
		if f, err := strconv.ParseFloat(shell[k], 64); err == nil {
			return f
		}
		return fallback
	}
	bg := get(shell, get(colors, omarchyColor{0.102, 0.106, 0.149, 1}, "background"), "popups.background")
	bg.a = 1
	fg := get(shell, get(colors, omarchyColor{0.663, 0.694, 0.839, 1}, "foreground"), "popups.text")
	mode := strings.ToLower(colors["mode"])
	if mode != "light" && mode != "dark" {
		mode = "dark"
		if bg.luminance() > 0.5 {
			mode = "light"
		}
	}
	dim := get(colors, fg.over(bg, 0.55), "dark_foreground")
	if mode == "light" {
		dim = get(colors, fg.over(bg, 0.6), "muted", "dark_foreground")
	}
	accent := get(shell, get(colors, omarchyColor{0.478, 0.635, 0.969, 1}, "accent", "blue"), "menu.selected-text")
	red := get(colors, omarchyColor{0.969, 0.463, 0.557, 1}, "red")
	green := get(colors, omarchyColor{0.62, 0.808, 0.416, 1}, "green")
	yellow := get(colors, omarchyColor{0.878, 0.686, 0.408, 1}, "yellow")
	edge := get(shell, accent, "popups.border", "hyprland.active-border")
	selFg := get(shell, fg, "menu.selected-background")
	selA := num("menu.selected-background-alpha", 0.08)
	fillA := num("controls.normal-fill-alpha", 0.04)
	hoverA := num("controls.hover-cursor-fill-alpha", 0.08)
	soft := func(c omarchyColor) string { return c.over(bg, 0.16).css() }
	v := map[string]string{
		"--bg":          bg.css(),
		"--card":        fg.over(bg, fillA).css(),
		"--card-2":      fg.over(bg, fillA/2).css(),
		"--pill":        fg.over(bg, fillA).css(),
		"--pill-hover":  fg.over(bg, hoverA).css(),
		"--seg-track":   "transparent",
		"--seg-thumb":   fg.over(bg, num("controls.selected-fill-alpha", 0.18)).css(),
		"--seg-edge":    "none",
		"--ctl-fg":      dim.css(),
		"--line":        fg.over(bg, 0.16).css(),
		"--line-2":      fg.over(bg, 0.1).css(),
		"--fg":          fg.css(),
		"--fg-2":        fg.css(),
		"--muted":       dim.css(),
		"--faint":       fg.over(bg, 0.38).css(),
		"--accent":      accent.css(),
		"--accent-soft": accent.over(bg, 0.14).css(),
		"--accent-fg":   bg.css(),
		"--sel":         selFg.over(bg, selA).css(),
		"--green":       green.css(),
		"--green-soft":  soft(green),
		"--red":         red.css(),
		"--red-soft":    soft(red),
		"--amber":       yellow.css(),
		"--amber-soft":  soft(yellow),
		"--pop-bg":      bg.css(),
		"--shadow":      "none",
		"--om-edge":     edge.css(),
		"--om-border":   strconv.Itoa(max(border, 1)) + "px",
		"--om-radius":   strconv.Itoa(max(rounding, 0)) + "px",
	}
	return v, mode
}

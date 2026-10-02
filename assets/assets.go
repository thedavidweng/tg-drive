//go:build gui

// Package assets embeds the app icon for the GUI's native integration (the
// system tray icon). It lives beside the icon because embed patterns
// cannot reach parent directories; packaging scripts read the same file
// from disk.
package assets

import _ "embed"

// Icon is the app icon as PNG bytes (512×512; every platform's tray
// scales it down).
//
//go:embed icon.png
var Icon []byte

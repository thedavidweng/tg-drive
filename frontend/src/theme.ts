export type ThemeMode = "system" | "light" | "dark"

/**
 * The localStorage key of the manual theme override. index.html reads it in
 * an inline script so the override applies before first paint.
 */
export const themeStorageKey = "td-theme"

export function storedTheme(): ThemeMode {
  const v = localStorage.getItem(themeStorageKey)
  return v === "light" || v === "dark" ? v : "system"
}

/** "system" removes the override, leaving prefers-color-scheme in charge. */
export function applyTheme(mode: ThemeMode) {
  if (mode === "system") {
    localStorage.removeItem(themeStorageKey)
    delete document.documentElement.dataset.theme
  } else {
    localStorage.setItem(themeStorageKey, mode)
    document.documentElement.dataset.theme = mode
  }
}

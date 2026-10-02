import type { OmarchyTheme } from "@/backend"

/**
 * The localStorage key of the Omarchy switch. Only "off" is written;
 * anything else means follow Omarchy when it is detected. The switch is a
 * GUI-side display preference, like the theme override (src/theme.ts), not
 * a td config key.
 */
export const omarchyStorageKey = "td-omarchy"

export function omarchyEnabled(): boolean {
  return localStorage.getItem(omarchyStorageKey) !== "off"
}

export function storeOmarchyEnabled(on: boolean) {
  if (on) {
    localStorage.removeItem(omarchyStorageKey)
  } else {
    localStorage.setItem(omarchyStorageKey, "off")
  }
}

/**
 * Draw the Omarchy theme: the class turns on the flat rules in index.css,
 * and the style element carries the theme's tokens so they win over the
 * light and dark palettes.
 */
export function applyOmarchyTheme(theme: OmarchyTheme) {
  const root = document.documentElement
  root.classList.add("omarchy")
  root.dataset.omarchyMode = theme.mode
  let style = document.getElementById("omarchy-theme")
  if (!style) {
    style = document.createElement("style")
    style.id = "omarchy-theme"
    document.head.append(style)
  }
  const decls = Object.entries(theme.vars ?? {})
    .map(([k, v]) => `${k}: ${v} !important;`)
    .join(" ")
  style.textContent = `:root.omarchy, :root.omarchy body { ${decls} color-scheme: ${theme.mode === "light" ? "light" : "dark"}; }`
}

export function clearOmarchyTheme() {
  document.documentElement.classList.remove("omarchy")
  delete document.documentElement.dataset.omarchyMode
  document.getElementById("omarchy-theme")?.remove()
}

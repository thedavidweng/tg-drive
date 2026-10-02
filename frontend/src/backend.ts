import type {
  ConfigEntry,
  Entry,
  OmarchyState,
  OmarchyTheme,
  Versions,
} from "../bindings/github.com/thedavidweng/tg-drive-cli/internal/gui/models"

export type { ConfigEntry, Entry, OmarchyState, OmarchyTheme, Versions }

/**
 * The error every facade call rejects with: the code and category of the
 * JSON contract's error envelope.
 */
export interface BackendError {
  code: string
  category: string
  message: string
}

/**
 * Everything the screens need from Go. The app uses the generated Wails
 * bindings (wails-backend.ts); tests pass an in-memory backend.
 */
export interface Backend {
  drive: {
    list(path: string): Promise<Entry[]>
  }
  settings: {
    /** Every config key in file order, secrets redacted. */
    listConfig(): Promise<ConfigEntry[]>
    /** One secret's real value; confirmed is the user's reveal click. */
    revealSecret(key: string, confirmed: boolean): Promise<ConfigEntry>
    /** Validate and save one key; resolves to the entry as it now displays. */
    setConfig(key: string, value: string): Promise<ConfigEntry>
    versions(): Promise<Versions>
    omarchy(): Promise<OmarchyState>
    /** Subscribe to omarchy:theme-changed; returns an unsubscribe. */
    onOmarchyTheme(cb: (theme: OmarchyTheme) => void): () => void
  }
}

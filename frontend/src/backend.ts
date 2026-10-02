import type {
  AuthPrompt,
  AuthStatus,
  AuthUser,
  ConfigEntry,
  Entry,
  LoginResult,
  OmarchyState,
  OmarchyTheme,
  Versions,
} from "../bindings/github.com/thedavidweng/tg-drive-cli/internal/gui/models"

export type { AuthPrompt, AuthStatus, AuthUser, ConfigEntry, Entry, LoginResult, OmarchyState, OmarchyTheme, Versions }

/**
 * The error every facade call rejects with: the code and category of the
 * JSON contract's error envelope, plus its machine-readable details (for
 * example retry_after_seconds on ERR_TELEGRAM_RATE_LIMITED).
 */
export interface BackendError {
  code: string
  category: string
  message: string
  details?: Record<string, unknown>
}

/**
 * Everything the screens need from Go. The app uses the generated Wails
 * bindings (wails-backend.ts); tests pass an in-memory backend.
 */
export interface Backend {
  drive: {
    list(path: string): Promise<Entry[]>
  }
  auth: {
    status(): Promise<AuthStatus>
    setup(apiID: string, apiHash: string, phone: string): Promise<AuthStatus>
    login(phone: string, forceNewCode: boolean): Promise<LoginResult>
    logout(): Promise<void>
    answerPrompt(id: string, value: string): Promise<void>
    cancelPrompt(id: string): Promise<void>
    /** Subscribes to the typed auth.prompt event; returns an unsubscribe. */
    onPrompt(cb: (prompt: AuthPrompt) => void): () => void
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

import type {
  AuthPrompt,
  AuthStatus,
  AuthUser,
  Backend,
  BackendError,
  ConfigEntry,
  Entry,
  LoginResult,
  OmarchyState,
  OmarchyTheme,
  Versions,
} from "@/backend"

/** A tiny synchronous event source for tests that push backend events. */
export function eventEmitter<T>() {
  const subs = new Set<(v: T) => void>()
  return {
    subscribe(cb: (v: T) => void): () => void {
      subs.add(cb)
      return () => subs.delete(cb)
    },
    emit(v: T) {
      for (const cb of subs) cb(v)
    },
  }
}

/** How the in-memory Settings service answers. */
export interface MemorySettings {
  /** Config keys and values, listed in insertion order. */
  config?: Record<string, string | number | boolean>
  /** Keys that are secrets: masked until revealed. */
  secrets?: readonly string[]
  /** When set, setConfig rejects with it. */
  setError?: BackendError
  omarchy?: OmarchyState
  versions?: Versions
  onOmarchyTheme?: (cb: (theme: OmarchyTheme) => void) => () => void
}

/** What the in-memory auth fake starts from. */
export interface AuthFakeOptions {
  /** Default true: api_id and api_hash are saved. */
  configured?: boolean
  /** Default true: the GUI session is logged in. */
  authenticated?: boolean
  /** The saved phone; defaults to the test account's when configured. */
  phone?: string
  /** The code login expects; default "12345". */
  code?: string
  /** When set, login asks for this 2FA password after the code. */
  password?: string
  /** When set, login fails with ERR_TELEGRAM_RATE_LIMITED for this many seconds. */
  rateLimitSeconds?: number
  /** When set, answering a reused (previously sent) code expires it once: a
   * fresh code is sent and asked for with the resent flag. */
  expiresReusedCode?: boolean
}

export const fakeUser: AuthUser = { display_name: "Test User", phone: "+1****01", user_id: 42 }

interface PendingPrompt {
  prompt: AuthPrompt
  answer(value: string): void
}

/**
 * The in-memory login flow, mirroring the facade: code attempts up to
 * max_attempts, then the 2FA password when the account has one, prompts
 * delivered through the auth.prompt subscription and settled by
 * answerPrompt/cancelPrompt.
 */
class AuthFake {
  private configured: boolean
  private phone: string
  private authenticated: boolean
  private readonly code: string
  private readonly password?: string
  private readonly rateLimitSeconds?: number
  private readonly expiresReusedCode?: boolean
  // codePending mirrors the adapter's persisted login state: a sent code
  // stays reusable until one is accepted, so a restarted login reuses it.
  private codePending = false
  private listeners = new Set<(p: AuthPrompt) => void>()
  private pending = new Map<string, PendingPrompt>()
  private cancelHandlers = new Map<string, (e: BackendError) => void>()
  private seq = 0

  constructor(opts: AuthFakeOptions = {}) {
    this.configured = opts.configured ?? true
    // An unconfigured machine cannot be logged in.
    this.authenticated = opts.authenticated ?? this.configured
    this.phone = opts.phone ?? "+15550001"
    this.code = opts.code ?? "12345"
    this.password = opts.password
    this.rateLimitSeconds = opts.rateLimitSeconds
    this.expiresReusedCode = opts.expiresReusedCode
  }

  status(): Promise<AuthStatus> {
    return Promise.resolve({
      configured: this.configured,
      has_phone: this.phone !== "",
      authenticated: this.authenticated,
      ...(this.authenticated ? { user: fakeUser } : {}),
    })
  }

  setup(apiID: string, apiHash: string, phone: string): Promise<AuthStatus> {
    if (!/^\d+$/.test(apiID.trim())) {
      return Promise.reject({ code: "ERR_CONFIG_INVALID", category: "config", message: "invalid api_id" } satisfies BackendError)
    }
    if (apiHash.trim() === "") {
      return Promise.reject({ code: "ERR_CONFIG_INVALID", category: "config", message: "api_hash required" } satisfies BackendError)
    }
    if (phone.trim() === "") {
      return Promise.reject({ code: "ERR_CONFIG_INVALID", category: "config", message: "phone required" } satisfies BackendError)
    }
    this.configured = true
    this.phone = phone.trim()
    return this.status()
  }

  login(phone: string, forceNewCode: boolean): Promise<LoginResult> {
    // The facade saves the phone the login form collected before starting.
    if (this.phone === "") {
      if (phone.trim() === "") {
        return Promise.reject({ code: "ERR_CONFIG_MISSING", category: "config", message: "telegram.phone required" } satisfies BackendError)
      }
      this.phone = phone.trim()
    }
    if (this.rateLimitSeconds !== undefined) {
      const seconds = this.rateLimitSeconds
      return Promise.reject({
        code: "ERR_TELEGRAM_RATE_LIMITED",
        category: "api",
        message: `telegram rate limited this account: retry after ${seconds}s`,
        details: { retry_after_seconds: seconds, retry_at: new Date(Date.now() + seconds * 1000).toISOString() },
      } satisfies BackendError)
    }
    if (this.authenticated) {
      return Promise.resolve({ already_authenticated: true, user: fakeUser })
    }
    return new Promise<LoginResult>((resolve, reject) => {
      const reused = this.codePending && !forceNewCode
      this.codePending = true
      const askCode = (attempt: number, resent: boolean) => {
        this.ask({ kind: "code", attempt, max_attempts: 3, reused: reused && !resent, resent }, (value) => {
          if (reused && !resent && this.expiresReusedCode) {
            // The pending code died server-side; a fresh one is sent once.
            askCode(1, true)
            return
          }
          if (value === this.code) {
            this.codePending = false
            if (this.password !== undefined) {
              this.askPassword(1, resolve, reject)
            } else {
              this.authenticated = true
              resolve({ already_authenticated: false, user: fakeUser })
            }
          } else if (attempt >= 3) {
            this.codePending = false
            reject({ code: "ERR_AUTH_FAILED", category: "auth", message: "login code invalid after 3 attempts" } satisfies BackendError)
          } else {
            askCode(attempt + 1, resent)
          }
        }, reject)
      }
      askCode(1, false)
    })
  }

  private askPassword(attempt: number, resolve: (r: LoginResult) => void, reject: (e: BackendError) => void) {
    this.ask({ kind: "password", attempt, max_attempts: 0, reused: false, resent: false }, (value) => {
      if (value === this.password) {
        this.authenticated = true
        resolve({ already_authenticated: false, user: fakeUser })
      } else if (attempt >= 3) {
        reject({ code: "ERR_AUTH_FAILED", category: "auth", message: "2FA password invalid" } satisfies BackendError)
      } else {
        this.askPassword(attempt + 1, resolve, reject)
      }
    }, reject)
  }

  private ask(
    prompt: Omit<AuthPrompt, "id">,
    onAnswer: (value: string) => void,
    onCancel: (e: BackendError) => void,
  ) {
    const full: AuthPrompt = { ...prompt, id: `prompt-${++this.seq}` }
    this.pending.set(full.id, { prompt: full, answer: onAnswer })
    // Cancelling rejects the login the way the facade's ERR_CANCELLED does.
    this.cancelHandlers.set(full.id, onCancel)
    queueMicrotask(() => {
      for (const cb of this.listeners) cb(full)
    })
  }

  answerPrompt(id: string, value: string): Promise<void> {
    const pending = this.pending.get(id)
    if (!pending) {
      return Promise.reject({ code: "ERR_USAGE", category: "validation", message: "no pending prompt with that id" } satisfies BackendError)
    }
    this.pending.delete(id)
    this.cancelHandlers.delete(id)
    pending.answer(value)
    return Promise.resolve()
  }

  cancelPrompt(id: string): Promise<void> {
    const onCancel = this.cancelHandlers.get(id)
    this.pending.delete(id)
    this.cancelHandlers.delete(id)
    if (onCancel) {
      onCancel({ code: "ERR_CANCELLED", category: "cancelled", message: "operation cancelled" } satisfies BackendError)
    }
    return Promise.resolve()
  }

  onPrompt(cb: (prompt: AuthPrompt) => void): () => void {
    this.listeners.add(cb)
    return () => this.listeners.delete(cb)
  }

  logout(): Promise<void> {
    this.authenticated = false
    return Promise.resolve()
  }
}

export interface MemoryBackendOptions extends MemorySettings {
  auth?: AuthFakeOptions
  /** When set, every drive listing fails with this error. */
  driveError?: BackendError
}

/**
 * A backend that answers directory listings and config values from fixed
 * maps and runs the auth flow in memory. Auth defaults to a configured,
 * logged-in account so screen tests start from the main shell.
 */
export function memoryBackend(dirs: Record<string, Entry[]>, opts: MemoryBackendOptions = {}): Backend {
  const auth = new AuthFake(opts.auth)
  const secrets = new Set(opts.secrets ?? [])
  const store = new Map(Object.entries(opts.config ?? {}))
  const entryOf = (key: string): ConfigEntry => {
    const secret = secrets.has(key)
    const value = store.get(key) ?? ""
    return { key, value: secret ? "redacted" : value, secret }
  }
  return {
    drive: {
      async list(path) {
        if (opts.driveError) throw opts.driveError
        const entries = dirs[path]
        if (!entries) {
          throw {
            code: "ERR_REMOTE_NOT_FOUND",
            category: "validation",
            message: `remote path "${path}" not found`,
          } satisfies BackendError
        }
        return entries
      },
    },
    auth: {
      status: () => auth.status(),
      setup: (apiID, apiHash, phone) => auth.setup(apiID, apiHash, phone),
      login: (phone, forceNewCode) => auth.login(phone, forceNewCode),
      logout: () => auth.logout(),
      answerPrompt: (id, value) => auth.answerPrompt(id, value),
      cancelPrompt: (id) => auth.cancelPrompt(id),
      onPrompt: (cb) => auth.onPrompt(cb),
    },
    settings: {
      async listConfig() {
        return [...store.keys()].map(entryOf)
      },
      async revealSecret(key, confirmed) {
        if (!confirmed) {
          throw {
            code: "ERR_CONFIRMATION_REQUIRED",
            category: "safety",
            message: "revealing a secret requires confirmation",
          } satisfies BackendError
        }
        const e = entryOf(key)
        return { ...e, value: store.get(key) ?? "" }
      },
      async setConfig(key, value) {
        if (opts.setError) throw opts.setError
        store.set(key, value)
        return entryOf(key)
      },
      async versions() {
        return opts.versions ?? { gui: "dev", cli: "dev" }
      },
      async omarchy() {
        return opts.omarchy ?? { available: false }
      },
      onOmarchyTheme: opts.onOmarchyTheme ?? (() => () => {}),
    },
  }
}

/** A backend whose every call fails with err. */
export function failingBackend(err: BackendError): Backend {
  const fail = (): Promise<never> => Promise.reject(err)
  return {
    drive: { list: fail },
    settings: {
      listConfig: fail,
      revealSecret: fail,
      setConfig: fail,
      versions: fail,
      omarchy: fail,
      onOmarchyTheme: () => () => {},
    },
    auth: {
      status: () => Promise.reject(err),
      setup: () => Promise.reject(err),
      login: () => Promise.reject(err),
      logout: () => Promise.reject(err),
      answerPrompt: () => Promise.reject(err),
      cancelPrompt: () => Promise.reject(err),
      onPrompt: () => () => {},
    },
  }
}

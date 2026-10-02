import type {
  AuthPrompt,
  AuthStatus,
  AuthUser,
  Backend,
  BackendError,
  ConfigEntry,
  DirectoryChanged,
  Entry,
  LoginResult,
  OmarchyState,
  OmarchyTheme,
  ScanProgress,
  TreeNode,
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

function backendError(code: string, message: string): BackendError {
  return { code, category: "validation", message }
}

const confirmationRequired: BackendError = {
  code: "ERR_CONFIRMATION_REQUIRED",
  category: "safety",
  message: "requires confirmation",
}

function parentOf(path: string): string {
  const i = path.lastIndexOf("/")
  return i <= 0 ? "/" : path.slice(0, i)
}

function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1)
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
 * An in-memory backend standing in for the facade: a remote tree (files
 * and explicitly created directories) with the service's confirmation
 * gates and not-found errors, the auth flow in memory, and config values
 * from a fixed map. Auth defaults to a configured, logged-in account so
 * screen tests start from the main shell. Tests emit typed events through
 * it.
 */
export class MemoryBackend implements Backend {
  private files = new Map<string, { size: number; date: string }>()
  private dirs = new Set<string>(["/"])
  private dirChangedCbs = new Set<(e: DirectoryChanged) => void>()
  private scanProgressCbs = new Set<(e: ScanProgress) => void>()
  private scanHeld = false
  private scanResolve: (() => void) | null = null

  private readonly authFake: AuthFake
  private readonly opts: MemoryBackendOptions
  private readonly secrets: Set<string>
  private readonly store: Map<string, string | number | boolean>

  constructor(opts: MemoryBackendOptions = {}) {
    this.opts = opts
    this.authFake = new AuthFake(opts.auth)
    this.secrets = new Set(opts.secrets ?? [])
    this.store = new Map(Object.entries(opts.config ?? {}))
  }

  private entryOf(key: string): ConfigEntry {
    const secret = this.secrets.has(key)
    const value = this.store.get(key) ?? ""
    return { key, value: secret ? "redacted" : value, secret }
  }

  readonly drive: Backend["drive"] = {
    list: async (path) => {
      if (this.opts.driveError) throw this.opts.driveError
      if (!this.dirs.has(path)) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${path}" not found`)
      }
      const entries: Entry[] = []
      const seen = new Set<string>()
      for (const dir of this.dirs) {
        if (dir !== "/" && parentOf(dir) === path) {
          entries.push({ name: baseName(dir), path: dir, type: "dir", size: 0, date: "2026-01-01T00:00:00Z" })
          seen.add(dir)
        }
      }
      for (const [file, meta] of this.files) {
        if (parentOf(file) === path) {
          entries.push({ name: baseName(file), path: file, type: "file", size: meta.size, date: meta.date })
        } else if (file.startsWith(path === "/" ? "/" : path + "/")) {
          const rest = file.slice(path === "/" ? 1 : path.length + 1)
          const first = rest.split("/")[0]
          const dirPath = (path === "/" ? "" : path) + "/" + first
          if (!seen.has(dirPath)) {
            seen.add(dirPath)
            entries.push({ name: first, path: dirPath, type: "dir", size: 0, date: "2026-01-01T00:00:00Z" })
          }
        }
      }
      entries.sort((a, b) => (a.type !== b.type ? (a.type === "dir" ? -1 : 1) : a.name.localeCompare(b.name)))
      return entries
    },
    tree: async (path, maxDepth) => {
      if (!this.dirs.has(path)) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${path}" not found`)
      }
      const build = async (dir: string, depth: number): Promise<TreeNode[]> => {
        const out: TreeNode[] = []
        for (const e of await this.drive.list(dir)) {
          const node: TreeNode = { name: e.name, path: e.path, type: e.type }
          if (e.type === "dir" && (maxDepth === 0 || depth < maxDepth)) {
            node.children = await build(e.path, depth + 1)
          }
          out.push(node)
        }
        return out
      }
      return build(path, 1)
    },
    mkdir: async (path) => {
      if (this.dirs.has(path) || this.files.has(path)) {
        throw backendError("ERR_PATH_EXISTS", `path already exists: ${path}`)
      }
      if (this.files.has(parentOf(path))) {
        throw backendError("ERR_PATH_ANCESTOR_IS_FILE", `ancestor path is a file: ${parentOf(path)}`)
      }
      let cur = ""
      for (const seg of path.split("/").filter(Boolean)) {
        cur += "/" + seg
        this.dirs.add(cur)
      }
    },
    move: async (from, to, opts) => {
      if (!opts.confirm) throw confirmationRequired
      const meta = this.files.get(from)
      if (!meta) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${from}" not found`)
      }
      const dest = this.dirs.has(to) ? (to === "/" ? "" : to) + "/" + baseName(from) : to
      if (this.files.has(dest) || this.dirs.has(dest)) {
        throw backendError("ERR_PATH_EXISTS", `file exists at destination: ${dest}`)
      }
      this.files.delete(from)
      this.files.set(dest, meta)
    },
    delete: async (path, opts) => {
      if (!opts.confirm) throw confirmationRequired
      if (!this.files.delete(path)) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${path}" not found`)
      }
      return { mode: "delete", path }
    },
    share: async (path) => {
      if (!this.files.has(path) && !this.dirs.has(path)) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${path}" not found`)
      }
      return { url: "https://t.me/+fake1001", hashtag: "#td_fake1001", path, channel: "Drive" }
    },
    scan: async () => {
      if (this.scanHeld) {
        await new Promise<void>((resolve) => {
          this.scanResolve = resolve
        })
        this.scanHeld = false
        this.scanResolve = null
      }
      return { mode: "full", active: this.files.size, deleted: 0, invalid: 0, missing: 0 }
    },
  }

  readonly events: Backend["events"] = {
    onDirectoryChanged: (cb) => {
      this.dirChangedCbs.add(cb)
      return () => this.dirChangedCbs.delete(cb)
    },
    onScanProgress: (cb) => {
      this.scanProgressCbs.add(cb)
      return () => this.scanProgressCbs.delete(cb)
    },
  }

  readonly auth: Backend["auth"] = {
    status: () => this.authFake.status(),
    setup: (apiID, apiHash, phone) => this.authFake.setup(apiID, apiHash, phone),
    login: (phone, forceNewCode) => this.authFake.login(phone, forceNewCode),
    logout: () => this.authFake.logout(),
    answerPrompt: (id, value) => this.authFake.answerPrompt(id, value),
    cancelPrompt: (id) => this.authFake.cancelPrompt(id),
    onPrompt: (cb) => this.authFake.onPrompt(cb),
  }

  readonly settings: Backend["settings"] = {
    listConfig: async () => [...this.store.keys()].map((key) => this.entryOf(key)),
    revealSecret: async (key, confirmed) => {
      if (!confirmed) {
        throw {
          code: "ERR_CONFIRMATION_REQUIRED",
          category: "safety",
          message: "revealing a secret requires confirmation",
        } satisfies BackendError
      }
      const e = this.entryOf(key)
      return { ...e, value: this.store.get(key) ?? "" }
    },
    setConfig: async (key, value) => {
      if (this.opts.setError) throw this.opts.setError
      this.store.set(key, value)
      return this.entryOf(key)
    },
    versions: async () => this.opts.versions ?? { gui: "dev", cli: "dev" },
    omarchy: async () => this.opts.omarchy ?? { available: false },
    onOmarchyTheme: (cb) => (this.opts.onOmarchyTheme ?? (() => () => {}))(cb),
  }

  /** Test helper: the next scan() call stays open until finishScan(). */
  holdScan(): void {
    this.scanHeld = true
  }

  /** Test helper: completes a scan started under holdScan(). */
  finishScan(): void {
    this.scanResolve?.()
  }

  /** Test helper: emits a directory-changed event as index sync would. */
  emitDirectoryChanged(e: DirectoryChanged): void {
    for (const cb of this.dirChangedCbs) cb(e)
  }

  /** Test helper: emits a scan-progress event as a running scan would. */
  emitScanProgress(e: ScanProgress): void {
    for (const cb of this.scanProgressCbs) cb(e)
  }

  /** Test helper: adds a file without going through the facade surface. */
  putFileForTest(path: string, size: number, date: string): void {
    this.files.set(path, { size, date: date || "2026-01-01T00:00:00Z" })
    let cur = parentOf(path)
    while (cur && cur !== "/") {
      this.dirs.add(cur)
      cur = parentOf(cur)
    }
  }

  /** Test helper: adds a directory without going through the facade surface. */
  mkdirForTest(path: string): void {
    let cur = ""
    for (const seg of path.split("/").filter(Boolean)) {
      cur += "/" + seg
      this.dirs.add(cur)
    }
  }
}

/** A backend that answers from fixed directory listings, keyed by path. */
export function memoryBackend(dirs: Record<string, Entry[]>, opts: MemoryBackendOptions = {}): MemoryBackend {
  const b = new MemoryBackend(opts)
  for (const entries of Object.values(dirs)) {
    for (const e of entries) {
      if (e.type === "dir") {
        b.mkdirForTest(e.path)
      } else {
        b.putFileForTest(e.path, e.size, e.date)
      }
    }
  }
  return b
}

/** A backend whose every call fails with err. */
export function failingBackend(err: BackendError): Backend {
  const fail = (): Promise<never> => Promise.reject(err)
  return {
    drive: {
      list: fail,
      tree: fail,
      mkdir: fail,
      move: fail,
      delete: fail,
      share: fail,
      scan: fail,
    },
    events: {
      onDirectoryChanged: () => () => {},
      onScanProgress: () => () => {},
    },
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

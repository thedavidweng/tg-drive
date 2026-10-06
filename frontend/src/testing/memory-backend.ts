import type {
  AdoptOutcome,
  AuthPrompt,
  AuthStatus,
  AuthUser,
  Backend,
  BackendError,
  BindResult,
  ChannelChoice,
  ChannelsChanged,
  ChannelStatus,
  ConfigEntry,
  DirectoryChanged,
  DoctorReport,
  DownloadOptions,
  Entry,
  FilesDropped,
  ImportOptions,
  ImportOutcome,
  ImportPrompt,
  ItemEvent,
  LoginResult,
  OmarchyState,
  OmarchyTheme,
  PathCodecReport,
  PreviewDescriptor,
  RepairOutcome,
  ScanProgress,
  Transfer,
  TransferRemoved,
  TreeNode,
  UploadOptions,
  Versions,
} from "@/backend"
import { isTerminalStage } from "@/backend"

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

function joinRemote(dir: string, name: string): string {
  return (dir === "" || dir === "/" ? "" : dir) + "/" + name
}

function joinLocal(dir: string, name: string): string {
  return dir.replace(/\/+$/, "") + "/" + name
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

/** One drive channel the in-memory backend starts with. */
export interface MemoryChannel {
  id: string
  title: string
  /** The linked discussion group's title; absent means not linked. */
  discussion?: string
  /** The permissions the account lacks on the channel. */
  denied?: ("upload" | "delete" | "edit" | "invite")[]
  probeFailed?: boolean
}

/** How the in-memory Transfers service answers. */
export interface MemoryTransfers {
  /** The answers of the native dialogs; empty is the dialog cancelled. */
  picks?: { files?: string[]; dir?: string }
  /** When set, the dialogs reject with it (no dialog connected). */
  pickError?: BackendError
  /** When set, upload and download reject with it. */
  submitError?: BackendError
  /**
   * The account's per-file upload limit planUpload reports; defaults to
   * the fake's 2 GiB free tier.
   */
  uploadLimitBytes?: number
  /** Local file sizes planUpload reports, by path; unknown paths are 0. */
  fileSizes?: Record<string, number>
  /** Paths that are directories in the plan (the facade stats them). */
  dirs?: string[]
}

/** How the in-memory import fake answers. */
export interface MemoryImport {
  /** The saved-chat plan preview returns; run returns it with dry_run off. */
  plan?: ImportOutcome
  /** When > 0 and the call carries no photos_as, the call emits an
   * import.prompt and waits for answerPrompt/cancelPrompt, like the facade. */
  photoCount?: number
}

/** How the in-memory maintenance fake answers. */
export interface MemoryMaintenance {
  adoptPlan?: AdoptOutcome
  adoptOutcome?: AdoptOutcome
  /** Repair outcomes by mode; an unlisted mode gets an empty outcome. */
  repair?: Partial<Record<string, RepairOutcome>>
  doctor?: DoctorReport
  pathCodec?: PathCodecReport
}

export interface MemoryBackendOptions extends MemorySettings {
  auth?: AuthFakeOptions
  transfers?: MemoryTransfers
  /** When set, every drive listing fails with this error. */
  driveError?: BackendError
  /**
   * The bound drive channels; the first is active. Default: one linked
   * "Drive" channel. Pass [] for a machine with nothing bound.
   */
  channels?: MemoryChannel[]
  /**
   * Telegram channels the account owns beyond the bound ones, offered as
   * bind choices. Default: one unbound "Archive".
   */
  telegramChannels?: { id: string; title: string }[]
  import?: MemoryImport
  maintenance?: MemoryMaintenance
}

const emptyImportOutcome: ImportOutcome = {
  dry_run: true,
  into: "/saved",
  history_complete: true,
  imported: 0,
  skipped: 0,
  failed: 0,
  duplicates: 0,
  captions_merged: 0,
  sources_deleted: 0,
  photos: 0,
  items: [],
}

const emptyAdoptOutcome: AdoptOutcome = { dry_run: true, adopted: 0, skipped: 0, failed: 0, deleted: 0, items: [] }

const defaultDoctorReport: DoctorReport = {
  checks: [
    { name: "auth", status: "pass" },
    { name: "channel", status: "pass" },
    { name: "delete", status: "pass" },
    { name: "edit_old_caption", status: "pass" },
    { name: "history_read", status: "pass" },
    { name: "invite_link", status: "pass" },
    { name: "upload", status: "pass" },
  ],
  max_upload_bytes: 2147483648,
}

const defaultPathCodecReport: PathCodecReport = {
  fixed_vectors: "pass",
  db_check: "pass",
  db_rows: 0,
  corrupt_rows: 0,
}

/** The empty outcome of a repair mode the test did not configure. */
function emptyRepairOutcome(mode: string): RepairOutcome {
  switch (mode) {
    case "captions":
      return { mode, captions: { dry_run: false, total: 0, planned: 0, cleaned: 0, skipped: 0, failed: 0, items: [] } }
    case "hash":
      return { mode, hash: { total: 0, backfilled: 0, failed: 0, items: [] } }
    case "path":
      return { mode, path: { repaired: "" } }
    case "orphaned":
      return { mode, orphaned: { repaired: 0, deleted: 0, invalid: 0 } }
    case "scan_errors":
      return { mode, scan_errors: { resolved: 0, pending: 0 } }
    default:
      return { mode, pending: { repaired: 0, invalid: 0, orphaned: 0, skipped: 0, locks_cleared: 0 } }
  }
}

/** The in-memory import photo prompt, mirroring the facade's prompt seam. */
class ImportFake {
  private readonly opts: MemoryImport
  private listeners = new Set<(p: ImportPrompt) => void>()
  private pending = new Map<string, (a: { value?: string; err?: BackendError }) => void>()
  private seq = 0

  constructor(opts: MemoryImport = {}) {
    this.opts = opts
  }

  /** Resolves the photo choice, prompting when the plan holds photos and no
   * choice was given. */
  private photoChoice(opts: ImportOptions, photos: number): Promise<string> {
    if (opts.photos_as) return Promise.resolve(opts.photos_as)
    if (photos <= 0) return Promise.resolve("")
    return new Promise((resolve, reject) => {
      const prompt: ImportPrompt = { id: `import-prompt-${++this.seq}`, kind: "photos", photos }
      this.pending.set(prompt.id, (a) => {
        if (a.err) {
          reject(a.err)
        } else if (a.value === "document" || a.value === "photo") {
          resolve(a.value)
        } else {
          reject({ code: "ERR_USAGE", category: "validation", message: `unknown photo presentation "${a.value}"` } satisfies BackendError)
        }
      })
      queueMicrotask(() => {
        for (const cb of this.listeners) cb(prompt)
      })
    })
  }

  async call(dryRun: boolean, opts: ImportOptions): Promise<ImportOutcome> {
    // The service's gates: a real run needs confirmation, delete-source
    // needs it even on a dry run.
    if (opts.delete_source && !opts.confirm) throw confirmationRequired
    if (!dryRun && !opts.confirm) throw confirmationRequired
    const plan = this.opts.plan ?? emptyImportOutcome
    const photosAs = await this.photoChoice(opts, this.opts.photoCount ?? plan.photos)
    const outcome: ImportOutcome = { ...plan, dry_run: dryRun, ...(photosAs ? { photos_as: photosAs } : {}) }
    if (!dryRun) {
      // One import.item event per planned item, tally included.
      let completed = 0
      let skipped = 0
      let failed = 0
      for (const item of outcome.items ?? []) {
        const status = item.action === "fail" ? "failed" : item.action === "skip" ? "skipped" : "completed"
        if (status === "completed") completed++
        else if (status === "skipped") skipped++
        else failed++
        this.emitItem({
          path: item.path ?? "",
          message_id: item.message_id,
          status,
          ...(item.error ? { error: item.error } : {}),
          completed,
          skipped,
          failed,
        })
      }
    }
    return outcome
  }

  private itemCbs = new Set<(e: ItemEvent) => void>()

  private emitItem(e: ItemEvent) {
    for (const cb of this.itemCbs) cb(e)
  }

  onItem(cb: (e: ItemEvent) => void): () => void {
    this.itemCbs.add(cb)
    return () => this.itemCbs.delete(cb)
  }

  answerPrompt(id: string, choice: string): Promise<void> {
    const settle = this.pending.get(id)
    if (!settle) {
      return Promise.reject({ code: "ERR_USAGE", category: "validation", message: "no pending prompt with that id" } satisfies BackendError)
    }
    this.pending.delete(id)
    settle({ value: choice })
    return Promise.resolve()
  }

  cancelPrompt(id: string): Promise<void> {
    const settle = this.pending.get(id)
    this.pending.delete(id)
    settle?.({ err: { code: "ERR_CANCELLED", category: "cancelled", message: "operation cancelled" } satisfies BackendError })
    return Promise.resolve()
  }

  onPrompt(cb: (p: ImportPrompt) => void): () => void {
    this.listeners.add(cb)
    return () => this.listeners.delete(cb)
  }
}

/**
 * An in-memory backend standing in for the facade: a remote tree (files
 * and explicitly created directories) with the service's confirmation
 * gates and not-found errors, the auth flow in memory, and config values
 * from a fixed map. Auth defaults to a configured, logged-in account so
 * screen tests start from the main shell. Tests emit typed events through
 * it.
 */
interface ChannelTree {
  files: Map<string, { size: number; date: string }>
  dirs: Set<string>
  previews: Map<string, PreviewContent>
}

function emptyTree(): ChannelTree {
  return { files: new Map(), dirs: new Set(["/"]), previews: new Map() }
}

/**
 * What the in-memory drive.preview serves for a file: bytes it turns into a
 * data URL, or a ready URL (the demo's sample hosts). A file without
 * content previews with an empty URL, which the surface shows as the
 * fallback.
 */
export interface PreviewContent {
  /** The indexed MIME type; default: guessed from the extension. */
  mime?: string
  data?: string | Uint8Array
  url?: string
  /** Default true: the URL answers byte ranges. */
  ranges?: boolean
}

// The handful of types the screens' tests and the demo need; the real
// index stores the MIME type detected at upload.
const mimeByExtension: Record<string, string> = {
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  png: "image/png",
  gif: "image/gif",
  webp: "image/webp",
  svg: "image/svg+xml",
  mp4: "video/mp4",
  mov: "video/quicktime",
  mkv: "video/x-matroska",
  webm: "video/webm",
  mp3: "audio/mpeg",
  flac: "audio/flac",
  ogg: "audio/ogg",
  wav: "audio/wav",
  pdf: "application/pdf",
  txt: "text/plain; charset=utf-8",
  md: "text/markdown; charset=utf-8",
  srt: "application/x-subrip",
  json: "application/json",
  zip: "application/zip",
}

function guessMIME(path: string): string {
  const i = path.lastIndexOf(".")
  return i < 0 ? "application/octet-stream" : (mimeByExtension[path.slice(i + 1).toLowerCase()] ?? "application/octet-stream")
}

function dataURL(mime: string, data: string | Uint8Array): string {
  const bytes = typeof data === "string" ? new TextEncoder().encode(data) : data
  let bin = ""
  for (const b of bytes) bin += String.fromCharCode(b)
  return `data:${mime.split(";")[0]};base64,${btoa(bin)}`
}

export class MemoryBackend implements Backend {
  private dirChangedCbs = new Set<(e: DirectoryChanged) => void>()
  private scanProgressCbs = new Set<(e: ScanProgress) => void>()
  private scanHeld = false
  private scanResolve: (() => void) | null = null
  private transferSeq = 0
  private transferOrder: string[] = []
  private transfersById = new Map<string, Transfer>()
  private transferStageCbs = new Set<(t: Transfer) => void>()
  private transferProgressCbs = new Set<(t: Transfer) => void>()
  private transferRemovedCbs = new Set<(e: TransferRemoved) => void>()
  private filesDroppedCbs = new Set<(e: FilesDropped) => void>()
  private channelsChangedCbs = new Set<(e: ChannelsChanged) => void>()

  /** Test-visible record of the transfers calls the screens made. */
  readonly uploads: { paths: string[]; dest: string; opts: UploadOptions }[] = []
  readonly downloads: { remotePath: string; destDir: string; opts: DownloadOptions }[] = []
  readonly cancelled: string[] = []
  readonly retried: string[] = []
  /** Test-visible record of the paths drive.preview was asked for. */
  readonly previewed: string[] = []
  private readonly previewErrors = new Map<string, BackendError>()

  private readonly authFake: AuthFake
  private readonly importFake: ImportFake
  private readonly opts: MemoryBackendOptions
  private readonly secrets: Set<string>
  private readonly store: Map<string, string | number | boolean>

  /** The bound channels and their trees; drive calls work on the active one. */
  private readonly bound: MemoryChannel[]
  private activeID: string
  private readonly trees = new Map<string, ChannelTree>()
  private readonly tgChannels: { id: string; title: string }[]
  private nextChannelID = 9000

  constructor(opts: MemoryBackendOptions = {}) {
    this.opts = opts
    this.authFake = new AuthFake(opts.auth)
    this.importFake = new ImportFake(opts.import)
    this.secrets = new Set(opts.secrets ?? [])
    this.store = new Map(Object.entries(opts.config ?? {}))
    this.bound = (opts.channels ?? [{ id: "1001", title: "Drive", discussion: "Drive Discussion" }]).map((c) => ({
      ...c,
    }))
    this.activeID = this.bound[0]?.id ?? ""
    for (const ch of this.bound) {
      this.trees.set(ch.id, emptyTree())
    }
    this.tgChannels = opts.telegramChannels ?? [{ id: "2001", title: "Archive" }]
  }

  /** The active channel's tree; rejects the way an unbound App would. */
  private tree(): ChannelTree {
    const tree = this.trees.get(this.activeID)
    if (!tree) {
      throw backendError(
        "ERR_CHANNEL_NOT_FOUND",
        "no channel bound in this database; run: td init <local-root> --create-channel",
      )
    }
    return tree
  }

  private activeChannel(): MemoryChannel {
    const ch = this.bound.find((c) => c.id === this.activeID)
    if (!ch) throw backendError("ERR_CHANNEL_NOT_FOUND", "no channel bound")
    return ch
  }

  private entryOf(key: string): ConfigEntry {
    const secret = this.secrets.has(key)
    const value = this.store.get(key) ?? ""
    return { key, value: secret ? "redacted" : value, secret }
  }

  readonly drive: Backend["drive"] = {
    list: async (path) => {
      if (this.opts.driveError) throw this.opts.driveError
      const { files, dirs } = this.tree()
      if (!dirs.has(path)) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${path}" not found`)
      }
      const entries: Entry[] = []
      const seen = new Set<string>()
      for (const dir of dirs) {
        if (dir !== "/" && parentOf(dir) === path) {
          entries.push({ name: baseName(dir), path: dir, type: "dir", size: 0, date: "2026-01-01T00:00:00Z" })
          seen.add(dir)
        }
      }
      for (const [file, meta] of files) {
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
      if (!this.tree().dirs.has(path)) {
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
      const { files, dirs } = this.tree()
      if (dirs.has(path) || files.has(path)) {
        throw backendError("ERR_PATH_EXISTS", `path already exists: ${path}`)
      }
      if (files.has(parentOf(path))) {
        throw backendError("ERR_PATH_ANCESTOR_IS_FILE", `ancestor path is a file: ${parentOf(path)}`)
      }
      let cur = ""
      for (const seg of path.split("/").filter(Boolean)) {
        cur += "/" + seg
        dirs.add(cur)
      }
    },
    move: async (from, to, opts) => {
      if (!opts.confirm) throw confirmationRequired
      const { files, dirs } = this.tree()
      const meta = files.get(from)
      if (!meta) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${from}" not found`)
      }
      const dest = dirs.has(to) ? (to === "/" ? "" : to) + "/" + baseName(from) : to
      if (files.has(dest) || dirs.has(dest)) {
        throw backendError("ERR_PATH_EXISTS", `file exists at destination: ${dest}`)
      }
      files.delete(from)
      files.set(dest, meta)
      const { previews } = this.tree()
      const content = previews.get(from)
      previews.delete(from)
      if (content) previews.set(dest, content)
    },
    delete: async (path, opts) => {
      if (!opts.confirm) throw confirmationRequired
      this.tree().previews.delete(path)
      if (!this.tree().files.delete(path)) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${path}" not found`)
      }
      return { mode: "delete", path }
    },
    share: async (path) => {
      const { files, dirs } = this.tree()
      if (!files.has(path) && !dirs.has(path)) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${path}" not found`)
      }
      return { url: "https://t.me/+fake1001", hashtag: "#td_fake1001", path, channel: this.activeChannel().title }
    },
    scan: async () => {
      if (this.scanHeld) {
        await new Promise<void>((resolve) => {
          this.scanResolve = resolve
        })
        this.scanHeld = false
        this.scanResolve = null
      }
      return { mode: "full", active: this.tree().files.size, deleted: 0, invalid: 0, missing: 0 }
    },
    preview: async (path) => {
      this.previewed.push(path)
      const { files, previews } = this.tree()
      const meta = files.get(path)
      if (!meta) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${path}" not found`)
      }
      const err = this.previewErrors.get(path)
      if (err) throw err
      const content = previews.get(path)
      const mime = content?.mime ?? guessMIME(path)
      const url = content?.url ?? (content?.data !== undefined ? dataURL(mime, content.data) : "")
      const ranges = url !== "" && (content?.ranges ?? true)
      const descriptor: PreviewDescriptor = {
        name: baseName(path),
        path,
        mime,
        size: meta.size,
        date: meta.date,
        capabilities: { ranges, media_size: ranges ? meta.size : -1 },
        url,
      }
      return descriptor
    },
  }

  readonly channels: Backend["channels"] = {
    list: async () =>
      this.bound.map((ch) => ({
        channel_id: ch.id,
        title: ch.title,
        local_root: `/data/drives/${ch.title}`,
        active: ch.id === this.activeID,
      })),
    status: async () => this.activeStatus(),
    choices: async () => {
      const seen = new Set<string>()
      const out: ChannelChoice[] = []
      // Bound channels are choices too (marked), the way InitChoices lists
      // every channel the account owns.
      for (const ch of [
        ...this.tgChannels,
        ...this.bound.map((b) => ({ id: b.id, title: b.title })),
      ]) {
        if (seen.has(ch.id)) continue
        seen.add(ch.id)
        out.push({ channel_id: ch.id, title: ch.title, bound: this.bound.some((b) => b.id === ch.id) })
      }
      return { channels: out, default_title: "Drive" }
    },
    bind: async (req): Promise<BindResult> => {
      const channelID = req.channel_id.trim()
      if (channelID !== "") {
        const existing = this.bound.find((c) => c.id === channelID)
        const choice = this.tgChannels.find((c) => c.id === channelID)
        const title = (existing ?? choice)?.title
        if (title === undefined) {
          throw backendError("ERR_CHANNEL_NOT_FOUND", `channel not found: ${channelID}`)
        }
        if (!existing) {
          this.bound.push({ id: channelID, title, discussion: `${title} Discussion` })
          this.trees.set(channelID, emptyTree())
        }
        this.activeID = channelID
        return { channel_id: channelID, title, created: false, already_initialized: false, indexed_files: 0 }
      }
      const title = req.title.trim() || "Drive"
      // Like the init use case: creating for a root the GUI already bound
      // reports the existing binding instead of minting another channel.
      const existing = this.bound.find((c) => c.title === title)
      if (existing) {
        this.activeID = existing.id
        return { channel_id: existing.id, title: existing.title, created: true, already_initialized: true }
      }
      const id = String(this.nextChannelID++)
      this.bound.push({ id, title, discussion: `${title} Discussion` })
      this.trees.set(id, emptyTree())
      this.activeID = id
      return { channel_id: id, title, created: true, already_initialized: false, indexed_files: 0 }
    },
    select: async (channelID) => {
      if (!this.bound.some((c) => c.id === channelID)) {
        throw backendError("ERR_CHANNEL_NOT_FOUND", `channel not bound: ${channelID}`)
      }
      this.activeID = channelID
      return this.activeStatus()
    },
    linkDiscussion: async () => {
      const ch = this.activeChannel()
      ch.discussion = ch.discussion ?? `${ch.title} Discussion`
      return { discussion_channel_id: `9${ch.id}`, discussion_title: ch.discussion }
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
    onTransferStage: (cb) => {
      this.transferStageCbs.add(cb)
      return () => this.transferStageCbs.delete(cb)
    },
    onTransferProgress: (cb) => {
      this.transferProgressCbs.add(cb)
      return () => this.transferProgressCbs.delete(cb)
    },
    onTransferRemoved: (cb) => {
      this.transferRemovedCbs.add(cb)
      return () => this.transferRemovedCbs.delete(cb)
    },
    onFilesDropped: (cb) => {
      this.filesDroppedCbs.add(cb)
      return () => this.filesDroppedCbs.delete(cb)
    },
    onChannelsChanged: (cb) => {
      this.channelsChangedCbs.add(cb)
      return () => this.channelsChangedCbs.delete(cb)
    },
  }

  readonly transfers: Backend["transfers"] = {
    list: async () => {
      const all = [...this.transferOrder]
        .reverse()
        .map((id) => this.transfersById.get(id)!)
      return {
        active: all.filter((t) => !isTerminalStage(t.stage)),
        history: all.filter((t) => isTerminalStage(t.stage)),
      }
    },
    upload: async (paths, dest, opts) => {
      if (this.opts.transfers?.submitError) throw this.opts.transfers.submitError
      const policy = opts.policy || "fail"
      if (!["fail", "skip", "replace", "rename"].includes(policy)) {
        throw backendError("ERR_USAGE", `unknown conflict policy "${opts.policy}" (want skip, replace, rename, or fail)`)
      }
      // The replace gate, mirroring the facade's fail-fast Validate.
      if (policy === "replace" && !opts.confirm_replace) {
        throw {
          code: "ERR_CONFIRMATION_REQUIRED",
          category: "safety",
          message: "replacing an existing remote file requires confirmation",
        } satisfies BackendError
      }
      // Several files are one album, and the service rejects album replace.
      if (paths.length > 1 && policy === "replace") {
        throw backendError("ERR_USAGE", "album uploads cannot replace existing files")
      }
      this.uploads.push({ paths, dest, opts })
      const kind = paths.length > 1 ? "album_upload" : "upload"
      const destPath = paths.length === 1 ? joinRemote(dest, baseName(paths[0])) : dest
      const t = this.makeTransfer(kind, paths.join("\n"), destPath, paths.length)
      queueMicrotask(() => this.completeTransfer(t.id))
      return [t.id]
    },
    download: async (remotePath, destDir, opts) => {
      if (this.opts.transfers?.submitError) throw this.opts.transfers.submitError
      const policy = opts.policy || "fail"
      if (!["fail", "skip", "replace", "rename"].includes(policy)) {
        throw backendError("ERR_USAGE", `unknown conflict policy "${opts.policy}" (want skip, replace, rename, or fail)`)
      }
      const tree = this.tree()
      if (!tree.files.has(remotePath) && !tree.dirs.has(remotePath)) {
        throw backendError("ERR_REMOTE_NOT_FOUND", `remote path "${remotePath}" not found`)
      }
      this.downloads.push({ remotePath, destDir, opts })
      const kind = tree.files.has(remotePath) ? "download" : "recursive_download"
      const t = this.makeTransfer(kind, remotePath, joinLocal(destDir, baseName(remotePath)), 1)
      queueMicrotask(() => this.completeTransfer(t.id))
      return t.id
    },
    planUpload: async (paths, dest, policy) => {
      if (paths.length === 0) throw backendError("ERR_USAGE", "nothing to upload")
      const p = policy || "fail"
      if (!["fail", "skip", "replace", "rename"].includes(p)) {
        throw backendError("ERR_USAGE", `unknown conflict policy "${policy}" (want skip, replace, rename, or fail)`)
      }
      const limit = this.opts.transfers?.uploadLimitBytes ?? 2147483648
      const dirs = new Set(this.opts.transfers?.dirs ?? [])
      return {
        local: paths,
        remote: dest,
        policy: p,
        ...(p === "replace" ? { would_replace: dest } : {}),
        upload_limit_bytes: limit,
        files: paths.map((local) => {
          if (dirs.has(local)) return { local, size: 0, dir: true }
          const size = this.opts.transfers?.fileSizes?.[local] ?? 0
          return { local, size, ...(size > limit ? { over_limit: true } : {}) }
        }),
      }
    },
    cancel: async (id) => {
      const t = this.transfersById.get(id)
      if (!t) throw backendError("ERR_TRANSFER_NOT_FOUND", `no transfer with id ${id}`)
      if (isTerminalStage(t.stage)) throw backendError("ERR_USAGE", `transfer already ended (${t.stage}): ${id}`)
      this.cancelled.push(id)
      const next = { ...t, cancel_requested: true }
      this.transfersById.set(id, next)
      this.emitTransferProgress(next)
      queueMicrotask(() => this.setStage(id, "cancelled"))
      return next
    },
    retry: async (id) => {
      const t = this.transfersById.get(id)
      if (!t) throw backendError("ERR_TRANSFER_NOT_FOUND", `no transfer with id ${id}`)
      if (!isTerminalStage(t.stage)) throw backendError("ERR_USAGE", `transfer is still ${t.stage}: ${id}`)
      if (t.stage === "completed") throw backendError("ERR_USAGE", `transfer already completed: ${id}`)
      this.retried.push(id)
      const next: Transfer = {
        ...t,
        stage: "queued",
        cancel_requested: false,
        bytes_done: 0,
        items_done: 0,
        items_failed: 0,
        error_code: undefined,
        error_message: undefined,
        finished_at: undefined,
      }
      this.transfersById.set(id, next)
      this.emitTransferStage(next)
      queueMicrotask(() => this.completeTransfer(id))
      return next
    },
    clearFinished: async () => {
      const cleared = [...this.transfersById.values()].filter((t) => isTerminalStage(t.stage))
      for (const t of cleared) {
        this.transfersById.delete(t.id)
        this.transferOrder = this.transferOrder.filter((id) => id !== t.id)
        this.emitTransferRemoved({ id: t.id })
      }
      return cleared.length
    },
    pickFiles: async () => {
      if (this.opts.transfers?.pickError) throw this.opts.transfers.pickError
      return this.opts.transfers?.picks?.files ?? []
    },
    pickDirectory: async () => {
      if (this.opts.transfers?.pickError) throw this.opts.transfers.pickError
      return this.opts.transfers?.picks?.dir ?? ""
    },
  }

  private makeTransfer(kind: string, source: string, dest: string, itemsTotal: number): Transfer {
    const now = new Date().toISOString()
    const t: Transfer = {
      id: `transfer-${++this.transferSeq}`,
      kind,
      stage: "queued",
      channel: "1001",
      source,
      dest,
      bytes_done: 0,
      bytes_total: 0,
      items_done: 0,
      items_total: itemsTotal,
      front_end: "gui",
      cancel_requested: false,
      created_at: now,
      updated_at: now,
    }
    this.transfersById.set(t.id, t)
    this.transferOrder.push(t.id)
    queueMicrotask(() => this.emitTransferStage(t))
    return t
  }

  private setStage(id: string, stage: string): void {
    const t = this.transfersById.get(id)
    if (!t) return
    const next: Transfer = { ...t, stage, updated_at: new Date().toISOString() }
    if (isTerminalStage(stage)) next.finished_at = next.updated_at
    this.transfersById.set(id, next)
    this.emitTransferStage(next)
  }

  private completeTransfer(id: string): void {
    const t = this.transfersById.get(id)
    if (!t || isTerminalStage(t.stage)) return
    this.transfersById.set(id, { ...t, items_done: t.items_total })
    this.setStage(id, "completed")
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

  readonly import: Backend["import"] = {
    preview: (opts) => this.importFake.call(true, opts),
    run: (opts) => this.importFake.call(false, opts),
    answerPrompt: (id, choice) => this.importFake.answerPrompt(id, choice),
    cancelPrompt: (id) => this.importFake.cancelPrompt(id),
    onPrompt: (cb) => this.importFake.onPrompt(cb),
    onItem: (cb) => this.importFake.onItem(cb),
  }

  readonly maintenance: Backend["maintenance"] = {
    previewAdopt: async () => this.opts.maintenance?.adoptPlan ?? emptyAdoptOutcome,
    adopt: async (opts) => {
      if (!opts.confirm) throw confirmationRequired
      return this.opts.maintenance?.adoptOutcome ?? { ...emptyAdoptOutcome, dry_run: false }
    },
    repair: async (opts) => {
      const known = ["pending", "orphaned", "scan_errors", "hash", "captions", "path"]
      if (!known.includes(opts.mode)) {
        throw { code: "ERR_USAGE", category: "validation", message: `unknown repair mode "${opts.mode}"` } satisfies BackendError
      }
      if (opts.mode === "orphaned" && opts.delete_orphaned && !opts.confirm) throw confirmationRequired
      return this.opts.maintenance?.repair?.[opts.mode] ?? emptyRepairOutcome(opts.mode)
    },
    doctor: async () => this.opts.maintenance?.doctor ?? defaultDoctorReport,
    pathCodecDoctor: async () => this.opts.maintenance?.pathCodec ?? defaultPathCodecReport,
    onRepairItem: () => () => {},
  }

  /** Test helper: the next scan() call stays open until finishScan(). */
  holdScan(): void {
    this.scanHeld = true
  }

  /** Test helper: completes a scan started under holdScan(). */
  finishScan(): void {
    this.scanResolve?.()
  }

  /**
   * Test helper: binds a channel the way another front end (td init) would,
   * and emits the channels-changed event index sync reports it with.
   */
  async bindExternally(ch: MemoryChannel): Promise<void> {
    this.bound.push({ ...ch })
    this.trees.set(ch.id, emptyTree())
    const channels = await this.channels.list()
    for (const cb of this.channelsChangedCbs) cb({ channels })
  }

  /** Test helper: emits a directory-changed event as index sync would. */
  emitDirectoryChanged(e: DirectoryChanged): void {
    for (const cb of this.dirChangedCbs) cb(e)
  }

  /** Test helper: emits a scan-progress event as a running scan would. */
  emitScanProgress(e: ScanProgress): void {
    for (const cb of this.scanProgressCbs) cb(e)
  }

  /** Test helper: emits a transfer-stage event as the facade would. */
  emitTransferStage(t: Transfer): void {
    for (const cb of this.transferStageCbs) cb(t)
  }

  /** Test helper: emits a transfer-progress event as the facade would. */
  emitTransferProgress(t: Transfer): void {
    for (const cb of this.transferProgressCbs) cb(t)
  }

  /** Test helper: emits a transfer-removed event as the facade would. */
  emitTransferRemoved(e: TransferRemoved): void {
    for (const cb of this.transferRemovedCbs) cb(e)
  }

  /** Test helper: emits a files-dropped event as a native drop would. */
  emitFilesDropped(paths: string[]): void {
    const e: FilesDropped = { paths }
    for (const cb of this.filesDroppedCbs) cb(e)
  }

  /** Test helper: registers a Transfer the way another front end's would
   * appear in the index (e.g. a CLI upload, front_end "cli"). */
  putTransferForTest(t: Partial<Transfer> & { id: string }): Transfer {
    const now = new Date().toISOString()
    const full: Transfer = {
      kind: "upload",
      stage: "queued",
      channel: "1001",
      source: "",
      dest: "",
      bytes_done: 0,
      bytes_total: 0,
      items_done: 0,
      items_total: 1,
      front_end: "cli",
      cancel_requested: false,
      created_at: now,
      updated_at: now,
      ...t,
    }
    this.transfersById.set(full.id, full)
    if (!this.transferOrder.includes(full.id)) this.transferOrder.push(full.id)
    return full
  }

  /** Test helper: adds a file without going through the facade surface. */
  putFileForTest(path: string, size: number, date: string, channelID?: string): void {
    const tree = this.trees.get(channelID ?? this.activeID)
    if (!tree) throw new Error(`no channel ${channelID ?? this.activeID} in the test backend`)
    tree.files.set(path, { size, date: date || "2026-01-01T00:00:00Z" })
    let cur = parentOf(path)
    while (cur && cur !== "/") {
      tree.dirs.add(cur)
      cur = parentOf(cur)
    }
  }

  /**
   * Test and demo helper: what drive.preview serves for a file. Adds the
   * file (sized by its data) when it is not in the tree yet.
   */
  putPreviewForTest(path: string, content: PreviewContent, channelID?: string): void {
    const tree = this.trees.get(channelID ?? this.activeID)
    if (!tree) throw new Error(`no channel ${channelID ?? this.activeID} in the test backend`)
    if (!tree.files.has(path)) {
      const size = typeof content.data === "string" ? new TextEncoder().encode(content.data).length : (content.data?.length ?? 0)
      this.putFileForTest(path, size, "", channelID)
    }
    tree.previews.set(path, content)
  }

  /** Test helper: drive.preview of path rejects with err. */
  failPreviewForTest(path: string, err: BackendError): void {
    this.previewErrors.set(path, err)
  }

  /** Test helper: adds a directory without going through the facade surface. */
  mkdirForTest(path: string, channelID?: string): void {
    const tree = this.trees.get(channelID ?? this.activeID)
    if (!tree) throw new Error(`no channel ${channelID ?? this.activeID} in the test backend`)
    let cur = ""
    for (const seg of path.split("/").filter(Boolean)) {
      cur += "/" + seg
      tree.dirs.add(cur)
    }
  }

  private activeStatus(): ChannelStatus {
    const ch = this.activeChannel()
    const files = this.trees.get(ch.id)?.files.size ?? 0
    return {
      channel_id: ch.id,
      title: ch.title,
      local_root: `/data/drives/${ch.title}`,
      files,
      discussion_linked: ch.discussion !== undefined,
      ...(ch.discussion !== undefined ? { discussion_title: ch.discussion } : {}),
      upload_limit_bytes: 2147483648,
      ...(!ch.probeFailed && { capabilities: {
        can_upload: !ch.denied?.includes("upload"),
        can_delete: !ch.denied?.includes("delete"),
        can_edit_captions: !ch.denied?.includes("edit"),
        can_invite: !ch.denied?.includes("invite"),
      } }),
      last_scan_at: "2026-01-05T09:00:00Z",
      last_full_scan_at: "2026-01-05T09:00:00Z",
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
      preview: fail,
    },
    events: {
      onDirectoryChanged: () => () => {},
      onScanProgress: () => () => {},
      onTransferStage: () => () => {},
      onTransferProgress: () => () => {},
      onTransferRemoved: () => () => {},
      onFilesDropped: () => () => {},
      onChannelsChanged: () => () => {},
    },
    transfers: {
      list: fail,
      upload: fail,
      download: fail,
      planUpload: fail,
      cancel: fail,
      retry: fail,
      clearFinished: fail,
      pickFiles: fail,
      pickDirectory: fail,
    },
    channels: {
      list: fail,
      status: fail,
      choices: fail,
      bind: fail,
      select: fail,
      linkDiscussion: fail,
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
    import: {
      preview: fail,
      run: fail,
      answerPrompt: () => Promise.reject(err),
      cancelPrompt: () => Promise.reject(err),
      onPrompt: () => () => {},
      onItem: () => () => {},
    },
    maintenance: {
      previewAdopt: fail,
      adopt: fail,
      repair: fail,
      doctor: fail,
      pathCodecDoctor: fail,
      onRepairItem: () => () => {},
    },
  }
}

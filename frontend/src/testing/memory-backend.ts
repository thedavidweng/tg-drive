import type {
  Backend,
  BackendError,
  ConfigEntry,
  Entry,
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

/** A backend that answers from fixed directory listings and config values. */
export function memoryBackend(
  dirs: Record<string, Entry[]>,
  settings: MemorySettings = {},
): Backend {
  const secrets = new Set(settings.secrets ?? [])
  const store = new Map(Object.entries(settings.config ?? {}))
  const entryOf = (key: string): ConfigEntry => {
    const secret = secrets.has(key)
    const value = store.get(key) ?? ""
    return { key, value: secret ? "redacted" : value, secret }
  }
  return {
    drive: {
      async list(path) {
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
        if (settings.setError) throw settings.setError
        store.set(key, value)
        return entryOf(key)
      },
      async versions() {
        return settings.versions ?? { gui: "dev", cli: "dev" }
      },
      async omarchy() {
        return settings.omarchy ?? { available: false }
      },
      onOmarchyTheme: settings.onOmarchyTheme ?? (() => () => {}),
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
  }
}

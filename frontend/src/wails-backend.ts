import { Events } from "@wailsio/runtime"

import { Drive, Settings } from "../bindings/github.com/thedavidweng/tg-drive-cli/internal/gui"
import type { Backend, BackendError } from "@/backend"

/**
 * The Wails runtime rejects a failed call with a RuntimeError whose cause is
 * the Go error marshalled as JSON, here internal/gui.Error.
 */
function toBackendError(err: unknown): BackendError {
  const cause = (err as { cause?: Partial<BackendError> } | null)?.cause
  if (cause && typeof cause.code === "string") {
    return { code: cause.code, category: cause.category ?? "internal", message: cause.message ?? "" }
  }
  return { code: "ERR_UNKNOWN", category: "internal", message: err instanceof Error ? err.message : String(err) }
}

async function call<T>(promise: Promise<T>): Promise<T> {
  try {
    return await promise
  } catch (err) {
    throw toBackendError(err)
  }
}

export const wailsBackend: Backend = {
  drive: {
    list: async (path) => (await call(Drive.List(path))) ?? [],
  },
  settings: {
    listConfig: async () => (await call(Settings.List())) ?? [],
    revealSecret: (key, confirmed) => call(Settings.Reveal(key, confirmed)),
    setConfig: (key, value) => call(Settings.Set(key, value)),
    versions: () => call(Settings.Versions()),
    omarchy: async () => (await call(Settings.Omarchy())) ?? { available: false },
    onOmarchyTheme: (cb) => Events.On("omarchy:theme-changed", (ev) => cb(ev.data)),
  },
}

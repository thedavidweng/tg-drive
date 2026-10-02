import { Events } from "@wailsio/runtime"

import { Auth, Drive, Settings } from "../bindings/github.com/thedavidweng/tg-drive-cli/internal/gui"
import type { Backend, BackendError } from "@/backend"

/**
 * The Wails runtime rejects a failed call with a RuntimeError whose cause is
 * the Go error marshalled as JSON, here internal/gui.Error.
 */
function toBackendError(err: unknown): BackendError {
  const cause = (err as { cause?: Partial<BackendError> } | null)?.cause
  if (cause && typeof cause.code === "string") {
    return {
      code: cause.code,
      category: cause.category ?? "internal",
      message: cause.message ?? "",
      ...(cause.details ? { details: cause.details } : {}),
    }
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
  auth: {
    status: async () => {
      const status = await call(Auth.Status())
      if (!status) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty auth status" } satisfies BackendError
      return status
    },
    setup: async (apiID, apiHash, phone) => {
      const status = await call(Auth.Setup(apiID, apiHash, phone))
      if (!status) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty auth status" } satisfies BackendError
      return status
    },
    login: async (phone, forceNewCode) => {
      const result = await call(Auth.Login(phone, forceNewCode))
      if (!result) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty login result" } satisfies BackendError
      return result
    },
    logout: () => call(Auth.Logout()),
    answerPrompt: (id, value) => call(Auth.AnswerPrompt(id, value)),
    cancelPrompt: (id) => call(Auth.CancelPrompt(id)),
    onPrompt: (cb) => Events.On("auth.prompt", (event) => cb(event.data)),
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

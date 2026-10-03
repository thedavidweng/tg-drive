import { Events } from "@wailsio/runtime"

import { Auth, Channels, Drive, Import, Maintenance, Settings, Transfers } from "../bindings/github.com/thedavidweng/tg-drive/internal/gui"
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
    tree: async (path, maxDepth) => (await call(Drive.Tree(path, maxDepth))) ?? [],
    mkdir: (path) => call(Drive.Mkdir(path)),
    move: (from, to, opts) => call(Drive.Move(from, to, opts)),
    delete: async (path, opts) => (await call(Drive.Delete(path, opts))) ?? { mode: "", path },
    share: async (path) => (await call(Drive.Share(path))) ?? { url: "", path, channel: "" },
    scan: async () => (await call(Drive.Scan())) ?? { mode: "", active: 0, deleted: 0, invalid: 0, missing: 0 },
  },
  events: {
    onDirectoryChanged: (cb) => Events.On("directory-changed", (ev) => cb(ev.data)),
    onScanProgress: (cb) => Events.On("scan-progress", (ev) => cb(ev.data)),
    onTransferStage: (cb) => Events.On("transfer-stage", (ev) => cb(ev.data)),
    onTransferProgress: (cb) => Events.On("transfer-progress", (ev) => cb(ev.data)),
    onTransferRemoved: (cb) => Events.On("transfer-removed", (ev) => cb(ev.data)),
    onFilesDropped: (cb) => Events.On("files-dropped", (ev) => cb(ev.data)),
    onChannelsChanged: (cb) => Events.On("channels-changed", (ev) => cb(ev.data)),
  },
  transfers: {
    list: async () => (await call(Transfers.List())) ?? { active: [], history: [] },
    upload: async (paths, dest, opts) => (await call(Transfers.Upload(paths, dest, opts))) ?? [],
    download: (remotePath, destDir, opts) => call(Transfers.Download(remotePath, destDir, opts)),
    planUpload: async (paths, dest, policy) => {
      const plan = await call(Transfers.PlanUpload(paths, dest, policy))
      if (!plan) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty upload plan" } satisfies BackendError
      return plan
    },
    cancel: async (id) => {
      const t = await call(Transfers.Cancel(id))
      if (!t) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty transfer" } satisfies BackendError
      return t
    },
    retry: async (id) => {
      const t = await call(Transfers.Retry(id))
      if (!t) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty transfer" } satisfies BackendError
      return t
    },
    clearFinished: async () => (await call(Transfers.ClearFinished())) ?? 0,
    pickFiles: async () => (await call(Transfers.PickFiles())) ?? [],
    pickDirectory: async () => (await call(Transfers.PickDirectory())) ?? "",
  },
  channels: {
    list: async () => (await call(Channels.List())) ?? [],
    status: async () => {
      const status = await call(Channels.Status())
      if (!status) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty channel status" } satisfies BackendError
      return status
    },
    choices: async () => (await call(Channels.Choices())) ?? { channels: [], default_title: "" },
    bind: async (req) => {
      const result = await call(Channels.Bind(req))
      if (!result) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty bind result" } satisfies BackendError
      return result
    },
    select: async (channelID) => {
      const status = await call(Channels.Select(channelID))
      if (!status) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty channel status" } satisfies BackendError
      return status
    },
    linkDiscussion: async () => {
      const link = await call(Channels.LinkDiscussion())
      if (!link) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty discussion link" } satisfies BackendError
      return link
    },
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
  import: {
    preview: async (opts) => {
      const outcome = await call(Import.Preview(opts))
      if (!outcome) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty import plan" } satisfies BackendError
      return outcome
    },
    run: async (opts) => {
      const outcome = await call(Import.Run(opts))
      if (!outcome) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty import result" } satisfies BackendError
      return outcome
    },
    answerPrompt: (id, choice) => call(Import.AnswerPrompt(id, choice)),
    cancelPrompt: (id) => call(Import.CancelPrompt(id)),
    onPrompt: (cb) => Events.On("import.prompt", (ev) => cb(ev.data)),
    onItem: (cb) => Events.On("import.item", (ev) => cb(ev.data)),
  },
  maintenance: {
    previewAdopt: async (opts) => {
      const outcome = await call(Maintenance.PreviewAdopt(opts))
      if (!outcome) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty adopt plan" } satisfies BackendError
      return outcome
    },
    adopt: async (opts) => {
      const outcome = await call(Maintenance.Adopt(opts))
      if (!outcome) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty adopt result" } satisfies BackendError
      return outcome
    },
    repair: async (opts) => {
      const outcome = await call(Maintenance.Repair(opts))
      if (!outcome) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty repair result" } satisfies BackendError
      return outcome
    },
    doctor: async () => {
      const report = await call(Maintenance.Doctor())
      if (!report) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty doctor report" } satisfies BackendError
      return report
    },
    pathCodecDoctor: async () => {
      const report = await call(Maintenance.PathCodecDoctor())
      if (!report) throw { code: "ERR_UNKNOWN", category: "internal", message: "empty path-codec report" } satisfies BackendError
      return report
    },
    onRepairItem: (cb) => Events.On("repair.item", (ev) => cb(ev.data)),
  },
}

import type { Backend, BackendError, Entry } from "@/backend"

/** A backend that answers from fixed directory listings, keyed by path. */
export function memoryBackend(dirs: Record<string, Entry[]>): Backend {
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
  }
}

/** A backend whose every call fails with err. */
export function failingBackend(err: BackendError): Backend {
  return {
    drive: {
      async list() {
        throw err
      },
    },
  }
}

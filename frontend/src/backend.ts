import type { Entry } from "../bindings/github.com/thedavidweng/tg-drive-cli/internal/gui/models"

export type { Entry }

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
}

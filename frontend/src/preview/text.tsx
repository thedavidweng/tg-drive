import { lazyView } from "@/preview/lazy-view"
import { languageByExtension } from "@/preview/text-languages"
import type { PreviewProvider } from "@/preview/types"

export const textPreview: PreviewProvider = {
  id: "text",
  mimeTypes: [
    "text/*",
    "application/json",
    "application/yaml",
    "application/x-yaml",
    "application/toml",
    "application/xml",
    "application/xhtml+xml",
    "application/javascript",
    "application/x-sh",
    "application/sql",
    "application/x-subrip",
  ],
  extensions: [
    "txt", "text", "log", "csv", "tsv", "srt", "vtt", "env", "gitignore", "editorconfig",
    ...Object.keys(languageByExtension),
  ],
  View: lazyView(() => import("@/preview/text-view").then((m) => m.TextView)),
}

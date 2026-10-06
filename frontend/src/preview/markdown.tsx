import { lazyView } from "@/preview/lazy-view"
import type { PreviewProvider } from "@/preview/types"

export const markdownPreview: PreviewProvider = {
  id: "markdown",
  mimeTypes: ["text/markdown", "text/x-markdown"],
  extensions: ["md", "markdown", "mdown", "mkd"],
  View: lazyView(() => import("@/preview/markdown-view").then((m) => m.MarkdownView)),
}

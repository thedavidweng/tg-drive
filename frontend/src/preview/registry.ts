import type { PreviewDescriptor } from "@/backend"
import { FallbackPreview } from "@/preview/fallback"
import type { PreviewProvider } from "@/preview/types"

/** Shows file details and Download; chosen when nothing else matches or a view fails. */
export const fallbackPreview: PreviewProvider = { id: "fallback", View: FallbackPreview }

/** The lower-case extension of a file name without the dot; "" when it has none. */
export function extensionOf(name: string): string {
  const i = name.lastIndexOf(".")
  if (i <= 0 || i === name.length - 1) return ""
  return name.slice(i + 1).toLowerCase()
}

/** The descriptor's MIME type without parameters; "" when it says nothing useful. */
function baseMIME(mime: string): string {
  const base = mime.split(";")[0].trim().toLowerCase()
  return base === "application/octet-stream" ? "" : base
}

function matchesMIME(provider: PreviewProvider, mime: string): boolean {
  if (!mime) return false
  const top = mime.split("/")[0] + "/*"
  return (provider.mimeTypes ?? []).some((m) => m === mime || m === top)
}

/**
 * The provider that shows d: the first eligible provider, in order, whose
 * MIME types match; else the first whose extensions match; else the
 * fallback.
 */
export function choosePreview(d: PreviewDescriptor, providers: readonly PreviewProvider[]): PreviewProvider {
  const eligible = (p: PreviewProvider) => p.canPreview?.(d) ?? true
  const mime = baseMIME(d.mime)
  const byMIME = providers.find((p) => matchesMIME(p, mime) && eligible(p))
  if (byMIME) return byMIME
  const ext = extensionOf(d.name)
  const byExt = ext ? providers.find((p) => (p.extensions ?? []).includes(ext) && eligible(p)) : undefined
  return byExt ?? fallbackPreview
}

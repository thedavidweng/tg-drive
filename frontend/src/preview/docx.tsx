import { useEffect, useRef } from "react"

import { OfficeView, type OfficeRendererProps } from "@/preview/office"
import type { PreviewProvider, PreviewViewProps } from "@/preview/types"

function DocxRenderer({ data, onError }: OfficeRendererProps) {
  const hostRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let live = true
    ;(async () => {
      const [{ renderAsync }, { default: DOMPurify }] = await Promise.all([import("docx-preview"), import("dompurify")])
      // Rendered detached and sanitized before it joins the page, so no
      // document markup is live before the sanitizer has seen it.
      const staging = document.createElement("div")
      await renderAsync(data, staging, staging, {
        className: "docx",
        inWrapper: true,
        ignoreFonts: true,
        useBase64URL: true,
        // An altChunk is embedded HTML, rendered as an iframe srcdoc.
        renderAltChunks: false,
        experimental: false,
      })
      if (!live) return
      DOMPurify.sanitize(staging, { IN_PLACE: true, ADD_TAGS: ["style"] })
      // The document's own stylesheet stays inside the shadow root instead
      // of restyling the app.
      const root = host.shadowRoot ?? host.attachShadow({ mode: "open" })
      root.replaceChildren(staging)
    })().catch(() => {
      if (live) onError()
    })
    return () => {
      live = false
    }
  }, [data, onError])

  return <div ref={hostRef} className="min-h-full" />
}

function DocxPreview(props: PreviewViewProps) {
  return <OfficeView {...props} Renderer={DocxRenderer} />
}

export const docxPreview: PreviewProvider = {
  id: "docx",
  mimeTypes: ["application/vnd.openxmlformats-officedocument.wordprocessingml.document"],
  extensions: ["docx"],
  View: DocxPreview,
}

import { useEffect, useRef } from "react"

import { OfficeView, type OfficeRendererProps } from "@/preview/office"
import { sanitizedCopy, type SanitizePolicy } from "@/preview/safe-html"
import type { PreviewProvider, PreviewViewProps } from "@/preview/types"

const block = ["class", "style", "id"] as const
const cell = [...block, "colspan", "rowspan"] as const

// What docx-preview emits. Links keep no target, since following one would
// navigate the app away; drawings rendered as SVG are dropped.
const docxPolicy: SanitizePolicy = {
  css: true,
  allow: {
    div: block, section: block, article: block, header: block, footer: block, p: block, span: block,
    a: block, b: block, i: block, u: block, s: block, sub: block, sup: block, del: block, ins: block,
    ol: block, ul: block, li: block, br: block, wbr: [],
    table: block, colgroup: block, col: [...block, "span"], thead: block, tbody: block, tr: block, td: cell, th: cell,
    img: [...block, "src", "alt", "width", "height"],
  },
}

function DocxRenderer({ data, onError }: OfficeRendererProps) {
  const hostRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let live = true
    ;(async () => {
      const { renderAsync } = await import("docx-preview")
      // Rendered detached and rebuilt before it joins the page, so no
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
      // The document's own stylesheet stays inside the shadow root instead
      // of restyling the app.
      const root = host.shadowRoot ?? host.attachShadow({ mode: "open" })
      root.replaceChildren(sanitizedCopy(staging, docxPolicy))
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

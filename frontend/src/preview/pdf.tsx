import { Component, useEffect, useState, type ComponentType, type ReactNode } from "react"

import { useI18n } from "@/i18n"
import type { PreviewProvider, PreviewViewProps } from "@/preview/types"
import { useLatest } from "@/preview/use-latest"

type Viewer = ComponentType<PreviewViewProps>

let ready: Promise<Viewer> | undefined

// EmbedPDF and PDFium are megabytes; they load only when a PDF is opened.
// Only success is cached, so a later preview retries a failed load.
function loadViewer(): Promise<Viewer> {
  ready ??= import("@/preview/pdf-viewer").then(async (m) => {
    await m.probePdfium()
    return m.PdfViewer
  })
  ready.catch(() => {
    ready = undefined
  })
  return ready
}

/** Turns a viewer that throws while rendering into the surface's fallback. */
class ViewerBoundary extends Component<{ onError: () => void; children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch() {
    this.props.onError()
  }

  render() {
    return this.state.failed ? null : this.props.children
  }
}

function PdfPreview(props: PreviewViewProps) {
  const { t } = useI18n()
  const [Viewer, setViewer] = useState<Viewer | null>(null)
  const onErrorRef = useLatest(props.onError)

  useEffect(() => {
    let live = true
    loadViewer().then(
      (v) => live && setViewer(() => v),
      () => live && onErrorRef.current(),
    )
    return () => {
      live = false
    }
  }, [onErrorRef])

  if (!Viewer) return <p className="py-16 text-center text-muted-foreground">{t("preview.loading")}</p>
  return (
    <div className="h-full min-h-0">
      <ViewerBoundary onError={() => onErrorRef.current()}>
        <Viewer {...props} />
      </ViewerBoundary>
    </div>
  )
}

export const pdfPreview: PreviewProvider = {
  id: "pdf",
  mimeTypes: ["application/pdf"],
  extensions: ["pdf"],
  View: PdfPreview,
}

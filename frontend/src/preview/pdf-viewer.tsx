import pdfiumWasm from "@embedpdf/pdfium/pdfium.wasm?url"
import { PDFViewer, type PDFViewerConfig, type PluginRegistry } from "@embedpdf/react-pdf-viewer"
import { useCallback, useEffect, useRef, useState } from "react"

import { useI18n, type Locale } from "@/i18n"
import type { PreviewViewProps } from "@/preview/types"

// A read-only viewer: everything that edits, saves, or opens another
// document is off; navigation, zoom, search, rotation, the sidebars,
// selection, print, and fullscreen stay.
const editingCategories = [
  "annotation",
  "redaction",
  "form",
  "insert",
  "signature",
  "stamp",
  "shape",
  "markup",
  "history",
  "capture",
  "mode-annotate",
  "mode-form",
  "mode-insert",
  "mode-redact",
  "mode-shapes",
  "document-open",
  "document-close",
  "document-export",
  "document-protect",
  "panel-comment",
  "panel-annotation-style",
  "panel-redaction",
]

type ViewerState = ReturnType<ReturnType<PluginRegistry["getStore"]>["getState"]>

// FPDF_ERR_PASSWORD: the viewer asks for the password itself.
const passwordErrorCode = 4

function pdfiumURL(): string {
  return new URL(pdfiumWasm, document.baseURI).href
}

// Mirrors how EmbedPDF's engine worker loads PDFium: a blob: module worker
// fetching the absolute WASM URL.
const probeSource = `self.onmessage = async (e) => {
  try {
    const r = await fetch(e.data)
    if (!r.ok) throw new Error("HTTP " + r.status)
    if (!WebAssembly.validate(await r.arrayBuffer())) throw new Error("not WebAssembly")
    self.postMessage("")
  } catch (err) {
    self.postMessage(String((err && err.message) || err) || "failed")
  }
}`

/**
 * Resolves once a worker can fetch a valid PDFium. EmbedPDF's own worker
 * reports a failed WASM load in a message its executor ignores, so the
 * viewer would wait forever; this turns that failure into the fallback.
 */
export function probePdfium(): Promise<void> {
  return new Promise((resolve, reject) => {
    const script = URL.createObjectURL(new Blob([probeSource], { type: "text/javascript" }))
    let worker: Worker
    const done = (error?: string) => {
      worker.terminate()
      URL.revokeObjectURL(script)
      if (error === undefined) resolve()
      else reject(new Error(error))
    }
    try {
      worker = new Worker(script, { type: "module" })
      worker.onmessage = (e: MessageEvent<string>) => done(e.data || undefined)
      worker.onerror = () => done("worker failed")
      worker.postMessage(pdfiumURL())
    } catch (error) {
      URL.revokeObjectURL(script)
      reject(error)
    }
  })
}

/**
 * The EmbedPDF configuration for one preview. Every resource it names is
 * same-origin: PDFium comes from the bundled frontend, and the jsDelivr font
 * fallback and stamp library and the Google Fonts stylesheets EmbedPDF
 * loads by default are off.
 */
export function pdfViewerConfig(src: string, locale: Locale): PDFViewerConfig {
  const theme = document.documentElement.dataset.theme
  return {
    src: new URL(src, document.baseURI).href,
    // The engine fetches the WASM from a blob: worker, which has no base URL.
    wasmUrl: pdfiumURL(),
    worker: true,
    fontFallback: null,
    fonts: { ui: null, signature: null },
    stamp: { manifests: [], defaultLibrary: false },
    theme: { preference: theme === "light" || theme === "dark" ? theme : "system" },
    tabBar: "never",
    disabledCategories: editingCategories,
    i18n: { defaultLocale: locale },
  }
}

/** The embedded PDF viewer; any document error replaces it with the fallback. */
export function PdfViewer({ descriptor, onError }: PreviewViewProps) {
  const { locale } = useI18n()
  const onErrorRef = useRef(onError)
  useEffect(() => {
    onErrorRef.current = onError
  })
  const unsubscribe = useRef<(() => void) | null>(null)
  useEffect(() => () => unsubscribe.current?.(), [])

  // The document may fail before the registry is ready, so this reads the
  // current state as well as watching it.
  const onReady = useCallback((registry: PluginRegistry) => {
    const store = registry.getStore()
    const check = (state: ViewerState) => {
      const failed = Object.values(state.core.documents).find(
        (d) => d.status === "error" && d.errorCode !== passwordErrorCode,
      )
      // PDFium's own messages ("FPDF_LoadMemDocument failed") mean nothing
      // to a user; the fallback's generic notice says enough.
      if (failed) onErrorRef.current()
    }
    unsubscribe.current?.()
    unsubscribe.current = store.subscribe((_action, state) => check(state))
    check(store.getState())
  }, [])

  // Fixed per preview: EmbedPDF reloads the viewer for a new config object.
  const [config] = useState(() => pdfViewerConfig(descriptor.url, locale))

  return <PDFViewer config={config} onReady={onReady} style={{ width: "100%", height: "100%" }} />
}

import { afterEach, expect, test } from "bun:test"
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react"

import { App } from "@/App"
import type { PreviewDescriptor } from "@/backend"
import { previewProviders } from "@/preview/providers"
import { choosePreview } from "@/preview/registry"
import { memoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

const { happyDOM } = window as unknown as { happyDOM: { setURL(url: string): void } }

function descriptor(name: string, mime: string): PreviewDescriptor {
  return {
    name,
    path: "/" + name,
    mime,
    size: 10,
    date: "2026-01-01T00:00:00Z",
    capabilities: { ranges: true, media_size: 10 },
    url: "data:,",
  }
}

test("the registry picks the PDF provider for application/pdf and for .pdf", () => {
  const pick = (name: string, mime: string) => choosePreview(descriptor(name, mime), previewProviders).id
  expect(pick("report.pdf", "application/pdf")).toBe("pdf")
  expect(pick("report.bin", "application/pdf")).toBe("pdf")
  expect(pick("Scan.PDF", "application/octet-stream")).toBe("pdf")
  expect(pick("scan.pdf", "")).toBe("pdf")
})

// A webview whose workers cannot load PDFium (here: every worker fails), the
// way a blocked or missing WASM asset fails in td-gui.
class FailingWorker extends EventTarget {
  onmessage: ((e: MessageEvent) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  postMessage() {
    setTimeout(() => this.onerror?.(new Event("error")))
  }
  terminate() {}
}

test("a PDF whose viewer fails to load falls back to file details and Download", async () => {
  const realWorker = globalThis.Worker
  globalThis.Worker = FailingWorker as unknown as typeof Worker
  happyDOM.setURL("http://localhost/")
  try {
    const backend = memoryBackend({
      "/": [{ name: "report.pdf", path: "/report.pdf", type: "file" as const, size: 9, date: "2026-01-02T00:00:00Z" }],
    })
    backend.putPreviewForTest("/report.pdf", { mime: "application/pdf", data: "%PDF-1.7" })
    render(<App backend={backend} languages={["en"]} />)
    fireEvent.click(await screen.findByRole("button", { name: "report.pdf" }))
    const dialog = await screen.findByRole("dialog", { name: "report.pdf" })

    await within(dialog).findByText("This file could not be previewed.", undefined, { timeout: 4000 })
    expect(dialog.textContent).toContain("/report.pdf")
    expect(within(dialog).getAllByRole("button", { name: "Download" }).length).toBeGreaterThan(0)
  } finally {
    globalThis.Worker = realWorker
    happyDOM.setURL("about:blank")
  }
})

function urlsIn(value: unknown): string[] {
  if (typeof value === "string") return /^[a-z][a-z0-9+.-]*:\/\//i.test(value) ? [value] : []
  if (value && typeof value === "object") return Object.values(value).flatMap(urlsIn)
  return []
}

test("the PDF viewer fetches only from the app's own origin: bundled PDFium, no CDN or webfonts", async () => {
  happyDOM.setURL("http://wails.localhost/")
  try {
    const { pdfViewerConfig } = await import("@/preview/pdf-viewer")
    const config = pdfViewerConfig("/td-media/abc?c=def", "en")

    expect(config.wasmUrl).toMatch(/^http:\/\/wails\.localhost\/.*pdfium.*\.wasm$/)
    expect(config.src).toBe("http://wails.localhost/td-media/abc?c=def")
    // EmbedPDF's defaults fetch fallback fonts from jsDelivr and its UI and
    // signature fonts from Google Fonts; null is its documented opt-out.
    expect(config.fontFallback).toBeNull()
    expect(config.fonts).toEqual({ ui: null, signature: null })
    // Its stamp plugin fetches a stamp library manifest from jsDelivr.
    expect(config.stamp?.manifests).toEqual([])
    const urls = urlsIn(config)
    expect(urls.length).toBe(2)
    for (const url of urls) expect(new URL(url).origin).toBe("http://wails.localhost")
  } finally {
    happyDOM.setURL("about:blank")
  }
})

// EmbedPDF renders its toolbar inside a shadow root; the surface's Tab trap
// must count those controls or Tab would wrap from Close straight past them.
test("the preview's Tab trap includes viewer controls inside a shadow root", async () => {
  const backend = memoryBackend({
    "/": [{ name: "cat.png", path: "/cat.png", type: "file" as const, size: 68, date: "2026-01-03T00:00:00Z" }],
  })
  backend.putPreviewForTest("/cat.png", { mime: "image/png", data: "x" })
  render(<App backend={backend} languages={["en"]} />)
  fireEvent.click(await screen.findByRole("button", { name: "cat.png" }))
  const dialog = await screen.findByRole("dialog", { name: "cat.png" })
  const img = await within(dialog).findByRole("img", { name: "cat.png" })

  const host = document.createElement("div")
  img.parentElement!.append(host)
  const shadow = host.attachShadow({ mode: "open" })
  shadow.innerHTML = "<button>Zoom in</button><button>Search</button>"
  const search = shadow.querySelectorAll("button")[1]
  const close = within(dialog).getByRole("button", { name: "Close preview" })
  const first = dialog.querySelector("button")!

  // Close is no longer the last stop, so Tab from it is left to the browser.
  act(() => close.focus())
  expect(fireEvent.keyDown(close, { key: "Tab" })).toBe(true)
  // From the viewer's last control, Tab wraps to the first.
  act(() => search.focus())
  // Outside the shadow root a key event from inside it targets the host.
  fireEvent.keyDown(host, { key: "Tab" })
  expect(document.activeElement === first).toBe(true)
  // And Shift+Tab from the first lands on the viewer's last control.
  fireEvent.keyDown(first, { key: "Tab", shiftKey: true })
  expect(shadow.activeElement === search).toBe(true)
})

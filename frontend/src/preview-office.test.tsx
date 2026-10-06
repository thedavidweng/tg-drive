import { afterEach, beforeEach, expect, test } from "bun:test"
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"

import { App } from "@/App"
import type { PreviewDescriptor } from "@/backend"
import { previewProviders } from "@/preview/providers"
import { choosePreview } from "@/preview/registry"
import { sanitizedCopy } from "@/preview/safe-html"
import { memoryBackend } from "@/testing/memory-backend"

const realFetch = globalThis.fetch
let fetched: string[] = []

beforeEach(() => {
  fetched = []
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    fetched.push(typeof input === "string" ? input : input instanceof URL ? input.href : input.url)
    return realFetch(input, init)
  }) as typeof fetch
})

afterEach(() => {
  cleanup()
  globalThis.fetch = realFetch
})

const MiB = 1024 * 1024

function backendWithDocs() {
  const backend = memoryBackend(
    { "/": [{ name: "docs", path: "/docs", type: "dir" as const, size: 0, date: "2026-01-01T00:00:00Z" }] },
    { transfers: { picks: { dir: "/home/me/Downloads" } } },
  )
  backend.mkdirForTest("/docs")
  return backend
}

async function openInDocs(name: string) {
  fireEvent.click(await screen.findByRole("button", { name: "docs" }))
  const list = await screen.findByRole("list", { name: "Files in /docs" })
  fireEvent.click(within(list).getByRole("button", { name }))
  return screen.findByRole("dialog", { name })
}

function descriptor(name: string, mime: string, size = 10): PreviewDescriptor {
  return {
    name,
    path: "/" + name,
    mime,
    size,
    date: "2026-01-01T00:00:00Z",
    capabilities: { ranges: true, media_size: size },
    url: "data:,",
  }
}

const DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
const XLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
const PPTX = "application/vnd.openxmlformats-officedocument.presentationml.presentation"

test("the registry routes docx, xlsx, and pptx by MIME and by extension", () => {
  const pick = (name: string, mime: string) => choosePreview(descriptor(name, mime), previewProviders).id
  expect(pick("report.bin", DOCX)).toBe("docx")
  expect(pick("budget.bin", XLSX)).toBe("xlsx")
  expect(pick("deck.bin", PPTX)).toBe("pptx")
  expect(pick("Report.DOCX", "application/octet-stream")).toBe("docx")
  expect(pick("budget.xlsx", "")).toBe("xlsx")
  expect(pick("deck.pptx", "application/zip")).toBe("pptx")
  // Legacy binary Office formats are not these renderers' input.
  expect(pick("old.doc", "application/msword")).toBe("fallback")
  expect(pick("old.xls", "application/vnd.ms-excel")).toBe("fallback")
  expect(pick("old.ppt", "application/vnd.ms-powerpoint")).toBe("fallback")
})

test("an Office file of 100 MiB or more warns and fetches nothing until the user continues", async () => {
  const backend = backendWithDocs()
  backend.putFileForTest("/docs/huge.docx", 100 * MiB, "")
  backend.putPreviewForTest("/docs/huge.docx", { data: "not a real document" })
  render(<App backend={backend} languages={["en"]} />)
  const dialog = await openInDocs("huge.docx")

  await within(dialog).findByText("This file is large")
  expect(dialog.textContent).toContain("100 MB")
  expect(fetched).toEqual([])

  fireEvent.click(within(dialog).getByRole("button", { name: "Preview anyway" }))
  await waitFor(() => expect(fetched.length).toBe(1))
  expect(fetched[0]).toStartWith("data:")
  // The bytes are not a document: the renderer fails into the fallback, which keeps Download.
  await within(dialog).findByText("This file could not be previewed.")
  expect(within(dialog).getAllByRole("button", { name: "Download" }).length).toBeGreaterThan(0)
})

test("an Office file under 100 MiB is fetched at once, with no warning", async () => {
  const backend = backendWithDocs()
  backend.putFileForTest("/docs/big.pptx", 100 * MiB - 1, "")
  backend.putPreviewForTest("/docs/big.pptx", { data: "not a real deck" })
  render(<App backend={backend} languages={["en"]} />)
  const dialog = await openInDocs("big.pptx")

  await waitFor(() => expect(fetched.length).toBe(1))
  expect(within(dialog).queryByText("This file is large")).toBeNull()
})

test("a renderer failure on each Office format falls back to file details and Download", async () => {
  for (const name of ["broken.docx", "broken.xlsx", "broken.pptx"]) {
    const backend = backendWithDocs()
    backend.putPreviewForTest("/docs/" + name, { data: "PK not really a zip" })
    render(<App backend={backend} languages={["en"]} />)
    const dialog = await openInDocs(name)

    await within(dialog).findByText("This file could not be previewed.", {}, { timeout: 5000 })
    expect(dialog.textContent).toContain("/docs/" + name)
    expect(within(dialog).getAllByRole("button", { name: "Download" }).length).toBeGreaterThan(0)
    cleanup()
  }
})

async function minimalDocx(): Promise<Uint8Array> {
  const { default: JSZip } = await import("jszip")
  const rels = "http://schemas.openxmlformats.org/package/2006/relationships"
  const zip = new JSZip()
  zip.file(
    "[Content_Types].xml",
    '<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">' +
      '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>' +
      '<Default Extension="xml" ContentType="application/xml"/>' +
      '<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
  )
  zip.file(
    "_rels/.rels",
    `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="${rels}">` +
      '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
  )
  zip.file(
    "word/document.xml",
    '<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>' +
      "<w:p><w:r><w:t>Quarterly summary</w:t></w:r></w:p><w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>" +
      "</w:body></w:document>",
  )
  return zip.generateAsync({ type: "uint8array" })
}

test("a Word document renders its text with its own stylesheet, inside a shadow root", async () => {
  const backend = backendWithDocs()
  backend.putPreviewForTest("/docs/report.docx", { data: await minimalDocx() })
  render(<App backend={backend} languages={["en"]} />)
  const dialog = await openInDocs("report.docx")

  const shadow = await waitFor(
    () => {
      const root = Array.from(dialog.querySelectorAll("div")).find((d) => d.shadowRoot)?.shadowRoot
      if (!root?.textContent?.includes("Second paragraph")) throw new Error("document not rendered yet")
      return root
    },
    { timeout: 5000 },
  )
  expect(shadow.textContent).toContain("Quarterly summary")
  expect(shadow.querySelector("style")).not.toBeNull()
  expect(document.head.querySelector("style")?.textContent ?? "").not.toContain("docx-wrapper")
})

// Embedded viewers (the PDF viewer's menus and page field, here the Word
// view) live in a shadow root and act on Escape without preventDefault.
test("Escape from inside a view's shadow root stays with the view; from the surface it closes", async () => {
  const backend = backendWithDocs()
  backend.putPreviewForTest("/docs/report.docx", { data: await minimalDocx() })
  render(<App backend={backend} languages={["en"]} />)
  const dialog = await openInDocs("report.docx")
  const shadow = await waitFor(
    () => {
      const root = Array.from(dialog.querySelectorAll("div")).find((d) => d.shadowRoot)?.shadowRoot
      if (!root?.querySelector("p")) throw new Error("document not rendered yet")
      return root
    },
    { timeout: 5000 },
  )

  act(() => {
    shadow.querySelector("p")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, composed: true }))
  })
  expect(screen.getByRole("dialog", { name: "report.docx" })).toBe(dialog)

  fireEvent.keyDown(within(dialog).getByRole("button", { name: "Close preview" }), { key: "Escape" })
  expect(screen.queryByRole("dialog")).toBeNull()
})

// docx-preview copies run colours and other values into CSS verbatim, so a
// document can smuggle declarations into the stylesheet. happy-dom's XML
// parser drops namespaced attributes, so no fixture document reaches that
// path here; the rebuild is checked directly.
test("the Word rebuild keeps inert CSS and drops anything that could load a resource", () => {
  const allow = { p: ["style"], img: ["src"] }
  const rebuilt = (html: string) => {
    const staging = document.createElement("div")
    staging.innerHTML = html
    const host = document.createElement("div")
    host.append(sanitizedCopy(staging, { allow, css: true }))
    return host.innerHTML
  }
  const sheet = (css: string) => rebuilt(`<style>${css}</style>`)
  const png = "data:image/png;base64,iVBORw0KGgo="

  for (const css of [
    "@import url(https://leak.example/a.css);",
    '@import "https://leak.example/a.css";',
    "@\\69mport 'https://leak.example/a.css';",
    "p{background:url(https://leak.example/a.png)}",
    "p{background:URL( 'https://leak.example/a.png' )}",
    "p{background:u\\72l(https://leak.example/a.png)}",
    "p{background:\\75 rl(//leak.example/a.png)}",
    'p{background:image-set("https://leak.example/a.png" 1x)}',
    'p{background:-webkit-image-set("https://leak.example/a.png" 1x)}',
    "@font-face{font-family:x;src:url(https://leak.example/f.woff)}",
    "p{color:#000;background:url(/relative.png)}",
    '/*"*/ p{background:url(https://leak.example/a.png)} /*"*/',
    "/*'*/ @import 'https://leak.example/a.css'; /*'*/",
    'p{content:"/*";background:url(https://leak.example/a.png)} /*"*/',
    'p{--label:\\";background:url(https://leak.example/a.png)} /*"*/',
    'p{content:"broken\n; background:url(https://leak.example/a.png)}',
    'p{content:"unterminated; background:url(https://leak.example/a.png)}',
  ]) {
    expect(sheet(css)).toBe("")
  }
  for (const css of [
    "p{color:rgba(0,0,0,.5);width:calc(100% - 2px)}",
    'p.n:before{content:"(1)\\9";counter-increment:x}',
    "section.docx:not(:last-child){margin:0}",
    `p{background:url(${png})}`,
    `p{background:url("${png}")}`,
    '/* a "quoted" comment */ p{color:red}',
    'p:before{content:"/* (1) */";color:red}',
    'p:before{content:"an escaped \\" quote";color:red}',
  ]) {
    expect(sheet(css)).toBe(`<style>${css}</style>`)
  }

  expect(rebuilt('<p style="background-image:url(https://leak.example/a.png)">t</p>')).toBe("<p>t</p>")
  expect(rebuilt('<p style="color:red">t</p>')).toBe('<p style="color:red">t</p>')
  expect(rebuilt('<img src="https://leak.example/a.png">')).toBe("<img>")
  expect(rebuilt(`<img src="${png}">`)).toBe(`<img src="${png}">`)
})

test("a workbook shows each worksheet's cells read-only", async () => {
  const { default: ExcelJS } = await import("exceljs")
  const wb = new ExcelJS.Workbook()
  const budget = wb.addWorksheet("Budget")
  budget.addRow(["Item", "Cost"])
  budget.addRow(["<b>Rent</b>", 1200])
  wb.addWorksheet("Notes").addRow(["remember"])
  const bytes = new Uint8Array(await wb.xlsx.writeBuffer())

  const backend = backendWithDocs()
  backend.putPreviewForTest("/docs/budget.xlsx", { data: bytes })
  render(<App backend={backend} languages={["en"]} />)
  const dialog = await openInDocs("budget.xlsx")

  const table = await within(dialog).findByRole("table", { name: "Budget" }, { timeout: 5000 })
  expect(within(table).getByRole("columnheader", { name: "B" })).toBeTruthy()
  expect(within(table).getByRole("cell", { name: "Cost" })).toBeTruthy()
  expect(within(table).getByRole("cell", { name: "1200" })).toBeTruthy()
  // Cell text is text, never markup.
  expect(within(table).getByRole("cell", { name: "<b>Rent</b>" })).toBeTruthy()
  expect(table.querySelector("b")).toBeNull()
  expect(within(dialog).getByRole("tab", { name: "Notes" })).toBeTruthy()
  expect(within(dialog).queryByRole("textbox")).toBeNull()
})

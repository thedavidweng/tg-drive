import { afterEach, beforeEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"

import { App } from "@/App"
import type { PreviewDescriptor } from "@/backend"
import { previewProviders } from "@/preview/providers"
import { choosePreview } from "@/preview/registry"
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

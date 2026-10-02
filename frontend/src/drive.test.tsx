import { afterEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen, waitForElementToBeRemoved, within } from "@testing-library/react"

import { App } from "@/App"
import { memoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

const seed = {
  "/": [
    { name: "photos", path: "/photos", type: "dir", size: 0, date: "2026-01-01T00:00:00Z" },
    { name: "notes.txt", path: "/notes.txt", type: "file", size: 2048, date: "2026-01-02T10:30:00Z" },
  ],
  "/photos": [{ name: "cat.jpg", path: "/photos/cat.jpg", type: "file", size: 512, date: "2026-01-03T00:00:00Z" }],
  "/photos/2024": [{ name: "beach.jpg", path: "/photos/2024/beach.jpg", type: "file", size: 99, date: "2026-01-04T00:00:00Z" }],
}

// seedTree adds the nested directory used by the tree view test.
function treeBackend() {
  const b = memoryBackend(seed)
  b.mkdirForTest("/photos/2024")
  b.putFileForTest("/photos/2024/beach.jpg", 99, "2026-01-04T00:00:00Z")
  return b
}

function row(name: string, listPath = "/"): HTMLElement {
  const list = screen.getByRole("list", { name: `Files in ${listPath}` })
  const el = within(list)
    .getAllByRole("listitem")
    .find((li) => li.textContent?.includes(name))
  if (!el) throw new Error(`no row for ${name}`)
  return el
}

test("breadcrumbs show the current path and jump back to a parent", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "photos" }))
  await screen.findByRole("list", { name: "Files in /photos" })

  const crumbs = screen.getByRole("navigation", { name: "Current location" })
  expect(within(crumbs).getByRole("button", { name: "Drive" })).toBeTruthy()
  expect(within(crumbs).getByText("photos")).toBeTruthy()

  fireEvent.click(within(crumbs).getByRole("button", { name: "Drive" }))
  await screen.findByRole("list", { name: "Files in /" })
})

test("the list card shows each file's size, type, and date", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)

  await screen.findByRole("list", { name: "Files in /" })
  const notes = row("notes.txt")
  expect(notes.textContent).toContain("2 KB")
  expect(notes.textContent).toContain("TXT")
  expect(notes.textContent).toMatch(/2026/)
  const photos = row("photos")
  expect(photos.textContent).toContain("Folder")
})

test("creating a folder from the toolbar lists it", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "New folder" }))
  const dialog = screen.getByRole("dialog", { name: "New folder" })
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Folder name" }), { target: { value: "docs" } })
  fireEvent.click(within(dialog).getByRole("button", { name: "Create" }))

  await screen.findByText("docs")
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(row("docs").textContent).toContain("Folder")
})

test("deleting a file is blocked by a confirmation sheet until confirmed", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(within(row("notes.txt")).getByRole("button", { name: "Delete" }))
  const dialog = screen.getByRole("dialog", { name: "Delete file" })
  expect(dialog.textContent).toContain("notes.txt")
  // Not deleted yet: the row is still listed.
  expect(row("notes.txt")).toBeTruthy()

  // Cancelling keeps the file.
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }))
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(row("notes.txt")).toBeTruthy()

  // Confirming deletes it.
  fireEvent.click(within(row("notes.txt")).getByRole("button", { name: "Delete" }))
  fireEvent.click(within(screen.getByRole("dialog", { name: "Delete file" })).getByRole("button", { name: "Delete" }))
  await waitForElementToBeRemoved(() => screen.queryByText("notes.txt"))
})

test("renaming a file goes through a confirmation sheet", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(within(row("notes.txt")).getByRole("button", { name: "Rename" }))
  const dialog = screen.getByRole("dialog", { name: "Rename or move" })
  const input = within(dialog).getByRole("textbox", { name: "New path" }) as HTMLInputElement
  expect(input.value).toBe("/notes.txt")
  fireEvent.change(input, { target: { value: "/todo.txt" } })
  fireEvent.click(within(dialog).getByRole("button", { name: "Confirm" }))

  await screen.findByText("todo.txt")
  expect(screen.queryByText("notes.txt")).toBeNull()
})

test("moving a file into a folder goes through the same sheet", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(within(row("notes.txt")).getByRole("button", { name: "Rename" }))
  const dialog = screen.getByRole("dialog", { name: "Rename or move" })
  fireEvent.change(within(dialog).getByRole("textbox", { name: "New path" }), { target: { value: "/photos/notes.txt" } })
  fireEvent.click(within(dialog).getByRole("button", { name: "Confirm" }))

  await waitForElementToBeRemoved(() => screen.queryByText("notes.txt"))
  fireEvent.click(screen.getByRole("button", { name: "photos" }))
  await screen.findByRole("list", { name: "Files in /photos" })
  expect(row("notes.txt", "/photos")).toBeTruthy()
})

test("sharing a file shows its invite link", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(within(row("notes.txt")).getByRole("button", { name: "Share" }))
  const dialog = screen.getByRole("dialog", { name: "Share link" })
  await within(dialog).findByText("https://t.me/+fake1001")

  fireEvent.click(within(dialog).getByRole("button", { name: "Close" }))
  expect(screen.queryByRole("dialog")).toBeNull()
})

test("a rescan reports live progress and its outcome", async () => {
  const backend = memoryBackend(seed)
  backend.holdScan()
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Rescan" }))
  backend.emitScanProgress({ stage: "reading", indexed: 0, failed: 0 })
  expect((await screen.findByRole("status")).textContent).toContain("Reading channel history…")

  backend.emitScanProgress({ stage: "indexing", indexed: 2, failed: 0 })
  expect((await screen.findByRole("status")).textContent).toContain("Indexing… 2 files")

  backend.finishScan()
  expect((await screen.findByRole("status")).textContent).toContain("Scan complete: 3 files indexed")
})

test("the tree view renders the nested structure", async () => {
  render(<App backend={treeBackend()} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Tree" }))
  const tree = await screen.findByRole("tree", { name: "Tree of /" })
  const photosItem = within(tree).getByRole("treeitem", { name: /photos/ })
  const subItem = within(photosItem).getByRole("treeitem", { name: /2024/ })
  expect(within(subItem).getByRole("treeitem", { name: /beach\.jpg/ })).toBeTruthy()
  expect(within(tree).getByRole("treeitem", { name: /notes\.txt/ })).toBeTruthy()
})

test("a directory change from another process refreshes the shown listing", async () => {
  const backend = memoryBackend(seed)
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  backend.emitDirectoryChanged({
    path: "/",
    entries: [{ name: "from-cli.txt", path: "/from-cli.txt", type: "file", size: 10, date: "2026-02-01T00:00:00Z" }],
  })
  await screen.findByText("from-cli.txt")
  expect(screen.queryByText("notes.txt")).toBeNull()
})

test("a directory change for a path that is not shown does not clobber the listing", async () => {
  const backend = memoryBackend(seed)
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  backend.emitDirectoryChanged({ path: "/photos", entries: [] })
  expect(screen.getByRole("list", { name: "Files in /" })).toBeTruthy()
  expect(screen.getByText("notes.txt")).toBeTruthy()
})

test("the toolbar and sheets are translated", async () => {
  render(<App backend={memoryBackend(seed)} languages={["zh-CN"]} />)
  await screen.findByRole("list")

  expect(screen.getByRole("button", { name: "新建文件夹" })).toBeTruthy()
  expect(screen.getByRole("button", { name: "重新扫描" })).toBeTruthy()
})

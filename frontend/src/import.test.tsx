import { afterEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react"

import { App } from "@/App"
import type { ImportOutcome } from "@/backend"
import { memoryBackend, type MemoryImport } from "@/testing/memory-backend"

afterEach(cleanup)

const plan: ImportOutcome = {
  dry_run: true,
  into: "/saved",
  photos_as: "document",
  history_complete: true,
  imported: 1,
  skipped: 1,
  failed: 1,
  duplicates: 1,
  captions_merged: 0,
  sources_deleted: 0,
  photos: 0,
  items: [
    { message_id: 101, kind: "document", action: "import", path: "/saved/clip.mp4", size: 11 },
    { message_id: 102, kind: "document", action: "skip", path: "", reason: "content already in tree", duplicate_of: "/clip.mp4" },
    { message_id: 103, kind: "document", action: "fail", path: "", error: "download failed" },
  ],
}

function backendWith(imp: MemoryImport) {
  return memoryBackend({ "/": [] }, { import: imp })
}

/** Radix tab triggers activate on mouseDown, not click. */
function switchTab(name: string) {
  fireEvent.mouseDown(screen.getByRole("tab", { name }))
}

async function openImportTab(backend: ReturnType<typeof memoryBackend>) {
  render(<App backend={backend} languages={["en"]} />)
  // The seeded drive is empty; readiness is the tab bar, not a listing.
  await screen.findByRole("tab", { name: "Drive" })
  switchTab("Import")
  await screen.findByText("Import from Saved Messages")
}

test("the preview renders each plan item with its outcome", async () => {
  await openImportTab(backendWith({ plan }))

  fireEvent.click(screen.getByRole("button", { name: "Preview" }))

  await screen.findByText("Plan: 1 to import, 1 skipped (1 duplicates).")
  const items = screen.getByRole("list", { name: "Import items" })
  const rows = within(items).getAllByRole("listitem")
  expect(rows).toHaveLength(3)
  expect(rows[0].textContent).toContain("#101")
  // A dry run speaks in planned actions, not past-tense outcomes.
  expect(rows[0].textContent).toContain("Import")
  expect(rows[0].textContent).not.toContain("Imported")
  expect(rows[0].textContent).toContain("/saved/clip.mp4")
  expect(rows[1].textContent).toContain("Skip")
  expect(rows[1].textContent).toContain("duplicate of /clip.mp4")
  expect(rows[2].textContent).toContain("Failed")
  expect(rows[2].textContent).toContain("download failed")
})

test("running an import is blocked by a confirmation sheet until confirmed", async () => {
  await openImportTab(backendWith({ plan }))

  fireEvent.click(screen.getByRole("button", { name: "Import…" }))
  const dialog = screen.getByRole("dialog", { name: "Import Saved Messages" })
  // Not running yet: no summary, no progress.
  expect(screen.queryByText(/imported, 1 skipped/)).toBeNull()

  // Cancelling keeps everything as it was.
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }))
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(screen.queryByRole("list", { name: "Import items" })).toBeNull()

  // Confirming runs the import and renders per-item outcomes.
  fireEvent.click(screen.getByRole("button", { name: "Import…" }))
  fireEvent.click(within(screen.getByRole("dialog", { name: "Import Saved Messages" })).getByRole("button", { name: "Import" }))
  await screen.findByText("1 imported, 1 skipped (1 duplicates), 1 failed.")
  const items = screen.getByRole("list", { name: "Import items" })
  expect(within(items).getAllByRole("listitem")).toHaveLength(3)
})

test("delete-source carries its warning into the confirmation sheet", async () => {
  await openImportTab(backendWith({ plan }))

  fireEvent.click(screen.getByRole("checkbox", { name: "Delete saved originals after import" }))
  fireEvent.click(screen.getByRole("button", { name: "Import…" }))
  const dialog = screen.getByRole("dialog", { name: "Import Saved Messages" })
  expect(dialog.textContent).toContain("saved originals of imported and duplicate items will be deleted")
})

test("the photo-storage choice arrives as a prompt and answering it completes the run", async () => {
  await openImportTab(
    backendWith({
      plan: { ...plan, photos: 1, items: (plan.items ?? []).slice(0, 1), imported: 1, skipped: 0, failed: 0, duplicates: 0 },
      photoCount: 1,
    }),
  )

  fireEvent.click(screen.getByRole("button", { name: "Import…" }))
  fireEvent.click(within(screen.getByRole("dialog", { name: "Import Saved Messages" })).getByRole("button", { name: "Import" }))

  // The run blocks on the photo prompt.
  const prompt = await screen.findByRole("dialog", { name: "Republish photos as" })
  expect(prompt.textContent).toContain("1 photo message(s)")
  fireEvent.click(within(prompt).getByRole("button", { name: "Document" }))

  await screen.findByText(/1 imported, 0 skipped/)
})

test("cancelling the photo prompt surfaces the cancellation", async () => {
  await openImportTab(backendWith({ plan: { ...plan, photos: 1 }, photoCount: 1 }))

  fireEvent.click(screen.getByRole("button", { name: "Import…" }))
  fireEvent.click(within(screen.getByRole("dialog", { name: "Import Saved Messages" })).getByRole("button", { name: "Import" }))
  const prompt = await screen.findByRole("dialog", { name: "Republish photos as" })
  fireEvent.click(within(prompt).getByRole("button", { name: "Cancel" }))

  const alert = await screen.findByRole("alert")
  expect(alert.textContent).toContain("ERR_CANCELLED")
})

test("opening the confirmation sheet keeps the previewed plan on screen", async () => {
  await openImportTab(backendWith({ plan }))

  fireEvent.click(screen.getByRole("button", { name: "Preview" }))
  await screen.findByText("Plan: 1 to import, 1 skipped (1 duplicates).")

  fireEvent.click(screen.getByRole("button", { name: "Import…" }))
  expect(screen.getByRole("dialog", { name: "Import Saved Messages" })).toBeTruthy()
  // Nothing is planning while the sheet waits on the user.
  expect(screen.queryByText("Planning…")).toBeNull()
  expect(screen.getByText("Plan: 1 to import, 1 skipped (1 duplicates).")).toBeTruthy()

  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }))
  expect(screen.getByText("Plan: 1 to import, 1 skipped (1 duplicates).")).toBeTruthy()
})

test("plan rows name each item's kind in the interface language", async () => {
  const withKinds: ImportOutcome = {
    ...plan,
    items: [
      { message_id: 101, kind: "document", action: "import", path: "/saved/clip.mp4", size: 11 },
      { message_id: 102, kind: "photo", action: "import", path: "/saved/cat.jpg", size: 5 },
      { message_id: 103, kind: "hologram", action: "import", path: "/saved/x.bin", size: 5 },
    ],
  }
  await openImportTab(backendWith({ plan: withKinds }))

  fireEvent.click(screen.getByRole("button", { name: "Preview" }))
  const items = await screen.findByRole("list", { name: "Import items" })
  const rows = within(items).getAllByRole("listitem")
  expect(rows[0].textContent).toContain("Document")
  expect(rows[1].textContent).toContain("Photo")
  // A kind this build has no string for shows as the service wrote it.
  expect(rows[2].textContent).toContain("hologram")
})

test("an unconfirmed run is rejected by the backend gate", async () => {
  const backend = backendWith({ plan })
  await expect(backend.import.run({})).rejects.toMatchObject({ code: "ERR_CONFIRMATION_REQUIRED" })
  await expect(backend.import.preview({ delete_source: true })).rejects.toMatchObject({ code: "ERR_CONFIRMATION_REQUIRED" })
})

test("the import tab is translated", async () => {
  render(<App backend={backendWith({ plan })} languages={["zh-CN"]} />)
  await screen.findByRole("tab", { name: "云盘" })
  switchTab("导入")

  await screen.findByText("从收藏夹导入")
  expect(screen.getByRole("button", { name: "预览" })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "导入后删除收藏夹中的原件" })).toBeTruthy()

  fireEvent.click(screen.getByRole("button", { name: "预览" }))
  const items = await screen.findByRole("list", { name: "导入条目" })
  expect(within(items).getAllByRole("listitem")[0].textContent).toContain("文档")
})

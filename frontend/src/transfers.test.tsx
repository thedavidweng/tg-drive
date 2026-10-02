import { afterEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen, waitForElementToBeRemoved, within } from "@testing-library/react"

import { App } from "@/App"
import type { Transfer } from "@/backend"
import { memoryBackend, type MemoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

function transfer(over: Partial<Transfer> & { id: string }): Transfer {
  return {
    kind: "upload",
    stage: "uploading",
    channel: "1001",
    source: "/home/me/big.bin",
    dest: "/big.bin",
    bytes_done: 0,
    bytes_total: 0,
    items_done: 0,
    items_total: 1,
    front_end: "gui",
    cancel_requested: false,
    created_at: "2026-02-01T10:00:00Z",
    updated_at: "2026-02-01T10:00:00Z",
    ...over,
  }
}

async function openTransfers(backend: MemoryBackend) {
  render(<App backend={backend} languages={["en"]} />)
  // The auth gate resolves before the shell renders its tabs.
  const tab = await screen.findByRole("tab", { name: "Transfers" })
  fireEvent.mouseDown(tab)
  fireEvent.click(tab)
  await screen.findByRole("region", { name: "Active" })
}

function activeList(): HTMLElement {
  return screen.getByRole("list", { name: "Active transfers" })
}

function historyList(): HTMLElement {
  return screen.getByRole("list", { name: "Finished transfers" })
}

function rowIn(list: HTMLElement, name: string): HTMLElement {
  const el = within(list)
    .getAllByRole("listitem")
    .find((li) => li.textContent?.includes(name))
  if (!el) throw new Error(`no row for ${name}`)
  return el
}

test("stage pills and progress bars update on the typed events", async () => {
  const backend = memoryBackend({})
  const t1 = transfer({ id: "t1", bytes_total: 2048, bytes_done: 512 })
  backend.putTransferForTest(t1)
  await openTransfers(backend)

  const row = rowIn(activeList(), "/big.bin")
  expect(within(row).getByText("Uploading")).toBeTruthy()
  expect(row.textContent).toContain("512 B / 2 KB · 25%")
  expect(within(row).getByRole("progressbar").getAttribute("aria-valuenow")).toBe("25")

  // Byte progress moves the bar within the stage.
  backend.emitTransferProgress({ ...t1, bytes_done: 1536 })
  expect((await screen.findByText(/75%/)).textContent).toContain("1.5 KB / 2 KB · 75%")

  // A stage event re-pills the row and lands it in the history.
  backend.emitTransferStage({ ...t1, bytes_done: 2048, stage: "completed", finished_at: "2026-02-01T10:01:00Z" })
  await waitForElementToBeRemoved(() => screen.queryByRole("list", { name: "Active transfers" }))
  expect(within(rowIn(historyList(), "/big.bin")).getByText("Completed")).toBeTruthy()
})

test("multi-item transfers count items, not bytes", async () => {
  const backend = memoryBackend({})
  const t1 = transfer({
    id: "t1",
    kind: "album_upload",
    source: "",
    dest: "/photos",
    items_total: 4,
    items_done: 1,
  })
  backend.putTransferForTest(t1)
  await openTransfers(backend)

  expect(rowIn(activeList(), "/photos").textContent).toContain("1 / 4 items")
  backend.emitTransferProgress({ ...t1, items_done: 3 })
  await screen.findByText("3 / 4 items")
})

test("cancel on an active transfer calls the backend, which answers cancelled", async () => {
  const backend = memoryBackend({})
  const t1 = transfer({ id: "t1", bytes_total: 2048, bytes_done: 512 })
  backend.putTransferForTest(t1)
  await openTransfers(backend)

  fireEvent.click(screen.getByRole("button", { name: "Cancel /big.bin" }))
  await screen.findByText("Cancelled")
  expect(backend.cancelled).toEqual(["t1"])
  // The cancelled Transfer moved to the history.
  expect(within(rowIn(historyList(), "/big.bin")).getByText("Cancelled")).toBeTruthy()
})

test("a failed transfer shows the plain-language reason and the error code, and retry calls the backend", async () => {
  const backend = memoryBackend({})
  const t1 = transfer({
    id: "t1",
    stage: "failed",
    bytes_total: 2048,
    bytes_done: 512,
    error_code: "ERR_TELEGRAM",
    error_message: "telegram returned an error",
    finished_at: "2026-02-01T10:05:00Z",
  })
  backend.putTransferForTest(t1)
  await openTransfers(backend)

  const row = rowIn(historyList(), "/big.bin")
  expect(row.textContent).toContain("telegram returned an error")
  expect(row.textContent).toContain("ERR_TELEGRAM")
  expect(within(row).getByText("Failed")).toBeTruthy()

  fireEvent.click(within(row).getByRole("button", { name: "Retry /big.bin" }))
  // The memory backend retries to completion; the row shows the new run.
  await screen.findByText("Completed")
  expect(backend.retried).toEqual(["t1"])
})

test("a transfer another front end runs wears the CLI badge", async () => {
  const backend = memoryBackend({})
  backend.putTransferForTest(transfer({ id: "t1", front_end: "cli", dest: "/from-cli.bin" }))
  await openTransfers(backend)

  expect(rowIn(activeList(), "/from-cli.bin").textContent).toContain("CLI")
})

test("a transfer-removed event drops the row", async () => {
  const backend = memoryBackend({})
  backend.putTransferForTest(
    transfer({ id: "t1", stage: "completed", finished_at: "2026-02-01T10:01:00Z" }),
  )
  await openTransfers(backend)
  expect(rowIn(historyList(), "/big.bin")).toBeTruthy()

  backend.emitTransferRemoved({ id: "t1" })
  await waitForElementToBeRemoved(() => screen.queryByText("/big.bin"))
})

test("clear finished empties the history through the backend", async () => {
  const backend = memoryBackend({})
  backend.putTransferForTest(transfer({ id: "t1", stage: "completed", finished_at: "2026-02-01T10:01:00Z" }))
  backend.putTransferForTest(
    transfer({ id: "t2", stage: "failed", dest: "/oops.bin", error_code: "ERR_X", error_message: "boom" }),
  )
  backend.putTransferForTest(transfer({ id: "t3", dest: "/running.bin" }))
  await openTransfers(backend)

  fireEvent.click(screen.getByRole("button", { name: "Clear finished" }))
  // The memory backend clears and emits the removals inside the click.
  await screen.findByText("No finished transfers yet.")
  expect(screen.queryByText("/oops.bin")).toBeNull()
  expect(screen.queryByText("/big.bin")).toBeNull()
  // The still-running transfer stays.
  expect(rowIn(activeList(), "/running.bin")).toBeTruthy()
})

test("the tab is translated", async () => {
  const backend = memoryBackend({})
  backend.putTransferForTest(
    transfer({ id: "t1", stage: "completed", finished_at: "2026-02-01T10:01:00Z" }),
  )
  render(<App backend={backend} languages={["zh-CN"]} />)
  const tab = await screen.findByRole("tab", { name: "传输" })
  fireEvent.mouseDown(tab)
  fireEvent.click(tab)

  const history = await screen.findByRole("list", { name: "已完成的传输" })
  expect(within(history).getByText("已完成")).toBeTruthy()
  expect(history.textContent).toContain("上传")
})

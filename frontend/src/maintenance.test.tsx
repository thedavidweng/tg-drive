import { afterEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react"

import { App } from "@/App"
import type { AdoptOutcome, DoctorReport, RepairOutcome } from "@/backend"
import { memoryBackend, type MemoryMaintenance } from "@/testing/memory-backend"

afterEach(cleanup)

const adoptPlan: AdoptOutcome = {
  dry_run: true,
  adopted: 1,
  skipped: 1,
  failed: 0,
  deleted: 0,
  items: [
    { message_id: 501, kind: "file", file_name: "old-report.pdf", path: "/files/old-report.pdf", size: 9, action: "adopt" },
    { message_id: 1, kind: "file", file_name: "notes.txt", path: "/files/notes.txt", size: 5, action: "skip", reason: "already indexed" },
  ],
}

const adoptOutcome: AdoptOutcome = { ...adoptPlan, dry_run: false }

const captionsOutcome: RepairOutcome = {
  mode: "captions",
  captions: {
    dry_run: true,
    total: 2,
    planned: 1,
    cleaned: 0,
    skipped: 1,
    failed: 0,
    items: [
      { path: "/notes.txt", message_id: 1, action: "planned", reason: "" },
      { path: "/plain.bin", message_id: 7, action: "skipped", reason: "already clean or scaffold not exact" },
    ],
  },
}

const doctorReport: DoctorReport = {
  checks: [
    { name: "auth", status: "pass" },
    { name: "db_wal", status: "fail", hint: "journal_mode is \"delete\", not WAL" },
    { name: "discussion", status: "warn", hint: "no linked discussion group" },
    { name: "upload", status: "pass" },
  ],
  max_upload_bytes: 2147483648,
}

function backendWith(maintenance: MemoryMaintenance) {
  return memoryBackend({ "/": [] }, { maintenance })
}

/** Radix tab triggers activate on mouseDown, not click. */
function switchTab(name: string) {
  fireEvent.mouseDown(screen.getByRole("tab", { name }))
}

async function openMaintenanceTab(backend: ReturnType<typeof memoryBackend>) {
  render(<App backend={backend} languages={["en"]} />)
  // The seeded drive is empty; readiness is the tab bar, not a listing.
  await screen.findByRole("tab", { name: "Drive" })
  switchTab("Maintenance")
  await screen.findByText("Adopt existing messages")
}

test("adopt preview renders the plan; the run waits for its confirmation sheet", async () => {
  await openMaintenanceTab(backendWith({ adoptPlan, adoptOutcome }))

  fireEvent.click(screen.getByRole("button", { name: "Preview" }))
  await screen.findByText("Plan: 1 to adopt, 1 skipped.")
  const items = screen.getByRole("list", { name: "Adopt items" })
  const rows = within(items).getAllByRole("listitem")
  expect(rows[0].textContent).toContain("old-report.pdf")
  expect(rows[0].textContent).toContain("Adopt")
  expect(rows[1].textContent).toContain("Skip")

  fireEvent.click(screen.getByRole("button", { name: "Adopt…" }))
  const dialog = screen.getByRole("dialog", { name: "Adopt messages" })
  expect(screen.queryByText("1 adopted, 1 skipped, 0 failed.")).toBeNull()

  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }))
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(screen.queryByText("1 adopted, 1 skipped, 0 failed.")).toBeNull()

  fireEvent.click(screen.getByRole("button", { name: "Adopt…" }))
  fireEvent.click(within(screen.getByRole("dialog", { name: "Adopt messages" })).getByRole("button", { name: "Adopt" }))
  await screen.findByText("1 adopted, 1 skipped, 0 failed.")
})

test("an unconfirmed adopt is rejected by the backend gate", async () => {
  const backend = backendWith({ adoptOutcome })
  await expect(backend.maintenance.adopt({})).rejects.toMatchObject({ code: "ERR_CONFIRMATION_REQUIRED" })
})

test("a captions dry run renders its counters and per-item rows", async () => {
  await openMaintenanceTab(backendWith({ repair: { captions: captionsOutcome } }))

  fireEvent.click(screen.getByRole("button", { name: "Captions" }))
  fireEvent.click(screen.getByRole("checkbox", { name: "Dry run" }))
  fireEvent.click(screen.getByRole("button", { name: "Run repair" }))

  await screen.findByText(/2 total/)
  const items = screen.getByRole("list", { name: "Repair items" })
  const rows = within(items).getAllByRole("listitem")
  expect(rows[0].textContent).toContain("/notes.txt")
  expect(rows[0].textContent).toContain("planned")
  expect(rows[1].textContent).toContain("already clean or scaffold not exact")
})

test("deleting orphaned messages is blocked by a confirmation sheet", async () => {
  const backend = backendWith({
    repair: { orphaned: { mode: "orphaned", orphaned: { repaired: 0, deleted: 2, invalid: 0 } } },
  })
  await openMaintenanceTab(backend)

  fireEvent.click(screen.getByRole("button", { name: "Orphaned" }))
  fireEvent.click(screen.getByRole("checkbox", { name: "Delete orphaned messages instead of completing them" }))
  fireEvent.click(screen.getByRole("button", { name: "Run repair" }))

  // Nothing ran: the sheet must confirm first.
  const dialog = screen.getByRole("dialog", { name: "Delete orphaned messages" })
  expect(dialog.textContent).toContain("cannot be undone")
  expect(screen.queryByText(/2 deleted/)).toBeNull()

  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }))
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(screen.queryByText(/2 deleted/)).toBeNull()

  fireEvent.click(screen.getByRole("button", { name: "Run repair" }))
  fireEvent.click(within(screen.getByRole("dialog", { name: "Delete orphaned messages" })).getByRole("button", { name: "Delete orphans" }))
  await screen.findByText(/2 deleted/)
})

test("the delete-orphans gate also rejects unconfirmed calls directly", async () => {
  const backend = backendWith({})
  await expect(
    backend.maintenance.repair({ mode: "orphaned", delete_orphaned: true }),
  ).rejects.toMatchObject({ code: "ERR_CONFIRMATION_REQUIRED" })
})

test("repairing a single file requires its path", async () => {
  await openMaintenanceTab(backendWith({}))
  fireEvent.click(screen.getByRole("button", { name: "Single file" }))

  expect((screen.getByRole("button", { name: "Run repair" }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.change(screen.getByRole("textbox", { name: "Path" }), { target: { value: "/notes.txt" } })
  expect((screen.getByRole("button", { name: "Run repair" }) as HTMLButtonElement).disabled).toBe(false)
})

test("doctor renders each check as pass, warn, or fail", async () => {
  await openMaintenanceTab(backendWith({ doctor: doctorReport }))

  fireEvent.click(screen.getByRole("button", { name: "Run checks" }))

  await screen.findByText("2 passed, 1 warnings, 1 failed")
  const checks = screen.getByRole("list", { name: "Capability checks" })
  const rows = within(checks).getAllByRole("listitem")
  expect(rows.length).toBe(5) // four checks plus the max-upload row
  const row = (name: string) => {
    const el = rows.find((r) => r.textContent?.startsWith(name))
    if (!el) throw new Error(`no check row for ${name}`)
    return el
  }
  expect(within(row("auth")).getByText("Pass")).toBeTruthy()
  expect(within(row("discussion")).getByText("Warn")).toBeTruthy()
  expect(row("discussion").textContent).toContain("no linked discussion group")
  expect(within(row("db_wal")).getByText("Fail")).toBeTruthy()
  expect(row("db_wal").textContent).toContain("not WAL")
  expect(within(row("upload")).getByText("Pass")).toBeTruthy()
  expect(row("max_upload").textContent).toContain("2 GB")

  // The path-codec doctor renders beside the checks.
  const codec = screen.getByRole("list", { name: "Path codec" })
  expect(within(codec).getByText("Fixed vectors")).toBeTruthy()
  expect(within(codec).getAllByText("Pass").length).toBe(2)
})

test("the maintenance tab is translated", async () => {
  render(<App backend={backendWith({})} languages={["zh-CN"]} />)
  await screen.findByRole("tab", { name: "云盘" })
  switchTab("维护")

  await screen.findByText("收纳现有消息")
  expect(screen.getByText("修复")).toBeTruthy()
  expect(screen.getByRole("button", { name: "运行检查" })).toBeTruthy()
})

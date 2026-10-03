import { afterEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen, waitForElementToBeRemoved, within } from "@testing-library/react"

import { App } from "@/App"
import { memoryBackend } from "@/testing/memory-backend"

afterEach(() => {
  cleanup()
  // The switcher persists the selected drive in localStorage; keep tests
  // independent of each other's selection.
  localStorage.clear()
})

const twoChannels = [
  { id: "1001", title: "Drive", discussion: "Drive Discussion" },
  { id: "2002", title: "Archive" },
]

test("the channel switcher changes the Drive view to the selected channel", async () => {
  const backend = memoryBackend({}, { channels: twoChannels })
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  backend.putFileForTest("/contract.pdf", 100, "2026-01-02T00:00:00Z", "2002")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })
  expect(screen.getByText("notes.txt")).toBeTruthy()

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  fireEvent.click(within(dialog).getByRole("button", { name: "Switch to Archive" }))

  // The Drive view resets to the new channel's root and lists its files.
  await screen.findByText("contract.pdf")
  expect(screen.queryByText("notes.txt")).toBeNull()
  expect(screen.getByRole("button", { name: "Switch drive" }).textContent).toContain("Archive")
})

test("the switcher sheet shows the active channel's status", async () => {
  const backend = memoryBackend({}, { channels: twoChannels })
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  backend.putFileForTest("/photo.jpg", 99, "2026-01-01T00:00:00Z")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })

  // The active channel is marked, the other is a switch action.
  const driveRow = within(dialog).getByText("Drive").closest("li")!
  expect(driveRow.textContent).toContain("Active")
  expect(within(dialog).getByRole("button", { name: "Switch to Archive" })).toBeTruthy()

  // Status: discussion group, upload limit, freshness, file count.
  await within(dialog).findByText("Drive Discussion")
  expect(within(dialog).getByText("Discussion group")).toBeTruthy()
  expect(within(dialog).getByText("2 GB")).toBeTruthy()
  expect(within(dialog).getByText("Last scan")).toBeTruthy()
  expect(within(dialog).getByText("2 files indexed")).toBeTruthy()
})

test("linking a discussion group updates the channel status", async () => {
  const backend = memoryBackend({}, { channels: [{ id: "1001", title: "Drive" }] })
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  await within(dialog).findByText("Not linked")

  fireEvent.click(within(dialog).getByRole("button", { name: "Link discussion group" }))
  await within(dialog).findByText("Drive Discussion")
  expect(within(dialog).queryByText("Not linked")).toBeNull()
})

test("binding a listed Telegram channel makes it the active drive", async () => {
  const backend = memoryBackend({}, { channels: [{ id: "1001", title: "Drive" }] })
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  fireEvent.click(within(dialog).getByRole("button", { name: "Bind or create a drive" }))

  const bindDialog = await screen.findByRole("dialog", { name: "Bind or create a drive" })
  // Drive is already bound; Archive can be bound.
  const boundRow = within(bindDialog).getByText("Drive").closest("li")!
  expect(boundRow.textContent).toContain("Bound")
  fireEvent.click(within(bindDialog).getByRole("button", { name: "Bind Archive" }))

  // The dialog closes, the header shows the new drive, the view is empty.
  await waitForElementToBeRemoved(() => screen.queryByRole("dialog"))
  expect(screen.getByRole("button", { name: "Switch drive" }).textContent).toContain("Archive")
  expect(await screen.findByText("This folder is empty.")).toBeTruthy()
})

test("creating a channel with the default title on a fresh machine", async () => {
  const backend = memoryBackend({}, { channels: [] })
  render(<App backend={backend} languages={["en"]} />)

  // Nothing bound: the Drive view explains, the switcher offers setup.
  await screen.findByRole("alert")
  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  expect(within(dialog).getByText("No drive bound.")).toBeTruthy()

  fireEvent.click(within(dialog).getByRole("button", { name: "Bind or create a drive" }))
  const bindDialog = await screen.findByRole("dialog", { name: "Bind or create a drive" })
  const input = within(bindDialog).getByRole("textbox", { name: "Channel title" }) as HTMLInputElement
  expect(input.value).toBe("Drive")
  fireEvent.click(within(bindDialog).getByRole("button", { name: "Create and bind" }))

  await waitForElementToBeRemoved(() => screen.queryByRole("dialog"))
  expect(screen.getByRole("button", { name: "Switch drive" }).textContent).toContain("Drive")
  expect(await screen.findByText("This folder is empty.")).toBeTruthy()
})

test("a channel bound in another front end appears in the open switcher sheet", async () => {
  const backend = memoryBackend({}, { channels: [{ id: "1001", title: "Drive" }] })
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  expect(within(dialog).queryByRole("button", { name: "Switch to Photos" })).toBeNull()

  // td init in a terminal bound a second channel; Go announces the new list.
  backend.emitChannelsChanged({
    channels: [
      { channel_id: "1001", title: "Drive", local_root: "/data/drives/Drive", active: true },
      { channel_id: "3003", title: "Photos", local_root: "/data/drives/Photos", active: false },
    ],
  })
  expect(await within(dialog).findByRole("button", { name: "Switch to Photos" })).toBeTruthy()
})

test("the status view shows the last full scan and checks capabilities on demand", async () => {
  const backend = memoryBackend(
    {},
    {
      channels: [{ id: "1001", title: "Drive", discussion: "Drive Discussion" }],
      maintenance: {
        doctor: {
          checks: [
            { name: "edit_old_caption", status: "pass" },
            { name: "invite_link", status: "warn", hint: "the account cannot create invite links" },
          ],
          max_upload_bytes: 2147483648,
        },
      },
    },
  )
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  await within(dialog).findByText("Drive Discussion")
  expect(within(dialog).getByText("Last full scan")).toBeTruthy()
  // Capabilities are probed only when asked: the probe talks to Telegram.
  expect(within(dialog).queryByRole("list", { name: "Capability checks" })).toBeNull()

  fireEvent.click(within(dialog).getByRole("button", { name: "Check capabilities" }))
  const checks = await within(dialog).findByRole("list", { name: "Capability checks" })
  const rows = within(checks).getAllByRole("listitem")
  expect(rows).toHaveLength(2)
  expect(rows[0].textContent).toContain("edit_old_caption")
  expect(within(rows[0]).getByText("Pass")).toBeTruthy()
  expect(within(rows[1]).getByText("Warn")).toBeTruthy()
  expect(rows[1].textContent).toContain("the account cannot create invite links")
})

test("the switcher sits with the header actions, after the tabs", async () => {
  const backend = memoryBackend({}, { channels: twoChannels })
  render(<App backend={backend} languages={["en"]} />)
  const switcher = await screen.findByRole("button", { name: "Switch drive" })
  const tabs = screen.getByRole("tablist", { name: "Sections" })
  // Reading order puts the switcher after the tab strip, beside the account.
  expect(tabs.compareDocumentPosition(switcher) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(switcher.parentElement?.contains(screen.getByRole("button", { name: "Log out" }))).toBe(true)
})

test("switching drives clears Import and Maintenance results of the previous drive", async () => {
  const backend = memoryBackend(
    {},
    {
      channels: twoChannels,
      import: {
        plan: {
          dry_run: true,
          into: "/saved",
          history_complete: true,
          imported: 1,
          skipped: 0,
          failed: 0,
          duplicates: 0,
          captions_merged: 0,
          sources_deleted: 0,
          photos: 0,
          items: [{ message_id: 101, kind: "document", action: "import", path: "/saved/clip.mp4", size: 11 }],
        },
      },
    },
  )
  render(<App backend={backend} languages={["en"]} />)
  fireEvent.mouseDown(await screen.findByRole("tab", { name: "Import" }))
  fireEvent.click(await screen.findByRole("button", { name: "Preview" }))
  await screen.findByText("Plan: 1 to import, 0 skipped (0 duplicates).")

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  fireEvent.click(within(dialog).getByRole("button", { name: "Switch to Archive" }))
  await waitForElementToBeRemoved(() => screen.queryByRole("dialog"))

  // The plan was for the previous drive's channel; it does not carry over.
  expect(screen.queryByText("Plan: 1 to import, 0 skipped (0 duplicates).")).toBeNull()
  expect(screen.getByText("Import from Saved Messages")).toBeTruthy()

  // The same holds for Maintenance's diagnostics.
  fireEvent.mouseDown(screen.getByRole("tab", { name: "Maintenance" }))
  fireEvent.click(await screen.findByRole("button", { name: "Run checks" }))
  await screen.findByText("7 passed, 0 warnings, 0 failed")
  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const again = await screen.findByRole("dialog", { name: "Drives" })
  fireEvent.click(within(again).getByRole("button", { name: "Switch to Drive" }))
  await waitForElementToBeRemoved(() => screen.queryByRole("dialog"))
  expect(screen.queryByText("7 passed, 0 warnings, 0 failed")).toBeNull()
})

test("the switcher and sheets are translated", async () => {
  const backend = memoryBackend({}, { channels: twoChannels })
  render(<App backend={backend} languages={["zh-CN"]} />)
  await screen.findByRole("button", { name: "切换云盘" })

  fireEvent.click(screen.getByRole("button", { name: "切换云盘" }))
  const dialog = await screen.findByRole("dialog", { name: "云盘" })
  expect(within(dialog).getByRole("button", { name: "切换到 Archive" })).toBeTruthy()
  await within(dialog).findByText("讨论组")
  expect(within(dialog).getByRole("button", { name: "绑定或创建云盘" })).toBeTruthy()
})

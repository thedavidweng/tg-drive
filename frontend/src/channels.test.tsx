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

test("the channel status reports the account's permissions", async () => {
  const backend = memoryBackend({}, { channels: twoChannels })
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  await within(dialog).findByText("Permissions")
  expect(within(dialog).getByText("All granted")).toBeTruthy()
})

test("the channel status names the missing permissions", async () => {
  const backend = memoryBackend(
    {},
    { channels: [{ id: "1001", title: "Drive", discussion: "Drive Discussion", denied: ["delete", "invite"] }] },
  )
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  await within(dialog).findByText("Missing: delete messages, invite links")
})

test("a failed permission probe does not hide the channel status", async () => {
  const backend = memoryBackend(
    {},
    { channels: [{ id: "1001", title: "Drive", discussion: "Drive Discussion", probeFailed: true }] },
  )
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })
  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  await within(dialog).findByText("Unavailable")
  expect(within(dialog).getByText("Drive Discussion")).toBeTruthy()
  expect(within(dialog).getByText("1 files indexed")).toBeTruthy()
})

test("a channel bound by another front end appears without a reload", async () => {
  const backend = memoryBackend({}, { channels: [{ id: "1001", title: "Drive" }] })
  backend.putFileForTest("/notes.txt", 5, "2026-01-01T00:00:00Z")
  render(<App backend={backend} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Switch drive" }))
  const dialog = await screen.findByRole("dialog", { name: "Drives" })
  expect(within(dialog).queryByRole("button", { name: "Switch to Archive" })).toBeNull()

  // td init --bind-channel in a terminal: index sync emits channels-changed.
  await backend.bindExternally({ id: "2002", title: "Archive" })
  await within(dialog).findByRole("button", { name: "Switch to Archive" })
})

import { afterEach, expect, test } from "bun:test"
import { cleanup, render, screen, within } from "@testing-library/react"

import { App } from "@/App"
import { memoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

test("the Drive tab lists the root of the bound channel", async () => {
  const backend = memoryBackend({
    "/": [
      { name: "photos", path: "/photos", type: "dir", size: 0, date: "2026-01-01T00:00:00Z" },
      { name: "notes.txt", path: "/notes.txt", type: "file", size: 2048, date: "2026-01-01T00:00:00Z" },
    ],
  })
  render(<App backend={backend} languages={["en-US"]} />)

  const list = await screen.findByRole("list", { name: "Files in /" })
  const rows = within(list).getAllByRole("listitem")
  expect(rows.some((r) => r.textContent?.includes("photos"))).toBe(true)
  expect(rows.some((r) => r.textContent?.includes("notes.txt") && r.textContent?.includes("2 KB"))).toBe(true)
})

test("an empty root says so", async () => {
  render(<App backend={memoryBackend({ "/": [] })} languages={["en"]} />)

  expect(await screen.findByText("This folder is empty.")).toBeTruthy()
})

test("a failed listing shows the service error", async () => {
  const backend = memoryBackend({}, {
    driveError: {
      code: "ERR_CHANNEL_NOT_FOUND",
      category: "api",
      message: "no channel bound in this database",
    },
  })
  render(<App backend={backend} languages={["en"]} />)

  const alert = await screen.findByRole("alert")
  expect(alert.textContent).toContain("no channel bound in this database")
  expect(alert.textContent).toContain("ERR_CHANNEL_NOT_FOUND")
})

test("an unknown system language falls back to English", async () => {
  render(<App backend={memoryBackend({ "/": [] })} languages={["fr-FR", "de"]} />)

  const tabs = (await screen.findAllByRole("tab")).map((t) => t.textContent)
  expect(tabs).toEqual(["Drive", "Transfers", "Import", "Maintenance", "Settings"])
  expect(await screen.findByText("This folder is empty.")).toBeTruthy()
})

test("a Chinese system language selects the Simplified Chinese catalogue", async () => {
  render(<App backend={memoryBackend({ "/": [] })} languages={["fr-FR", "zh-Hans-CN"]} />)

  const tabs = (await screen.findAllByRole("tab")).map((t) => t.textContent)
  expect(tabs).toEqual(["云盘", "传输", "导入", "维护", "设置"])
  expect(await screen.findByText("此文件夹为空。")).toBeTruthy()
})

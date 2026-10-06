import { afterEach, expect, test } from "bun:test"
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react"

import { App } from "@/App"
import type { PreviewDescriptor } from "@/backend"
import { previewProviders } from "@/preview/providers"
import { choosePreview, fallbackPreview } from "@/preview/registry"
import { memoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

const seed = {
  "/": [
    { name: "photos", path: "/photos", type: "dir" as const, size: 0, date: "2026-01-01T00:00:00Z" },
    { name: "archive.zip", path: "/archive.zip", type: "file" as const, size: 4096, date: "2026-01-02T00:00:00Z" },
  ],
  "/photos": [{ name: "cat.png", path: "/photos/cat.png", type: "file" as const, size: 68, date: "2026-01-03T00:00:00Z" }],
}

// A 1×1 PNG, the bytes the memory backend serves as a data URL.
const png = Uint8Array.from(
  atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="),
  (c) => c.charCodeAt(0),
)

function backendWithPhoto() {
  const backend = memoryBackend(seed, { transfers: { picks: { dir: "/home/me/Downloads" } } })
  backend.putPreviewForTest("/photos/cat.png", { mime: "image/png", data: png })
  return backend
}

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

async function openPhotos() {
  fireEvent.click(await screen.findByRole("button", { name: "photos" }))
  return screen.findByRole("list", { name: "Files in /photos" })
}

test("the registry picks a provider by MIME first, then by extension, else the fallback", () => {
  const pick = (name: string, mime: string) => choosePreview(descriptor(name, mime), previewProviders).id
  expect(pick("cat.png", "image/png")).toBe("image")
  // The indexed MIME wins over a misleading extension.
  expect(pick("cat.dat", "image/webp")).toBe("image")
  expect(pick("logo.svg", "image/svg+xml")).toBe("image")
  // No useful MIME: the extension decides, case-insensitively.
  expect(pick("IMG_0001.JPEG", "application/octet-stream")).toBe("image")
  expect(pick("anim.gif", "")).toBe("image")
  // Nothing matches: the fallback, never undefined.
  expect(pick("archive.zip", "application/zip")).toBe(fallbackPreview.id)
  expect(pick("photo.heic", "image/heic")).toBe(fallbackPreview.id)
})

test("activating a file name opens its image preview; a directory still navigates", async () => {
  const backend = backendWithPhoto()
  render(<App backend={backend} languages={["en"]} />)
  const list = await openPhotos()

  fireEvent.click(within(list).getByRole("button", { name: "cat.png" }))
  const dialog = await screen.findByRole("dialog", { name: "cat.png" })
  const img = await within(dialog).findByRole("img", { name: "cat.png" })
  expect(img.getAttribute("src")).toStartWith("data:image/png;base64,")
  // The header names the file and carries its metadata and actions.
  expect(within(dialog).getByRole("heading", { name: "cat.png" })).toBeTruthy()
  expect(dialog.textContent).toContain("PNG")
  expect(dialog.textContent).toContain("68 B")
  expect(within(dialog).getByRole("button", { name: "Download" })).toBeTruthy()
  expect(within(dialog).getByRole("button", { name: "Close preview" })).toBeTruthy()
  expect(backend.previewed).toEqual(["/photos/cat.png"])
})

test("the Preview row action opens the preview, and Close returns focus and the directory", async () => {
  render(<App backend={backendWithPhoto()} languages={["en"]} />)
  const list = await openPhotos()
  const trigger = within(list).getByRole("button", { name: "Preview" })
  act(() => trigger.focus())

  fireEvent.click(trigger)
  const dialog = await screen.findByRole("dialog", { name: "cat.png" })
  expect(dialog.contains(document.activeElement)).toBe(true)

  fireEvent.click(within(dialog).getByRole("button", { name: "Close preview" }))
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(document.activeElement === trigger).toBe(true)
  // Closing never reloads or leaves the directory being shown.
  expect(screen.getByRole("list", { name: "Files in /photos" })).toBe(list)
})

test("Escape closes the preview and traps Tab, but not while a viewer is fullscreen", async () => {
  render(<App backend={backendWithPhoto()} languages={["en"]} />)
  const list = await openPhotos()
  const trigger = within(list).getByRole("button", { name: "cat.png" })
  act(() => trigger.focus())
  fireEvent.click(trigger)
  const dialog = await screen.findByRole("dialog", { name: "cat.png" })

  const controls = Array.from(dialog.querySelectorAll<HTMLElement>("button"))
  act(() => controls[controls.length - 1].focus())
  fireEvent.keyDown(controls[controls.length - 1], { key: "Tab" })
  expect(document.activeElement === controls[0]).toBe(true)

  Object.defineProperty(document, "fullscreenElement", { configurable: true, get: () => dialog })
  try {
    fireEvent.keyDown(document.activeElement!, { key: "Escape" })
    expect(screen.getByRole("dialog", { name: "cat.png" })).toBe(dialog)
  } finally {
    Object.defineProperty(document, "fullscreenElement", { configurable: true, get: () => null })
  }

  fireEvent.keyDown(document.activeElement!, { key: "Escape" })
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(document.activeElement === trigger).toBe(true)
})

test("an unsupported file shows the fallback with its details and Download", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  fireEvent.click(await screen.findByRole("button", { name: "archive.zip" }))
  const dialog = await screen.findByRole("dialog", { name: "archive.zip" })

  await within(dialog).findByText("No preview is available for this file type.")
  expect(dialog.textContent).toContain("/archive.zip")
  expect(dialog.textContent).toContain("4 KB")
  expect(within(dialog).getAllByRole("button", { name: "Download" }).length).toBeGreaterThan(0)
  expect(within(dialog).queryByRole("img")).toBeNull()
})

test("a failed preview call keeps Download, and Download opens the download sheet above the preview", async () => {
  const backend = backendWithPhoto()
  backend.failPreviewForTest("/photos/cat.png", {
    code: "ERR_TELEGRAM_RATE_LIMITED",
    category: "telegram",
    message: "telegram rate limited this account",
  })
  render(<App backend={backend} languages={["en"]} />)
  const list = await openPhotos()
  fireEvent.click(within(list).getByRole("button", { name: "cat.png" }))
  const dialog = await screen.findByRole("dialog", { name: "cat.png" })

  expect((await within(dialog).findByRole("alert")).textContent).toContain("telegram rate limited this account")
  fireEvent.click(within(dialog).getAllByRole("button", { name: "Download" })[0])
  const sheet = await screen.findByRole("dialog", { name: "Download" })

  // Escape belongs to the topmost dialog: it closes the sheet, not the preview.
  fireEvent.keyDown(document.activeElement!, { key: "Escape" })
  expect(screen.queryByRole("dialog", { name: "Download" })).toBeNull()
  expect(sheet.isConnected).toBe(false)
  expect(screen.getByRole("dialog", { name: "cat.png" })).toBe(dialog)
})

test("an image that fails to load falls back to file details and Download", async () => {
  render(<App backend={backendWithPhoto()} languages={["en"]} />)
  const list = await openPhotos()
  fireEvent.click(within(list).getByRole("button", { name: "cat.png" }))
  const dialog = await screen.findByRole("dialog", { name: "cat.png" })
  const img = await within(dialog).findByRole("img", { name: "cat.png" })

  fireEvent.error(img)
  await within(dialog).findByText("This file could not be previewed.")
  expect(within(dialog).queryByRole("img")).toBeNull()
  expect(within(dialog).getAllByRole("button", { name: "Download" }).length).toBeGreaterThan(0)
})

test("the Preview action and the preview controls speak Simplified Chinese", async () => {
  render(<App backend={backendWithPhoto()} languages={["zh-CN"]} />)
  fireEvent.click(await screen.findByRole("button", { name: "photos" }))
  const list = await screen.findByRole("list", { name: "/photos 中的文件" })
  fireEvent.click(within(list).getByRole("button", { name: "预览" }))
  const dialog = await screen.findByRole("dialog", { name: "cat.png" })
  expect(await within(dialog).findByRole("button", { name: "关闭预览" })).toBeTruthy()
})

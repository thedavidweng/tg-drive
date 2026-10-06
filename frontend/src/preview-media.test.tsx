import { afterEach, expect, test } from "bun:test"
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"

import { App } from "@/App"
import type { PreviewDescriptor } from "@/backend"
import { previewProviders } from "@/preview/providers"
import { choosePreview } from "@/preview/registry"
import { memoryBackend } from "@/testing/memory-backend"

afterEach(async () => {
  cleanup()
  if (document.fullscreenElement) await document.exitFullscreen()
})

const seed = {
  "/": [
    { name: "clip.mp4", path: "/clip.mp4", type: "file" as const, size: 9, date: "2026-01-02T00:00:00Z" },
    { name: "song.mp3", path: "/song.mp3", type: "file" as const, size: 9, date: "2026-01-02T00:00:00Z" },
  ],
}

function mediaBackend() {
  const backend = memoryBackend(seed, { transfers: { picks: { dir: "/home/me/Downloads" } } })
  backend.putPreviewForTest("/clip.mp4", { mime: "video/mp4", data: "not-a-mp4" })
  backend.putPreviewForTest("/song.mp3", { mime: "audio/mpeg", data: "not-a-mp3" })
  return backend
}

async function openPreview(name: string) {
  render(<App backend={mediaBackend()} languages={["en"]} />)
  fireEvent.click(await screen.findByRole("button", { name }))
  return screen.findByRole("dialog", { name })
}

/** Fires error on media the way a webview does when it cannot decode it. */
function failDecode(media: HTMLMediaElement) {
  Object.defineProperty(media, "error", { configurable: true, value: { code: 4, message: "unsupported" } })
  fireEvent.error(media)
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

const pick = (name: string, mime: string) => choosePreview(descriptor(name, mime), previewProviders).id

test("the registry picks the video player for common video MIME types and extensions", () => {
  expect(pick("clip.mp4", "video/mp4")).toBe("video")
  expect(pick("clip.webm", "video/webm")).toBe("video")
  expect(pick("clip.mov", "video/quicktime")).toBe("video")
  // Any video/* type is offered to the player; the webview decides.
  expect(pick("clip.mkv", "video/x-matroska")).toBe("video")
  expect(pick("clip.bin", "video/mp4; codecs=avc1")).toBe("video")
  // No useful MIME: the extension decides.
  expect(pick("CLIP.MP4", "application/octet-stream")).toBe("video")
  expect(pick("clip.m4v", "")).toBe("video")
  expect(pick("clip.webm", "")).toBe("video")
  expect(pick("clip.mov", "")).toBe("video")
})

test("the registry picks the audio player for common audio MIME types and extensions", () => {
  expect(pick("song.mp3", "audio/mpeg")).toBe("audio")
  expect(pick("song.flac", "audio/flac")).toBe("audio")
  expect(pick("song.ogg", "audio/ogg")).toBe("audio")
  expect(pick("song.wav", "audio/wav")).toBe("audio")
  expect(pick("song.m4a", "audio/mp4")).toBe("audio")
  expect(pick("song.dat", "audio/x-anything")).toBe("audio")
  // No useful MIME: the extension decides.
  for (const ext of ["mp3", "m4a", "aac", "flac", "ogg", "oga", "opus", "wav"]) {
    expect(pick("song." + ext, "")).toBe("audio")
  }
  // The indexed MIME outranks a misleading extension in both directions.
  expect(pick("song.mp4", "audio/mp4")).toBe("audio")
  expect(pick("clip.ogg", "video/ogg")).toBe("video")
})

test("a video opens in the player over the media URL; a decode error falls back with Download", async () => {
  const dialog = await openPreview("clip.mp4")
  const video = await waitForMedia(dialog, "video")
  expect(video.getAttribute("src")).toStartWith("data:video/mp4;base64,")

  failDecode(video)
  await within(dialog).findByText("This file could not be previewed.")
  expect(within(dialog).getByRole("alert").textContent).toBe("This system cannot play this file's format.")
  expect(dialog.querySelector("video")).toBeNull()
  expect(within(dialog).getAllByRole("button", { name: "Download" }).length).toBe(2)
})

test("an audio file opens in the audio player; a decode error falls back with Download", async () => {
  const dialog = await openPreview("song.mp3")
  const audio = await waitForMedia(dialog, "audio")
  expect(audio.getAttribute("src")).toStartWith("data:audio/mpeg;base64,")
  expect(within(dialog).getByRole("button", { name: "Play" })).toBeTruthy()
  expect(within(dialog).getByRole("slider", { name: "Seek" })).toBeTruthy()
  expect(within(dialog).getByRole("slider", { name: "Volume" })).toBeTruthy()

  failDecode(audio)
  await within(dialog).findByText("This file could not be previewed.")
  expect(dialog.querySelector("audio")).toBeNull()
  expect(within(dialog).getAllByRole("button", { name: "Download" }).length).toBe(2)
})

test("Escape while the video player is fullscreen leaves the preview open", async () => {
  const dialog = await openPreview("clip.mp4")
  const video = await waitForMedia(dialog, "video")
  // The player wires up fullscreen once it knows the video's metadata.
  fireEvent(video, new Event("loadedmetadata"))
  const button = dialog.querySelector<HTMLElement>(".art-control-fullscreen")
  expect(button).toBeTruthy()
  await act(async () => {
    fireEvent.click(button!)
  })
  expect(document.fullscreenElement).toBeTruthy()
  expect(dialog.contains(document.fullscreenElement)).toBe(true)

  fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape", code: "Escape" })
  expect(screen.getByRole("dialog", { name: "clip.mp4" })).toBe(dialog)

  await act(() => document.exitFullscreen())
  fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape", code: "Escape" })
  expect(screen.queryByRole("dialog")).toBeNull()
})

async function waitForMedia<K extends "video" | "audio">(dialog: HTMLElement, tag: K) {
  return waitFor(
    () => {
      const media = dialog.querySelector(tag)
      if (!media) throw new Error(`no <${tag}> in the preview yet`)
      return media
    },
    { timeout: 3000 },
  )
}

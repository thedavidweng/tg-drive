import { afterEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react"

import { App } from "@/App"
import { memoryBackend, type MemoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

const seed = {
  "/": [{ name: "notes.txt", path: "/notes.txt", type: "file", size: 2048, date: "2026-01-02T10:30:00Z" }],
}

async function openDrive(backend: MemoryBackend, languages = ["en"]) {
  render(<App backend={backend} languages={languages} />)
  await screen.findByRole("list")
}

test("the upload sheet previews the plan and starts the upload with the chosen options", async () => {
  const backend = memoryBackend(seed, {
    transfers: { picks: { files: ["/home/me/kyoto.jpg"] }, fileSizes: { "/home/me/kyoto.jpg": 1536 } },
  })
  await openDrive(backend)

  fireEvent.click(screen.getByRole("button", { name: "Upload files" }))
  const dialog = await screen.findByRole("dialog", { name: "Upload to /" })

  // The dry-run plan: the file with its size, and the account's limit.
  await within(dialog).findByRole("list", { name: "Files to upload" })
  expect(within(dialog).getByText("kyoto.jpg")).toBeTruthy()
  expect(within(dialog).getByText("1.5 KB")).toBeTruthy()
  expect(within(dialog).getByText("Upload limit: 2 GB per file")).toBeTruthy()

  // The options: photo presentation, a caption, skip on conflict, no hash.
  fireEvent.click(within(dialog).getByRole("radio", { name: "Photo" }))
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Caption" }), { target: { value: "spring trip" } })
  fireEvent.click(within(dialog).getByRole("radio", { name: "Skip it" }))
  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Skip content hashing" }))

  fireEvent.click(within(dialog).getByRole("button", { name: "Start upload" }))
  await screen.findByText("Upload started — watch it in Transfers.")
  expect(backend.uploads).toHaveLength(1)
  expect(backend.uploads[0].paths).toEqual(["/home/me/kyoto.jpg"])
  expect(backend.uploads[0].dest).toBe("/")
  expect(backend.uploads[0].opts).toMatchObject({
    policy: "skip",
    kind: "photo",
    caption: "spring trip",
    no_hash: true,
  })
})

test("the replace policy is blocked until its confirmation is ticked", async () => {
  const backend = memoryBackend(seed, { transfers: { picks: { files: ["/tmp/a.txt"] } } })
  await openDrive(backend)

  fireEvent.click(screen.getByRole("button", { name: "Upload files" }))
  const dialog = await screen.findByRole("dialog", { name: "Upload to /" })
  await within(dialog).findByRole("list", { name: "Files to upload" })

  fireEvent.click(within(dialog).getByRole("radio", { name: "Replace it" }))
  // The dry-run plan names what a replace would overwrite.
  await within(dialog).findByText("Overwrites /")
  const start = within(dialog).getByRole("button", { name: "Start upload" })
  expect(start.hasAttribute("disabled")).toBe(true)

  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Overwrite existing files" }))
  expect(start.hasAttribute("disabled")).toBe(false)

  fireEvent.click(start)
  await screen.findByText("Upload started — watch it in Transfers.")
  expect(backend.uploads[0].opts).toMatchObject({ policy: "replace", confirm_replace: true })
})

test("an oversized file is flagged before the upload starts", async () => {
  const backend = memoryBackend(seed, {
    transfers: {
      picks: { files: ["/home/me/huge.bin", "/home/me/small.txt"] },
      fileSizes: { "/home/me/huge.bin": 3 * 1024 * 1024 * 1024, "/home/me/small.txt": 42 },
    },
  })
  await openDrive(backend)

  fireEvent.click(screen.getByRole("button", { name: "Upload files" }))
  const dialog = await screen.findByRole("dialog", { name: "Upload to /" })
  await within(dialog).findByText("huge.bin exceeds the 2 GB upload limit")
  // The file under the limit is not flagged.
  expect(within(dialog).queryByText(/small\.txt exceeds/)).toBeNull()
})

test("an album upload cannot choose replace", async () => {
  const backend = memoryBackend(seed, { transfers: { picks: { files: ["/tmp/a.jpg", "/tmp/b.jpg"] } } })
  await openDrive(backend)

  fireEvent.click(screen.getByRole("button", { name: "Upload files" }))
  const dialog = await screen.findByRole("dialog", { name: "Upload to /" })
  await within(dialog).findByRole("list", { name: "Files to upload" })

  const replace = within(dialog).getByRole("radio", { name: "Replace it" })
  expect(replace.hasAttribute("disabled")).toBe(true)
  expect(within(dialog).getByText("Replace is not available for album uploads.")).toBeTruthy()
})

test("a folder upload offers the recursive options instead of presentation", async () => {
  const backend = memoryBackend(seed, {
    transfers: { picks: { dir: "/home/me/Pictures" }, dirs: ["/home/me/Pictures"] },
  })
  await openDrive(backend)

  fireEvent.click(screen.getByRole("button", { name: "Upload folder" }))
  const dialog = await screen.findByRole("dialog", { name: "Upload to /" })
  await within(dialog).findByRole("list", { name: "Files to upload" })

  expect(within(dialog).queryByRole("radiogroup", { name: "Show as" })).toBeNull()
  expect(within(dialog).queryByRole("textbox", { name: "Caption" })).toBeNull()
  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Continue past failed files" }))
  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Create empty folders" }))

  fireEvent.click(within(dialog).getByRole("button", { name: "Start upload" }))
  await screen.findByText("Upload started — watch it in Transfers.")
  expect(backend.uploads[0].opts).toMatchObject({ continue_on_error: true, include_empty_dirs: true })
})

test("the download sheet applies the chosen local conflict policy", async () => {
  const backend = memoryBackend(seed, { transfers: { picks: { dir: "/home/me/Downloads" } } })
  await openDrive(backend)

  fireEvent.click(within(screen.getByRole("listitem")).getByRole("button", { name: "Download" }))
  const dialog = await screen.findByRole("dialog", { name: "Download" })
  expect(within(dialog).getByText("/home/me/Downloads")).toBeTruthy()

  fireEvent.click(within(dialog).getByRole("radio", { name: "Replace it" }))
  fireEvent.click(within(dialog).getByRole("button", { name: "Start download" }))
  await screen.findByText("Download started — watch it in Transfers.")
  expect(backend.downloads).toHaveLength(1)
  expect(backend.downloads[0]).toMatchObject({ remotePath: "/notes.txt", destDir: "/home/me/Downloads" })
  expect(backend.downloads[0].opts).toMatchObject({ policy: "replace" })
})

test("a video upload carries the attributes, thumbnail, and upload tuning td cp takes", async () => {
  const backend = memoryBackend(seed, { transfers: { picks: { files: ["/home/me/clip.mp4"] } } })
  await openDrive(backend)

  fireEvent.click(screen.getByRole("button", { name: "Upload files" }))
  const dialog = await screen.findByRole("dialog", { name: "Upload to /" })
  await within(dialog).findByRole("list", { name: "Files to upload" })

  // The video attributes only show for the video kind.
  expect(within(dialog).queryByRole("spinbutton", { name: "Duration (seconds)" })).toBeNull()
  fireEvent.click(within(dialog).getByRole("radio", { name: "Video" }))
  fireEvent.change(within(dialog).getByRole("spinbutton", { name: "Duration (seconds)" }), { target: { value: "12.5" } })
  fireEvent.change(within(dialog).getByRole("spinbutton", { name: "Width (px)" }), { target: { value: "1920" } })
  fireEvent.change(within(dialog).getByRole("spinbutton", { name: "Height (px)" }), { target: { value: "1080" } })
  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Streamable" }))
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Thumbnail (JPEG)" }), {
    target: { value: "/home/me/thumb.jpg" },
  })
  fireEvent.change(within(dialog).getByRole("spinbutton", { name: "Upload threads" }), { target: { value: "3" } })
  fireEvent.change(within(dialog).getByRole("spinbutton", { name: "Part size (KB)" }), { target: { value: "256" } })
  fireEvent.click(within(dialog).getByRole("radio", { name: "Rename the new file" }))

  fireEvent.click(within(dialog).getByRole("button", { name: "Start upload" }))
  await screen.findByText("Upload started — watch it in Transfers.")
  expect(backend.uploads[0].opts).toMatchObject({
    policy: "rename",
    kind: "video",
    duration_seconds: 12.5,
    width: 1920,
    height: 1080,
    supports_streaming: true,
    thumb_path: "/home/me/thumb.jpg",
    upload_threads: 3,
    upload_part_size_kb: 256,
  })
})

test("a photo upload offers no thumbnail", async () => {
  const backend = memoryBackend(seed, { transfers: { picks: { files: ["/home/me/kyoto.jpg"] } } })
  await openDrive(backend)

  fireEvent.click(screen.getByRole("button", { name: "Upload files" }))
  const dialog = await screen.findByRole("dialog", { name: "Upload to /" })
  await within(dialog).findByRole("list", { name: "Files to upload" })
  expect(within(dialog).getByRole("textbox", { name: "Thumbnail (JPEG)" })).toBeTruthy()
  fireEvent.click(within(dialog).getByRole("radio", { name: "Photo" }))
  expect(within(dialog).queryByRole("textbox", { name: "Thumbnail (JPEG)" })).toBeNull()
})

test("a folder download offers auto-rename and continuing past failures", async () => {
  const backend = memoryBackend(
    {
      "/": [{ name: "photos", path: "/photos", type: "dir", size: 0, date: "2026-01-02T10:30:00Z" }],
      "/photos": [{ name: "a.jpg", path: "/photos/a.jpg", type: "file", size: 3, date: "2026-01-02T10:30:00Z" }],
    },
    { transfers: { picks: { dir: "/home/me/Downloads" } } },
  )
  await openDrive(backend)

  fireEvent.click(within(screen.getByRole("listitem")).getByRole("button", { name: "Download" }))
  const dialog = await screen.findByRole("dialog", { name: "Download" })
  fireEvent.click(within(dialog).getByRole("radio", { name: "Rename the new file" }))
  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Continue past failed files" }))
  fireEvent.click(within(dialog).getByRole("button", { name: "Start download" }))
  await screen.findByText("Download started — watch it in Transfers.")
  expect(backend.downloads[0].opts).toMatchObject({ policy: "rename", continue_on_error: true })
})

test("a file download does not offer continuing past failures", async () => {
  const backend = memoryBackend(seed, { transfers: { picks: { dir: "/home/me/Downloads" } } })
  await openDrive(backend)

  fireEvent.click(within(screen.getByRole("listitem")).getByRole("button", { name: "Download" }))
  const dialog = await screen.findByRole("dialog", { name: "Download" })
  expect(within(dialog).queryByRole("checkbox", { name: "Continue past failed files" })).toBeNull()
})

test("a submission failure shows in the sheet and keeps it open", async () => {
  const backend = memoryBackend(seed, {
    transfers: {
      picks: { files: ["/tmp/a.jpg"] },
      submitError: { code: "ERR_USAGE", category: "validation", message: "nothing to upload" },
    },
  })
  await openDrive(backend)

  fireEvent.click(screen.getByRole("button", { name: "Upload files" }))
  const dialog = await screen.findByRole("dialog", { name: "Upload to /" })
  await within(dialog).findByRole("list", { name: "Files to upload" })
  fireEvent.click(within(dialog).getByRole("button", { name: "Start upload" }))

  await within(dialog).findByRole("alert")
  expect(within(dialog).getByRole("alert").textContent).toContain("nothing to upload")
  expect(screen.getByRole("dialog", { name: "Upload to /" })).toBeTruthy()
})

test("the sheets are translated", async () => {
  const backend = memoryBackend(seed, { transfers: { picks: { files: ["/tmp/a.jpg"] } } })
  await openDrive(backend, ["zh-CN"])

  fireEvent.click(screen.getByRole("button", { name: "上传文件" }))
  const dialog = await screen.findByRole("dialog", { name: "上传到 /" })
  await within(dialog).findByRole("radiogroup", { name: "显示为" })
  expect(within(dialog).getByRole("button", { name: "开始上传" })).toBeTruthy()
  expect(within(dialog).getByText(/上传限制/)).toBeTruthy()
})

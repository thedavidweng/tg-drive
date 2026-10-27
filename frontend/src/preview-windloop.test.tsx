// TEMPORARY diagnostic: repeats the Windows-flaky preview scenario in-process
// many times so an intermittent ordering/commit race shows up in one CI run.
// Each iteration reports the Escape outcome; any iteration that loses the
// preview is logged with the listener/commit state seen at dispatch time.
import { afterEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"

import { App } from "@/App"
import { memoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

const seed = {
  "/": [
    { name: "photos", path: "/photos", type: "dir" as const, size: 0, date: "2026-01-01T00:00:00Z" },
    { name: "archive.zip", path: "/archive.zip", type: "file" as const, size: 4096, date: "2026-01-02T00:00:00Z" },
  ],
  "/photos": [{ name: "cat.png", path: "/photos/cat.png", type: "file" as const, size: 68, date: "2026-01-03T00:00:00Z" }],
}

const png = Uint8Array.from(
  atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="),
  (c) => c.charCodeAt(0),
)

const ITERATIONS = 200

test(
  "windiag: repeat failed-preview -> download sheet -> Escape",
  async () => {
    const dialogsAtDispatch: string[] = []
    const observe = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return
      const open = Array.from(document.querySelectorAll('[role="dialog"][aria-modal="true"]'))
      dialogsAtDispatch.push(
        open.map((d) => d.getAttribute("aria-label") ?? "?").join(">") + ` dp=${e.defaultPrevented}`,
      )
    }

    const lost: number[] = []
    for (let i = 0; i < ITERATIONS; i++) {
      dialogsAtDispatch.length = 0
      document.addEventListener("keydown", observe)
      try {
        const backend = memoryBackend(seed, { transfers: { picks: { dir: "/home/me/Downloads" } } })
        backend.putPreviewForTest("/photos/cat.png", { mime: "image/png", data: png })
        backend.failPreviewForTest("/photos/cat.png", {
          code: "ERR_TELEGRAM_RATE_LIMITED",
          category: "telegram",
          message: "telegram rate limited this account",
        })
        render(<App backend={backend} languages={["en"]} />)
        fireEvent.click(await screen.findByRole("button", { name: "photos" }))
        const list = await screen.findByRole("list", { name: "Files in /photos" })
        fireEvent.click(within(list).getByRole("button", { name: "cat.png" }))
        const dialog = await screen.findByRole("dialog", { name: "cat.png" })
        await within(dialog).findByRole("alert")
        fireEvent.click(within(dialog).getAllByRole("button", { name: "Download" })[0])
        const sheet = await screen.findByRole("dialog", { name: "Download" })

        fireEvent.keyDown(document.activeElement!, { key: "Escape" })
        await waitFor(() => {
          if (sheet.isConnected) throw new Error("sheet still connected")
        })
        if (!document.querySelector('[role="dialog"][aria-label="cat.png"]')) {
          lost.push(i)
          console.log(`[windiag] iteration ${i} LOST THE PREVIEW; dispatch saw ${dialogsAtDispatch.join(" | ")}`)
        }
      } finally {
        document.removeEventListener("keydown", observe)
        cleanup()
      }
    }
    console.log(`[windiag] ${ITERATIONS} iterations, lost preview in: [${lost.join(",")}]`)
    expect(lost).toEqual([])
  },
  240_000,
)

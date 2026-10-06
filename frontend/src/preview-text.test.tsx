import { afterEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react"

import { App } from "@/App"
import type { PreviewDescriptor } from "@/backend"
import { previewProviders } from "@/preview/providers"
import { choosePreview } from "@/preview/registry"
import { sanitizedFragment } from "@/preview/safe-html"
import { memoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

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

test("the registry routes text, code, Markdown, and HTML to the text previews", () => {
  const pick = (name: string, mime: string) => choosePreview(descriptor(name, mime), previewProviders).id
  expect(pick("notes.txt", "text/plain; charset=utf-8")).toBe("text")
  expect(pick("server.log", "application/octet-stream")).toBe("text")
  expect(pick("main.go", "application/octet-stream")).toBe("text")
  expect(pick("config.json", "application/json")).toBe("text")
  expect(pick("deploy.yaml", "application/yaml")).toBe("text")
  expect(pick("Cargo.toml", "")).toBe("text")
  expect(pick("app.tsx", "")).toBe("text")
  expect(pick("README.md", "text/markdown; charset=utf-8")).toBe("markdown")
  expect(pick("notes.markdown", "application/octet-stream")).toBe("markdown")
  // HTML is source text, never a rendered page.
  expect(pick("index.html", "text/html; charset=utf-8")).toBe("text")
  expect(pick("page.xhtml", "application/xhtml+xml")).toBe("text")
  expect(pick("feed.xml", "text/xml; charset=utf-8")).toBe("text")
  // The system MIME table calls .ts and .mts MPEG transport streams, which
  // no supported webview plays; the extension decides.
  expect(pick("app.ts", "video/mp2t")).toBe("text")
  expect(pick("index.mts", "video/mp2t")).toBe("text")
})

test("a binary file under a text name falls back to file details and Download", async () => {
  const backend = memoryBackend({ "/": [] })
  // An MPEG transport stream packet: sync byte, then header and NUL padding.
  backend.putPreviewForTest("/capture.ts", { mime: "video/mp2t", data: new Uint8Array([0x47, 0x40, 0x00, 0x10, 0x00, 0x00, 0xb0, 0x0d]) })
  const dialog = await openPreview(backend, "capture.ts")
  await within(dialog).findByText("This file could not be previewed.")
  expect(within(dialog).queryByLabelText("Contents of capture.ts")).toBeNull()
  expect(within(dialog).getAllByRole("button", { name: "Download" }).length).toBeGreaterThan(0)
})

test("source code is monospaced, read-only, and syntax highlighted; plain text stays plain", async () => {
  const config = '{\n  "name": "td",\n  "retries": 3,\n  "debug": false\n}\n'
  const backend = memoryBackend({ "/": [] })
  backend.putPreviewForTest("/config.json", { data: config })
  backend.putPreviewForTest("/notes.txt", { data: "just <b>notes</b>\n" })
  const dialog = await openPreview(backend, "config.json")

  const source = await within(dialog).findByLabelText("Contents of config.json")
  expect(source.tagName).toBe("PRE")
  expect(source.textContent).toBe(config)
  expect(source.querySelector("textarea, input, [contenteditable]")).toBeNull()
  expect(Array.from(source.querySelectorAll(".hljs-attr")).map((e) => e.textContent)).toEqual([
    '"name"',
    '"retries"',
    '"debug"',
  ])
  expect(source.querySelector(".hljs-number")?.textContent).toBe("3")
  fireEvent.click(within(dialog).getByRole("button", { name: "Close preview" }))

  fireEvent.click(await screen.findByRole("button", { name: "notes.txt" }))
  const notes = await within(await screen.findByRole("dialog", { name: "notes.txt" })).findByLabelText("Contents of notes.txt")
  expect(notes.textContent).toBe("just <b>notes</b>\n")
  expect(notes.querySelector("b, span")).toBeNull()
})

test("a failed read falls back to file details and Download", async () => {
  const realFetch = globalThis.fetch
  globalThis.fetch = (async () => new Response("Bad Gateway", { status: 502 })) as unknown as typeof fetch
  try {
    const backend = memoryBackend({ "/": [] })
    backend.putPreviewForTest("/notes.txt", { data: "hello" })
    const dialog = await openPreview(backend, "notes.txt")
    await within(dialog).findByText("This file could not be previewed.")
    expect(within(dialog).queryByLabelText("Contents of notes.txt")).toBeNull()
    expect(within(dialog).getAllByRole("button", { name: "Download" }).length).toBeGreaterThan(0)
  } finally {
    globalThis.fetch = realFetch
  }
})

/** Opens path's preview in a fresh App over backend and returns the dialog. */
async function openPreview(backend: ReturnType<typeof memoryBackend>, name: string) {
  render(<App backend={backend} languages={["en"]} />)
  fireEvent.click(await screen.findByRole("button", { name }))
  return screen.findByRole("dialog", { name })
}

test("an HTML file shows its source as text, with no frame and no script run", async () => {
  const page = `<!doctype html><h1>Hi</h1><script>window.__ran = true</script><img src=x onerror="window.__ran = true">`
  const backend = memoryBackend({ "/": [] })
  backend.putPreviewForTest("/index.html", { mime: "text/html; charset=utf-8", data: page })
  const dialog = await openPreview(backend, "index.html")

  const source = await within(dialog).findByLabelText("Contents of index.html")
  expect(source.textContent).toBe(page)
  expect(dialog.querySelector("iframe, frame, object, embed, script, img, h1")).toBeNull()
  expect((window as { __ran?: boolean }).__ran).toBeUndefined()
  // Syntax-aware: the markup is tokenised, not dumped as one string.
  expect(source.querySelector(".hljs-tag")).not.toBeNull()
})

test("Markdown renders sanitised: raw HTML stays text, so script and handler payloads are inert", async () => {
  const note = [
    "# Trip notes",
    "",
    "Some **bold** text and a [link](https://example.com/x).",
    "",
    "- one",
    "- two",
    "",
    "<script>window.__mdRan = true</script>",
    '<img src="x" onerror="window.__mdRan = true" alt="broken">',
    "",
    "![pixel](https://tracker.example/pixel.png) and [js link](javascript:window.__mdRan=true)",
    "",
  ].join("\n")
  const backend = memoryBackend({ "/": [] })
  backend.putPreviewForTest("/notes.md", { data: note })
  const dialog = await openPreview(backend, "notes.md")

  const doc = await within(dialog).findByLabelText("Contents of notes.md")
  expect(within(doc).getByRole("heading", { level: 1, name: "Trip notes" })).toBeTruthy()
  expect(doc.querySelector("strong")?.textContent).toBe("bold")
  expect(within(doc).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["one", "two"])

  // Raw HTML stays source text; images never load.
  expect(doc.querySelector("script, iframe, img, div, [onerror], [onclick], [style]")).toBeNull()
  expect(doc.textContent).toContain("<script>window.__mdRan = true</script>")
  expect(doc.textContent).toContain('<img src="x" onerror="window.__mdRan = true" alt="broken">')
  expect(doc.textContent).toContain("pixel and js link")
  expect(within(doc).getAllByRole("link").map((a) => a.getAttribute("href"))).toEqual(["https://example.com/x"])

  // A link never navigates the app away.
  const link = within(doc).getByRole("link", { name: "link" })
  expect(link.getAttribute("title")).toBe("https://example.com/x")
  const click = new MouseEvent("click", { bubbles: true, cancelable: true })
  link.dispatchEvent(click)
  expect(click.defaultPrevented).toBe(true)
  expect((window as { __mdRan?: boolean }).__mdRan).toBeUndefined()
})

/**
 * Serves body at url the way the media route does, 206 for each Range (or,
 * with whole, a 200 stream of the whole body as a non-seekable file gets),
 * and records every Range asked for and how many body bytes were pulled.
 * Other URLs go to the real fetch.
 */
function serveRanges(url: string, body: Uint8Array, { whole = false } = {}) {
  const ranges: string[] = []
  const pulled = { bytes: 0 }
  const realFetch = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input) !== url) return realFetch(input, init)
    const range = new Headers(init?.headers).get("Range") ?? ""
    ranges.push(range)
    if (whole) {
      let at = 0
      const stream = new ReadableStream<Uint8Array>({
        pull(ctl) {
          if (at >= body.length) return ctl.close()
          const piece = body.slice(at, at + 64 * 1024)
          at += piece.length
          pulled.bytes += piece.length
          ctl.enqueue(piece)
        },
      })
      return new Response(stream, { status: 200, headers: { "Accept-Ranges": "none" } })
    }
    const [, from, to] = /^bytes=(\d+)-(\d*)$/.exec(range) ?? []
    const start = Number(from)
    const end = Math.min(to ? Number(to) : body.length - 1, body.length - 1)
    if (start >= body.length) return new Response(null, { status: 416, headers: { "Content-Range": `bytes */${body.length}` } })
    return new Response(body.slice(start, end + 1), {
      status: 206,
      headers: { "Content-Range": `bytes ${start}-${end}/${body.length}`, "Content-Type": "text/plain; charset=utf-8" },
    })
  }) as typeof fetch
  return { ranges, pulled, restore: () => (globalThis.fetch = realFetch) }
}

test("a large text file fetches only its first 2 MiB, says so, and Load more fetches the next range", async () => {
  const MiB = 1 << 20
  // 5 MiB of lines; a two-byte "é" straddles the 2 MiB boundary.
  const head = "x".repeat(2 * MiB - 1) + "é"
  const text = head + "\nsecond chunk starts here\n" + "y".repeat(5 * MiB - head.length - 30) + "\nthe end\n"
  const body = new TextEncoder().encode(text)
  const server = serveRanges("td-test://big.log", body)
  try {
    const backend = memoryBackend({ "/": [] })
    backend.putFileForTest("/big.log", body.length, "2026-01-01T00:00:00Z")
    backend.putPreviewForTest("/big.log", { mime: "text/plain; charset=utf-8", url: "td-test://big.log" })
    const dialog = await openPreview(backend, "big.log")

    const source = await within(dialog).findByLabelText("Contents of big.log")
    expect(server.ranges).toEqual(["bytes=0-2097151"])
    expect(source.textContent!.length).toBe(2 * MiB - 1)
    const status = within(dialog).getByRole("status")
    expect(status.textContent).toContain("Partial file: showing the first 2 MB of 5 MB.")

    fireEvent.click(within(status).getByRole("button", { name: "Load more" }))
    await within(dialog).findByText(/second chunk starts here/)
    expect(server.ranges).toEqual(["bytes=0-2097151", "bytes=2097152-4194303"])
    // The split character is joined, not garbled.
    expect(source.textContent!.slice(2 * MiB - 2, 2 * MiB + 2)).toBe("xé\ns")
    expect(within(dialog).getByRole("status").textContent).toContain("showing the first 4 MB of 5 MB")

    fireEvent.click(within(dialog).getByRole("button", { name: "Load more" }))
    await within(dialog).findByText(/the end/)
    expect(server.ranges).toEqual(["bytes=0-2097151", "bytes=2097152-4194303", "bytes=4194304-6291455"])
    expect(source.textContent).toBe(text)
    // The whole file is shown: no partial notice, nothing more to load.
    expect(within(dialog).queryByRole("status")).toBeNull()
    expect(within(dialog).queryByRole("button", { name: "Load more" })).toBeNull()
  } finally {
    server.restore()
  }
})

test("a file served whole without ranges is still read only as far as the chunk shown", async () => {
  const MiB = 1 << 20
  const text = "a".repeat(2 * MiB) + "b".repeat(2 * MiB) + "c".repeat(MiB)
  const body = new TextEncoder().encode(text)
  const server = serveRanges("td-test://whole.txt", body, { whole: true })
  try {
    const backend = memoryBackend({ "/": [] })
    backend.putFileForTest("/whole.txt", body.length, "2026-01-01T00:00:00Z")
    backend.putPreviewForTest("/whole.txt", { url: "td-test://whole.txt", ranges: false })
    const dialog = await openPreview(backend, "whole.txt")

    const source = await within(dialog).findByLabelText("Contents of whole.txt")
    expect(source.textContent).toBe("a".repeat(2 * MiB))
    expect(server.pulled.bytes).toBeLessThan(3 * MiB)
    expect(within(dialog).getByRole("status").textContent).toContain("showing the first 2 MB of 5 MB")

    fireEvent.click(within(dialog).getByRole("button", { name: "Load more" }))
    await within(dialog).findByText(/^a+b+$/)
    expect(source.textContent).toBe(text.slice(0, 4 * MiB))
    expect(server.ranges).toEqual(["bytes=0-2097151", "bytes=2097152-4194303"])
  } finally {
    server.restore()
  }
})

// A text message's served body is not the indexed file, so when the route
// cannot say how long it is, only the response itself marks the end.
test("a file of unknown served length is complete only when the response ends", async () => {
  const MiB = 1 << 20
  const text = "a".repeat(2 * MiB) + "b".repeat(MiB)
  const body = new TextEncoder().encode(text)
  const server = serveRanges("td-test://note.txt", body, { whole: true })
  try {
    const backend = memoryBackend({ "/": [] })
    backend.putFileForTest("/note.txt", MiB, "2026-01-01T00:00:00Z")
    backend.putPreviewForTest("/note.txt", { url: "td-test://note.txt", ranges: false })
    const dialog = await openPreview(backend, "note.txt")

    const source = await within(dialog).findByLabelText("Contents of note.txt")
    expect(source.textContent).toBe("a".repeat(2 * MiB))
    fireEvent.click(within(dialog).getByRole("button", { name: "Load more" }))
    await within(dialog).findByText(/^a+b+$/)
    expect(source.textContent).toBe(text)
    expect(within(dialog).queryByRole("status")).toBeNull()
  } finally {
    server.restore()
  }
})

// The Markdown renderer escapes raw HTML before it reaches the sanitiser,
// so no preview can drive these payloads through it end to end.
test("the sanitiser keeps only allowlisted elements and attributes", () => {
  const allow = { p: [], a: ["href", "title"], span: ["class"] }
  const html = (input: string) => {
    const host = document.createElement("div")
    host.append(sanitizedFragment(input, allow))
    return host.innerHTML
  }
  expect(html('<p onclick="x()" style="position:fixed">hi <span class="k" onmouseover="x()">there</span></p>')).toBe(
    '<p>hi <span class="k">there</span></p>',
  )
  expect(html("<script>x()</script><style>p{}</style><template><p>t</p></template>ok")).toBe("ok")
  expect(html('<img src=x onerror="x()"><iframe src="about:blank"></iframe><div><b>kept</b></div>')).toBe("kept")
  expect(html('<svg><a href="https://e.x">s</a></svg><math><mi>m</mi></math>')).toBe("")
  for (const href of ["javascript:x()", " JaVaScRiPt:x()", "\u0001javascript:x()", "data:text/html,<p>", "vbscript:x", "java\tscript:x()", "java&#x0A;script:x()", "javascript&#58;x()"]) {
    expect(html(`<a href="${href}">l</a>`)).toBe("<a>l</a>")
  }
  for (const href of ["https://e.x/", "mailto:a@e.x", "#top", "docs/a.md"]) {
    expect(html(`<a href="${href}">l</a>`)).toBe(`<a href="${href}">l</a>`)
  }
  expect(html('<constructor>c</constructor><toString>t</toString>')).toBe("ct")
})

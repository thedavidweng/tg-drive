import assert from "node:assert/strict"
import { createRequire } from "node:module"
import { fileURLToPath } from "node:url"
import { test } from "node:test"
import { chromium, webkit } from "playwright"

const frontend = fileURLToPath(new URL("../frontend/", import.meta.url))
const requireFrontend = createRequire(new URL("../frontend/package.json", import.meta.url))
const { createServer } = await import(requireFrontend.resolve("vite"))

test("sanitizing HTML does not load discarded resources", { timeout: 30_000 }, async () => {
  const server = await createServer({
    root: frontend,
    configFile: `${frontend}vite.config.ts`,
    logLevel: "silent",
    server: { host: "127.0.0.1", port: 0 },
  })
  let browser
  try {
    await server.listen()
    const port = server.httpServer.address().port
    const browserType = { chromium, webkit }[process.env.PLAYWRIGHT_BROWSER ?? "chromium"]
    assert.ok(browserType, "unsupported browser")
    browser = await browserType.launch({ headless: true })
    const page = await browser.newPage()
    const requests = []
    const origin = `http://127.0.0.1:${port}`
    const testPage = await server.transformIndexHtml("/", "<!doctype html><html><head></head><body></body></html>")
    await page.route("**/*", (route) => {
      const url = route.request().url()
      if (url === `${origin}/`) return route.fulfill({ status: 200, contentType: "text/html", body: testPage })
      if (!url.startsWith(`${origin}/`)) return route.abort()
      return route.continue()
    })
    page.on("request", (request) => {
      if (request.url().startsWith("https://resources.invalid/")) requests.push(request.url())
    })
    await page.goto(`${origin}/`)

    const image = page.waitForRequest("https://resources.invalid/control-image")
    const frame = page.waitForRequest("https://resources.invalid/control-frame")
    await page.evaluate(() => {
      const img = document.createElement("img")
      img.src = "https://resources.invalid/control-image"
      document.body.append(img)
      const iframe = document.createElement("iframe")
      iframe.src = "https://resources.invalid/control-frame"
      document.body.append(iframe)
    })
    await Promise.all([image, frame])
    assert.deepEqual(new Set(requests), new Set([
      "https://resources.invalid/control-image", "https://resources.invalid/control-frame",
    ]))
    requests.length = 0

    const result = await page.evaluate(async () => {
      const { sanitizedFragment } = await import("/src/preview/safe-html.tsx")
      const html = '<img src="https://resources.invalid/image"><iframe src="https://resources.invalid/frame"></iframe>' +
        '<link rel="stylesheet" href="https://resources.invalid/style"><script>window.injected = true</script>' +
        '<p onclick="window.injected = true">safe <strong>text</strong></p>'
      const host = document.createElement("div")
      host.append(sanitizedFragment(html, { p: [], strong: [] }))
      document.body.append(host)
      return { html: host.innerHTML, injected: window.injected === true }
    })
    await page.waitForTimeout(500)
    assert.deepEqual(result, { html: "<p>safe <strong>text</strong></p>", injected: false })
    assert.deepEqual(requests, [])
  } finally {
    await browser?.close()
    await server.close()
  }
})

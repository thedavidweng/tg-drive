// Records the td-gui preview: a screenshot per scene plus one walkthrough
// video, driven by Playwright against a td-gui server-mode build
// (go build -tags gui,server) backed by the fake Telegram.
//
//   node record.mjs --url http://127.0.0.1:3209 --out out
//
// A scene is one entry in the list below; adding one is a new entry (plus
// whatever state run.sh seeds for it). Each scene renders in its own
// browser context at 2x device scale, the window size the desktop app opens
// with, and produces one PNG in the output directory next to manifest.json
// and preview.mp4.
import fs from "node:fs/promises"
import path from "node:path"
import { chromium } from "playwright"

// The window size cmd/td-gui opens with; screenshots are taken at 2x.
const VIEWPORT = { width: 960, height: 640 }

// The scenes of the current app. Fields: colorScheme is the emulated
// prefers-color-scheme, theme the localStorage override (theme.ts), locale
// the browser locale the i18n catalogues resolve from. settle is optional
// extra interaction before the shot.
const scenes = [
  { name: "drive-light", title: "Drive — light", colorScheme: "light" },
  { name: "drive-dark", title: "Drive — dark", colorScheme: "dark" },
  {
    name: "drive-theme-override",
    title: "Drive — dark override on a light system",
    colorScheme: "light",
    theme: "dark",
  },
  { name: "drive-zh-CN", title: "Drive — 简体中文", colorScheme: "light", locale: "zh-CN" },
]

function parseArgs(argv) {
  const args = { url: "", out: "" }
  for (let i = 2; i < argv.length; i += 2) {
    args[argv[i].replace(/^--/, "")] = argv[i + 1]
  }
  if (!args.url || !args.out) throw new Error("usage: record.mjs --url <base> --out <dir>")
  return args
}

// The Drive list is ready when its <ul> renders (the loading and error
// states render no list).
async function openDrive(page, base) {
  await page.goto(base + "/", { waitUntil: "networkidle" })
  await page.waitForSelector("ul", { timeout: 30_000 })
}

async function shootScene(browser, base, out, scene) {
  const context = await browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: 2,
    colorScheme: scene.colorScheme,
    locale: scene.locale,
  })
  if (scene.theme) {
    await context.addInitScript((theme) => localStorage.setItem("td-theme", theme), scene.theme)
  }
  const page = await context.newPage()
  await openDrive(page, base)
  if (scene.settle) await scene.settle(page)
  const file = `${scene.name}.png`
  await page.screenshot({ path: path.join(out, file) })
  await context.close()
  return { name: scene.name, title: scene.title, file, width: VIEWPORT.width * 2, height: VIEWPORT.height * 2 }
}

// The walkthrough video: open the Drive and cycle the theme override, so
// the recording shows the real app responding, not a static page.
async function shootVideo(browser, base, out) {
  const context = await browser.newContext({
    viewport: VIEWPORT,
    recordVideo: { dir: out, size: VIEWPORT },
  })
  const page = await context.newPage()
  // The recording only materialises when the context closes; on a failed
  // walkthrough the caller removes the partial webm so it never ships.
  let webm = ""
  try {
    await openDrive(page, base)
    const toggle = page.getByRole("button", { name: /^Theme:/ })
    await page.waitForTimeout(600)
    await toggle.click() // system → light
    await page.waitForTimeout(900)
    await toggle.click() // light → dark
    await page.waitForTimeout(1200)
    await toggle.click() // dark → system
    await page.waitForTimeout(600)
    webm = await page.video().path()
  } finally {
    await context.close()
  }
  return webm
}

const { url, out } = parseArgs(process.argv)
await fs.mkdir(out, { recursive: true })
const browser = await chromium.launch()
const errors = []
const shots = []
for (const scene of scenes) {
  try {
    shots.push(await shootScene(browser, url, out, scene))
  } catch (err) {
    errors.push(`${scene.name}: ${err.message}`)
  }
}
let video = ""
try {
  const webm = await shootVideo(browser, url, out)
  video = path.basename(webm)
} catch (err) {
  errors.push(`video: ${err.message}`)
  for (const f of await fs.readdir(out)) {
    if (f.endsWith(".webm")) await fs.unlink(path.join(out, f)).catch(() => {})
  }
}
await browser.close()

await fs.writeFile(
  path.join(out, "manifest.json"),
  JSON.stringify(
    {
      sha: process.env.PREVIEW_SHA || "",
      scenes: shots,
      video,
      poster: shots[0]?.file || "",
      errors,
    },
    null,
    2,
  ) + "\n",
)
console.log(`recorded ${shots.length}/${scenes.length} scenes${video ? " + video" : ""}`)
for (const e of errors) console.error("scene failed:", e)
process.exit(errors.length ? 1 : 0)

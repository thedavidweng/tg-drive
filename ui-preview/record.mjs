// Records the td-gui preview: a screenshot per scene plus one walkthrough
// video, driven by Playwright against a td-gui server-mode build
// (go build -tags gui,server) backed by the fake Telegram.
//
//   node record.mjs --url http://127.0.0.1:3209 --out out [--setup-url http://127.0.0.1:3210]
//
// A scene is one entry in the list below; adding one is a new entry (plus
// whatever state run.sh seeds for it). Each scene renders in its own
// browser context at 2x device scale, the window size the desktop app opens
// with, and produces one PNG in the output directory next to manifest.json
// and preview.mp4. Scenes marked setup record against --setup-url, the
// second, credential-free server run.sh launches.
import fs from "node:fs/promises"
import path from "node:path"
import { spawn } from "node:child_process"
import { chromium } from "playwright"

// The window size cmd/td-gui opens with; screenshots are taken at 2x.
const VIEWPORT = { width: 960, height: 640 }

// The scenes of the current app. Fields: colorScheme is the emulated
// prefers-color-scheme, theme the localStorage override (theme.ts), locale
// the browser locale the i18n catalogues resolve from. open replaces the
// default navigation (openDrive); settle is optional extra interaction
// before the shot; leave runs after it, to hand the next scene a clean
// state. setup selects the credential-free server. spawn names an
// environment variable holding a shell command (run.sh exports them) to
// start once the scene's page has loaded — how a scene runs a CLI upload
// against the same fake Telegram.
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
  // The Drive tab's file-manager surfaces: breadcrumb navigation into a
  // folder, a destructive row action blocked by its confirmation sheet,
  // and the tree view. All three only navigate or open a sheet — no
  // server state changes, so later scenes see the seeded drive intact.
  {
    name: "drive-breadcrumbs",
    title: "Drive — breadcrumbs in a folder",
    colorScheme: "light",
    settle: async (page) => {
      await page.getByRole("listitem").filter({ hasText: "Photos" }).getByRole("button", { name: "Photos" }).click()
      await page.getByRole("list", { name: "Files in /Photos" }).waitFor()
    },
  },
  {
    name: "drive-delete-sheet",
    title: "Drive — delete confirmation sheet",
    colorScheme: "light",
    settle: async (page) => {
      await page
        .getByRole("listitem")
        .filter({ hasText: "notes.txt" })
        .getByRole("button", { name: "Delete" })
        .click()
      await page.getByRole("dialog", { name: "Delete file" }).waitFor()
    },
  },
  {
    name: "drive-tree",
    title: "Drive — tree view",
    colorScheme: "light",
    settle: async (page) => {
      await page.getByRole("button", { name: "Tree" }).click()
      // The tree view is a static outline: a named nested list, not the
      // interactive tree role.
      await page.getByRole("list", { name: "Tree of /" }).waitFor()
    },
  },
  // The channel switcher: the sheet lists the bound drives and the active
  // channel's status, and the bind sheet offers the account's unbound
  // channel next to the create form. The third scene completes a switch
  // and hands the Drive channel back afterwards, because the facade keeps
  // the selected channel server-side across the per-scene contexts.
  {
    name: "drive-channels-sheet",
    title: "Drive — channel switcher and status",
    colorScheme: "light",
    settle: async (page) => {
      await page.getByRole("button", { name: "Switch drive" }).click()
      await page.getByRole("dialog", { name: "Drives" }).waitFor()
      // The seeded CLI drive auto-links its discussion group; waiting for
      // it proves the status block loaded.
      await page.getByText("Drive Discussion").waitFor()
    },
  },
  {
    name: "drive-bind-sheet",
    title: "Drive — bind or create a drive",
    colorScheme: "light",
    settle: async (page) => {
      await page.getByRole("button", { name: "Switch drive" }).click()
      await page.getByRole("button", { name: "Bind or create a drive" }).click()
      await page.getByRole("dialog", { name: "Bind or create a drive" }).waitFor()
      await page.getByRole("button", { name: "Bind Photos Archive" }).waitFor()
    },
  },
  {
    name: "drive-switched",
    title: "Drive — switched to Backups",
    colorScheme: "light",
    settle: async (page) => {
      await page.getByRole("button", { name: "Switch drive" }).click()
      await page.getByRole("button", { name: "Switch to Backups" }).click()
      await page.getByText("backup.txt").waitFor()
    },
    leave: async (page) => {
      await page.getByRole("button", { name: "Switch drive" }).click()
      await page.getByRole("button", { name: "Switch to Drive" }).click()
      await page.getByText("notes.txt").waitFor()
    },
  },
  // Import and Maintenance, against the seeded drive. All dry runs or
  // prompts that get cancelled in leave(), so the drive stays untouched.
  {
    name: "import-preview",
    title: "Import — dry-run plan",
    colorScheme: "light",
    settle: async (page) => {
      await page.getByRole("tab", { name: "Import" }).click()
      await page.getByText("Import from Saved Messages").waitFor()
      // Choose a photo presentation so the preview does not pause on the
      // photo prompt (that prompt gets its own scene below).
      await page.getByRole("group", { name: "Photos" }).getByRole("button", { name: "Document" }).click()
      await page.getByRole("button", { name: "Preview" }).click()
      await page.getByText(/^Plan: /).waitFor()
    },
  },
  {
    name: "import-photo-prompt",
    title: "Import — photo presentation prompt",
    colorScheme: "light",
    settle: async (page) => {
      await page.getByRole("tab", { name: "Import" }).click()
      await page.getByText("Import from Saved Messages").waitFor()
      await page.getByRole("button", { name: "Import…" }).click()
      await page
        .getByRole("dialog", { name: "Import Saved Messages" })
        .getByRole("button", { name: "Import" })
        .click()
      // The run blocks on the photo prompt: the shot shows that sheet.
      await page.getByRole("dialog", { name: "Republish photos as" }).waitFor()
    },
    leave: async (page) => {
      // Abort the pending run so later scenes see an untouched drive.
      await page
        .getByRole("dialog", { name: "Republish photos as" })
        .getByRole("button", { name: "Cancel" })
        .click()
      await page.getByRole("alert").waitFor()
    },
  },
  {
    name: "maintenance-adopt",
    title: "Maintenance — adopt preview",
    colorScheme: "light",
    settle: async (page) => {
      await page.getByRole("tab", { name: "Maintenance" }).click()
      await page.getByText("Adopt existing messages").waitFor()
      await page.getByRole("button", { name: "Preview" }).click()
      await page.getByText(/^Plan: /).waitFor()
    },
  },
  {
    name: "maintenance-doctor",
    title: "Maintenance — diagnostics",
    colorScheme: "dark",
    settle: async (page) => {
      await page.getByRole("tab", { name: "Maintenance" }).click()
      await page.getByText("Adopt existing messages").waitFor()
      await page.getByRole("button", { name: "Run checks" }).click()
      await page.getByRole("list", { name: "Capability checks" }).waitFor()
    },
  },
  // First-run setup and login, against the credential-free server. They
  // chain through the facade's real state: auth-login saves credentials,
  // auth-code starts a login (and cancels it after the shot), so each
  // open below starts from what the previous scene left.
  {
    name: "auth-setup-zh-CN",
    title: "Setup — 简体中文",
    colorScheme: "light",
    locale: "zh-CN",
    setup: true,
    open: (page, base) => openAuth(page, base, "form", "连接 Telegram"),
  },
  {
    name: "auth-setup",
    title: "First-run setup",
    colorScheme: "light",
    setup: true,
    open: (page, base) => openAuth(page, base, "form", "Connect to Telegram"),
  },
  {
    name: "auth-login",
    title: "Login — start",
    colorScheme: "light",
    setup: true,
    open: async (page, base) => {
      await openAuth(page, base, "form", "Connect to Telegram")
      await page.getByLabel("api_id").fill("12345")
      await page.getByLabel("api_hash").fill("deadbeef")
      await page.getByLabel("Phone").fill("+15551234567")
      await page.getByRole("button", { name: "Save and continue" }).click()
      await page.getByRole("heading", { name: "Log in to Telegram" }).waitFor()
    },
  },
  {
    name: "auth-code",
    title: "Login — code prompt",
    colorScheme: "light",
    setup: true,
    open: async (page, base) => {
      await openAuth(page, base, "heading", "Log in to Telegram")
      await page.getByRole("button", { name: "Send login code" }).click()
      await page.getByLabel("Login code").waitFor()
    },
    // Cancelling aborts the login the prompt belongs to; waiting for the
    // start button proves the facade released it before the next scene.
    leave: async (page) => {
      await page.getByRole("button", { name: "Cancel" }).click()
      await page.getByRole("button", { name: "Send login code" }).waitFor()
    },
  },
  {
    name: "auth-logged-in",
    title: "Logged in",
    colorScheme: "light",
    setup: true,
    open: async (page, base) => {
      await openAuth(page, base, "heading", "Log in to Telegram")
      await page.getByRole("button", { name: "Send login code" }).click()
      await page.getByLabel("Login code").fill("12345")
      await page.getByRole("button", { name: "Verify code" }).click()
      // run.sh gives this server's fake account two-step verification.
      await page.getByLabel("Two-step verification password").fill("hunter2")
      await page.getByRole("button", { name: "Verify password" }).click()
      await page.getByLabel("Logged in as Test User").waitFor()
    },
  },
  // The Transfers scenes. Native pickers are no-ops in server mode, so the
  // binary answers them from TD_GUI_PICK_FILES (see picker.go). The "CLI"
  // scenes start a real td cp against the same fake Telegram through the
  // spawn hook, then watch it surface through the index poll.
  //
  // Order matters: the fake serializes Telegram calls, and a running
  // upload holds it for every part — so no page may load while an upload
  // runs. The CLI scenes open their page first, then spawn their upload;
  // the live-upload scene goes last, so its album overlaps only the video
  // walkthrough (which never calls Telegram).
  {
    name: "transfers-cli-upload",
    title: "Transfers — a CLI upload among finished ones",
    colorScheme: "light",
    open: openTransfers,
    spawn: "TD_PREVIEW_CLI_CP",
    settle: async (page) => {
      const active = page.getByRole("region", { name: "Active" })
      // exact: the upload's source path ends with the same file name.
      await active.getByText("/big.bin", { exact: true }).waitFor({ timeout: 15_000 })
      await active.getByText("Uploading").first().waitFor()
      // The history carries the failed seeding upload, its reason, and the
      // error code, with a retry button.
      const history = page.getByRole("region", { name: "Last 30 days" })
      await history.getByText("/broken.bin", { exact: true }).waitFor()
      await history.getByText(/ERR_TELEGRAM/).waitFor()
      await history.getByRole("button", { name: "Retry /broken.bin" }).waitFor()
    },
    // Hand the next scene a quiet server: big.bin has completed.
    leave: async (page) => {
      await page
        .getByRole("region", { name: "Last 30 days" })
        .getByText("/big.bin", { exact: true })
        .waitFor({ timeout: 30_000 })
    },
  },
  {
    name: "transfers-cli-cancel",
    title: "Transfers — cancelling a CLI upload",
    colorScheme: "light",
    open: openTransfers,
    spawn: "TD_PREVIEW_CLI_CP2",
    settle: async (page) => {
      const active = page.getByRole("region", { name: "Active" })
      await active.getByText("/cli-slow.bin", { exact: true }).waitFor({ timeout: 15_000 })
      await page.getByRole("button", { name: "Cancel /cli-slow.bin" }).click()
      await page.getByText("Cancelled").first().waitFor({ timeout: 15_000 })
    },
  },
  {
    name: "transfers-upload-live",
    title: "Transfers — live upload",
    colorScheme: "light",
    settle: async (page) => {
      await page.getByRole("button", { name: "Upload files" }).click()
      await page.getByRole("tab", { name: "Transfers" }).click()
      const active = page.getByRole("region", { name: "Active" })
      await active.getByText("Uploading").first().waitFor()
      await active.getByRole("progressbar").waitFor()
    },
    // The video walkthrough loads a page after this scene; wait the album
    // out so its fake-Telegram RPCs do not block the load.
    leave: async (page) => {
      await page
        .getByRole("region", { name: "Last 30 days" })
        .getByText("/", { exact: true })
        .first()
        .waitFor({ timeout: 60_000 })
    },
  },
]

function parseArgs(argv) {
  const args = { url: "", out: "", "setup-url": "" }
  for (let i = 2; i < argv.length; i += 2) {
    args[argv[i].replace(/^--/, "")] = argv[i + 1]
  }
  if (!args.url || !args.out) throw new Error("usage: record.mjs --url <base> --out <dir> [--setup-url <base>]")
  if (scenes.some((s) => s.setup) && !args["setup-url"]) {
    throw new Error("scenes marked setup need --setup-url (run.sh launches the second server)")
  }
  return args
}

// The Drive list is ready when its <ul> renders (the loading and error
// states render no list). goto waits for "load", not "networkidle":
// active Transfers stream progress events over the bindings websocket,
// and "networkidle" never arrives while one runs.
async function openDrive(page, base) {
  await page.goto(base + "/", { waitUntil: "load" })
  await page.waitForSelector("ul", { timeout: 30_000 })
}

// The auth screens render no <ul>; readiness is the role and name the
// scene passes (the setup form, the login heading).
async function openAuth(page, base, role, name) {
  await page.goto(base + "/", { waitUntil: "load" })
  await page.getByRole(role, { name }).waitFor({ timeout: 30_000 })
}

// openTransfers navigates straight to the Transfers tab: the CLI scenes
// have nothing to do on the Drive tab.
async function openTransfers(page, base) {
  await page.goto(base + "/", { waitUntil: "load" })
  await page.getByRole("tab", { name: "Transfers" }).click()
  await page.getByRole("region", { name: "Active" }).waitFor({ timeout: 30_000 })
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
  try {
    await (scene.open ?? openDrive)(page, base)
    // A scene's spawn hook starts an outside process — the CLI upload the
    // Transfers scenes watch. It keeps running into the following scenes;
    // the main loop SIGINTs every spawned child before exiting.
    if (scene.spawn) {
      const cmd = process.env[scene.spawn]
      if (!cmd) throw new Error(`scene ${scene.name} wants $${scene.spawn}; run.sh exports it`)
      const child = spawn("bash", ["-c", cmd], { stdio: "ignore", env: process.env })
      spawned.push(child)
    }
    if (scene.settle) await scene.settle(page)
  } catch (err) {
    // A failure screenshot shows the state the scene got stuck on.
    await page.screenshot({ path: path.join(out, `${scene.name}-failed.png`) }).catch(() => {})
    throw err
  }
  const file = `${scene.name}.png`
  await page.screenshot({ path: path.join(out, file) })
  if (scene.leave) await scene.leave(page)
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

const { url, out, "setup-url": setupUrl } = parseArgs(process.argv)
await fs.mkdir(out, { recursive: true })
const browser = await chromium.launch()
const errors = []
const shots = []
// CLI processes the scenes started; a SIGINT lets each end its transfer
// as cancelled instead of leaving a dead lease in the index.
const spawned = []
let video = ""
try {
  for (const scene of scenes) {
    try {
      shots.push(await shootScene(browser, scene.setup ? setupUrl : url, out, scene))
    } catch (err) {
      errors.push(`${scene.name}: ${err.message.split("\n")[0]}`)
    }
  }
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
} finally {
  for (const child of spawned) {
    child.kill("SIGINT")
    child.unref()
  }
}

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

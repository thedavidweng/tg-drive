// The project website, served by GitHub Pages from the root of the
// previews branch it shares with the UI preview (ui-preview/publish.mjs,
// which owns the pr-*/ directories there).
//
//   node site/publish.mjs build <out-dir>   assemble the site locally
//   node site/publish.mjs publish           push it to the previews branch
//
// env (publish): GITHUB_TOKEN, GITHUB_REPOSITORY, SHA
import { execFileSync } from "node:child_process"
import fs from "node:fs/promises"
import os from "node:os"
import path from "node:path"

const BRANCH = "previews"
const here = path.dirname(new URL(import.meta.url).pathname)
const root = path.dirname(here)
const files = ["index.html", "site.css", "site.js"]
const images = { "icon.png": "assets/icon.png", "screenshot.png": "assets/screenshot.png" }

async function build(out) {
  await fs.mkdir(path.join(out, "img"), { recursive: true })
  for (const f of files) await fs.copyFile(path.join(here, f), path.join(out, f))
  for (const [name, src] of Object.entries(images)) await fs.copyFile(path.join(root, src), path.join(out, "img", name))
}

function git(args, cwd) {
  return execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim()
}

// Replaces everything at the branch root except the previews' pr-*/
// directories, as one fresh orphan commit (the branch never accumulates
// history). A preview pushing in between makes this start over from theirs.
async function publish() {
  const repo = process.env.GITHUB_REPOSITORY
  const token = process.env.GITHUB_TOKEN
  const sha = (process.env.SHA || "").slice(0, 7)
  const remote = `https://x-access-token:${token}@github.com/${repo}.git`
  const work = await fs.mkdtemp(path.join(os.tmpdir(), "td-site-"))
  git(["init", "-q"], work)
  git(["config", "user.name", "github-actions[bot]"], work)
  git(["config", "user.email", "41898282+github-actions[bot]@users.noreply.github.com"], work)
  for (let attempt = 0; attempt < 6; attempt++) {
    let base = ""
    try {
      git(["fetch", "-q", "--depth=1", remote, BRANCH], work)
      base = git(["rev-parse", "FETCH_HEAD"], work)
      git(["checkout", "-q", "-f", "--detach", "FETCH_HEAD"], work)
    } catch {
      /* the first push */
    }
    for (const d of await fs.readdir(work)) {
      if (d === ".git" || /^pr-\d+$/.test(d)) continue
      await fs.rm(path.join(work, d), { recursive: true, force: true })
    }
    await build(work)
    await fs.writeFile(path.join(work, ".nojekyll"), "")
    git(["checkout", "-q", "--orphan", `tmp-${attempt}`], work)
    git(["add", "-A"], work)
    git(["commit", "-q", "-m", `site${sha ? ` at ${sha}` : ""}`], work)
    try {
      git(["push", "-q", `--force-with-lease=${BRANCH}:${base}`, remote, `HEAD:refs/heads/${BRANCH}`], work)
      await fs.rm(work, { recursive: true, force: true })
      return
    } catch (e) {
      console.log(`push lost a race (${e.message.split("\n")[0]}), again`)
      await new Promise((r) => setTimeout(r, 2000 + Math.random() * 4000))
    }
  }
  throw new Error("couldn't push the site")
}

const [mode, out] = process.argv.slice(2)
const run = mode === "build" && out ? build(path.resolve(out)) : mode === "publish" ? publish() : null
if (!run) throw new Error("usage: publish.mjs build <out-dir> | publish")
run.catch((e) => {
  console.error(e)
  process.exit(1)
})

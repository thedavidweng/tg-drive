// The trusted half of the UI preview: never checks out or runs the PR's
// code. It publishes the recorded artifacts (OUT_DIR, written by
// record.mjs) to the previews branch served by GitHub Pages and rewrites
// the preview block in the PR description.
//
//   node publish.mjs publish
//
// The preview lives in one fixed block between the markers below; each run
// replaces that block and leaves the rest of the description untouched.
//
// env: GITHUB_TOKEN, GITHUB_REPOSITORY, PR, SHA, OUT_DIR, RECORD_RESULT,
// RUN_URL
import { execFileSync } from "node:child_process"
import fs from "node:fs/promises"
import path from "node:path"
import os from "node:os"

const BEGIN = "<!-- td-gui-preview:start -->"
const END = "<!-- td-gui-preview:end -->"
const BRANCH = "previews"
const repo = process.env.GITHUB_REPOSITORY
const pr = Number(process.env.PR)
const token = process.env.GITHUB_TOKEN
const runURL = process.env.RUN_URL || ""

async function gh(p, init = {}) {
  const res = await fetch(`https://api.github.com${p}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      Accept: "application/vnd.github+json",
      "X-GitHub-Api-Version": "2022-11-28",
      ...init.headers,
    },
  })
  if (!res.ok) throw new Error(`${init.method || "GET"} ${p}: ${res.status} ${(await res.text()).slice(0, 300)}`)
  return res.status === 204 ? null : res.json()
}

// the description with the block in place of the old one, or at its foot
function withBlock(body, block) {
  body = body || ""
  const a = body.indexOf(BEGIN)
  const b = body.indexOf(END)
  if (a >= 0 && b > a) return body.slice(0, a) + block + body.slice(b + END.length)
  return body.replace(/\s*$/, "") + "\n\n" + block
}

async function setBlock(inner) {
  // read just before writing, so an edit the author just made is kept
  const cur = await gh(`/repos/${repo}/pulls/${pr}`)
  const next = withBlock(cur.body, `${BEGIN}\n${inner.trim()}\n${END}`)
  if (next !== cur.body) await gh(`/repos/${repo}/pulls/${pr}`, { method: "PATCH", body: JSON.stringify({ body: next }) })
}

const short = (sha) => sha.slice(0, 7)
const head = (sha) =>
  `---\n### td-gui preview\n<sub>CI built this PR's code, seeded a fake Telegram drive, and recorded the real app · <code>${short(sha)}</code>${runURL ? ` · [workflow run](${runURL})` : ""}</sub>\n`

function git(args, cwd) {
  return execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim()
}

// The artifacts onto the previews branch: one commit, replaced each run so
// the branch never piles up old media; directories of PRs that have since
// closed are dropped. Another PR's run pushing in between makes this one
// start over from theirs.
async function pushMedia(sha, dir) {
  const work = await fs.mkdtemp(path.join(os.tmpdir(), "td-gui-previews-"))
  const remote = `https://x-access-token:${token}@github.com/${repo}.git`
  git(["init", "-q"], work)
  git(["config", "user.name", "github-actions[bot]"], work)
  git(["config", "user.email", "41898282+github-actions[bot]@users.noreply.github.com"], work)
  const open = new Set((await gh(`/repos/${repo}/pulls?state=open&per_page=100`)).map((p) => p.number))
  for (let attempt = 0; attempt < 6; attempt++) {
    let base = ""
    try {
      git(["fetch", "-q", "--depth=1", remote, BRANCH], work)
      base = git(["rev-parse", "FETCH_HEAD"], work)
      git(["checkout", "-q", "-f", "--detach", "FETCH_HEAD"], work)
    } catch {
      /* the first push */
    }
    for (const d of await fs.readdir(work).catch(() => [])) {
      const m = /^pr-(\d+)$/.exec(d)
      if (m && (Number(m[1]) === pr || !open.has(Number(m[1])))) await fs.rm(path.join(work, d), { recursive: true, force: true })
    }
    const to = path.join(work, `pr-${pr}`, short(sha))
    await fs.mkdir(to, { recursive: true })
    for (const f of await fs.readdir(dir)) await fs.copyFile(path.join(dir, f), path.join(to, f))
    const manifest = JSON.parse(await fs.readFile(path.join(dir, "manifest.json"), "utf8"))
    await fs.writeFile(path.join(to, "index.html"), player(sha, manifest))
    await fs.writeFile(path.join(work, ".nojekyll"), "")
    await fs.writeFile(
      path.join(work, "index.html"),
      `<!doctype html><meta charset="utf-8"><title>td-gui previews</title><p>Screenshots and recordings of <a href="https://github.com/${repo}/pulls">${repo}</a> pull requests, made by .github/workflows/ui-preview.yml.</p>`,
    )
    git(["checkout", "-q", "--orphan", `tmp-${attempt}`], work)
    git(["add", "-A"], work)
    git(["commit", "-q", "-m", `ui preview for #${pr} at ${short(sha)}`], work)
    try {
      git(["push", "-q", `--force-with-lease=${BRANCH}:${base}`, remote, `HEAD:refs/heads/${BRANCH}`], work)
      await fs.rm(work, { recursive: true, force: true })
      return
    } catch (e) {
      console.log(`push lost a race (${e.message.split("\n")[0]}), again`)
      await new Promise((r) => setTimeout(r, 2000 + Math.random() * 4000))
    }
  }
  throw new Error("couldn't push the preview")
}

const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c])

// The page the video plays on: GitHub renders no video it does not host
// itself, so the recording lives on the Pages site next to the shots.
function player(sha, m) {
  const shots = m.scenes
  return `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>#${pr} td-gui preview · ${short(sha)}</title>
<style>:root{color-scheme:light dark;--bg:#f6f6f7;--fg:#1d1d1f;--muted:#6e6e73;--card:#fff;--line:#e5e5ea}
@media (prefers-color-scheme:dark){:root{--bg:#111113;--fg:#f2f2f4;--muted:#9a9aa0;--card:#1c1c1f;--line:#2c2c30}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.6 -apple-system,"Segoe UI","PingFang SC","Noto Sans CJK SC",sans-serif}
main{max-width:1100px;margin:0 auto;padding:28px 16px 60px}h1{font-size:20px;margin:0 0 4px}.m{color:var(--muted);font-size:13px;margin:0 0 20px}
video,img{display:block;width:100%;border-radius:10px;border:1px solid var(--line);background:var(--card)}figure{margin:0 0 28px}figcaption{color:var(--muted);font-size:13px;margin-top:8px}
a{color:inherit}</style>
<main><h1>#${pr} td-gui preview</h1><p class="m">commit ${short(sha)} · <a href="https://github.com/${repo}/pull/${pr}">back to the PR</a></p>
${m.video ? `<figure><video src="${esc(m.video)}" controls autoplay muted playsinline poster="${esc(m.poster || "")}"></video><figcaption>A walkthrough of the scenes below, recorded from the real app.</figcaption></figure>` : ""}
${shots.map((s) => `<figure><img src="${esc(s.file)}" alt="${esc(s.title)}"><figcaption>${esc(s.title)}</figcaption></figure>`).join("\n")}
</main></html>`
}

async function pagesURL() {
  try {
    return (await gh(`/repos/${repo}/pages`)).html_url.replace(/\/?$/, "/")
  } catch {
    const [o, n] = repo.split("/")
    return `https://${o.toLowerCase()}.github.io/${n}/`
  }
}

async function publish() {
  const p = await gh(`/repos/${repo}/pulls/${pr}`)
  const sha = process.env.SHA || p.head.sha
  const dir = process.env.OUT_DIR
  const result = process.env.RECORD_RESULT || "success"
  let m = null
  try {
    m = JSON.parse(await fs.readFile(path.join(dir, "manifest.json"), "utf8"))
  } catch {
    /* no manifest: the record job never produced one */
  }
  if (result === "cancelled" && !m) return // a newer commit took over
  if (!m || (!m.scenes.length && !m.video)) {
    await setBlock(`${head(sha)}\nThe preview could not be recorded (${result}).${runURL ? ` See the [workflow run](${runURL}).` : ""}`)
    return
  }
  await pushMedia(sha, dir)
  const raw = `https://raw.githubusercontent.com/${repo}/${BRANCH}/pr-${pr}/${short(sha)}/`
  const site = `${await pagesURL()}pr-${pr}/${short(sha)}/`
  let md = `${head(sha)}\n`
  if (m.video) {
    md += `[![Play the recording](${raw}${m.poster})](${site})\n\n`
    md += `<sub>▶ Open the recording (pausable, seekable) · [download the mp4](${raw}${m.video})</sub>\n\n`
  }
  for (const s of m.scenes) {
    md += `#### ${s.title}\n\n<img src="${raw}${s.file}" width="${Math.min(760, Math.round((s.width || 1520) / 2))}" alt="${esc(s.title)}">\n\n`
  }
  if (m.errors?.length) {
    md += `<details><summary>Some scenes failed to record</summary>\n\n${m.errors.map((e) => "- " + e).join("\n")}\n</details>\n`
  }
  await setBlock(md)
}

if (process.argv[2] !== "publish") throw new Error("usage: publish.mjs publish")
publish().catch((e) => {
  console.error(e)
  process.exit(1)
})

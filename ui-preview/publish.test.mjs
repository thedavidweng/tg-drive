// The record job runs the PR's code, so its artifact is untrusted. These
// tests pin the publish trust boundary: only allowlisted media reach the
// Pages origin, and no manifest value can inject markup or break the block.
import assert from "node:assert/strict"
import fs from "node:fs/promises"
import os from "node:os"
import path from "node:path"
import test from "node:test"
import { cleanManifest, mediaFiles, text, MEDIA, MAX_FILE_BYTES } from "./publish.mjs"

async function outDir() {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "ui-preview-test-"))
  return dir
}

async function write(dir, name, bytes = "x") {
  await fs.writeFile(path.join(dir, name), bytes)
}

test("mediaFiles keeps allowlisted media and drops everything else", async () => {
  const dir = await outDir()
  await write(dir, "drive-light.png")
  await write(dir, "preview.mp4")
  await write(dir, "notes.jpeg")
  await write(dir, "clip.webm")
  await write(dir, "still.jpg")
  // not media: a PR could put these in the Pages origin
  await write(dir, "evil.html")
  await write(dir, "evil.js")
  await write(dir, "evil.svg")
  await write(dir, "noext")
  await write(dir, ".png")
  await write(dir, "manifest.json", "{}")
  const media = await mediaFiles(dir)
  assert.deepEqual([...media].sort(), ["clip.webm", "drive-light.png", "notes.jpeg", "preview.mp4", "still.jpg"])
})

test("mediaFiles rejects unsafe file names", async () => {
  const dir = await outDir()
  for (const name of ["../escape.png", "sub/inside.png", "a b.png", "a<b>.png", "a|b.png", "a\"b.png", "-dash.png"]) {
    assert.equal(MEDIA.test(name), false, `${name} must not be allowlisted`)
  }
  await fs.mkdir(path.join(dir, "sub"))
  await write(dir, "sub/inside.png")
  const media = await mediaFiles(dir)
  assert.equal(media.size, 0, "nested entries are not copied as plain files")
})

test("mediaFiles skips symlinks, even to an allowlisted name", async () => {
  const dir = await outDir()
  const target = path.join(dir, "real.png")
  await write(dir, "real.png")
  await fs.symlink(target, path.join(dir, "linked.png"))
  await fs.symlink("/etc/passwd", path.join(dir, "passwd.png"))
  const media = await mediaFiles(dir)
  assert.deepEqual([...media], ["real.png"])
})

test("mediaFiles skips oversized files", async () => {
  const dir = await outDir()
  await write(dir, "small.png", "x")
  await write(dir, "big.png", Buffer.alloc(MAX_FILE_BYTES + 1))
  const media = await mediaFiles(dir)
  assert.deepEqual([...media], ["small.png"])
})

test("mediaFiles on a missing directory yields no media", async () => {
  assert.equal((await mediaFiles("/nonexistent/ui-preview-out")).size, 0)
})

test("text flattens markup, block markers, and newlines", () => {
  assert.equal(text("Drive — light"), "Drive — light")
  assert.equal(text("a\nb\tc"), "a b c")
  assert.equal(text("<!-- td-gui-preview:end -->"), "!-- td-gui-preview:end --")
  assert.equal(text("<img src=x onerror=alert(1)>"), "img src=x onerror=alert(1)")
  assert.equal(text("`code` [link](http://x) | _em_"), "code link(http://x) em")
  assert.equal(text(undefined), "")
  assert.equal(text("x".repeat(500)).length, 200)
  // the one property that matters: a title can never reopen or close the block
  for (const hostile of ["<!-- td-gui-preview:end -->", "-->", "<!--", "\n<!-- td-gui-preview:start -->"]) {
    assert.equal(text(hostile).includes("<!--"), false, hostile)
    assert.equal(text(hostile).includes("-->"), false, hostile)
  }
})

test("cleanManifest keeps known fields and drops unknown or foreign files", () => {
  const media = new Set(["drive-light.png", "preview.mp4"])
  const raw = {
    sha: "deadbeef",
    extra: "ignored",
    scenes: [
      { title: "Drive — light", file: "drive-light.png", width: 1520 },
      { title: "Foreign", file: "not-in-artifact.png" },
      { title: "No file" },
      { title: "<script>x</script>", file: "drive-light.png", width: 99999 },
      "not an object",
    ],
    errors: ["scene a failed", "scene b failed"],
    video: "preview.mp4",
    poster: "drive-light.png",
  }
  const m = cleanManifest(raw, media)
  assert.deepEqual(m.scenes, [
    { title: "Drive — light", file: "drive-light.png", width: 1520 },
    { title: "scriptx/script", file: "drive-light.png", width: 4000 },
  ])
  assert.deepEqual(m.errors, ["scene a failed", "scene b failed"])
  assert.equal(m.video, "preview.mp4")
  assert.equal(m.poster, "drive-light.png")
  assert.equal("sha" in m, false, "unknown fields never reach the player or the description")
})

test("cleanManifest falls back to a default width and refuses foreign video", () => {
  const m = cleanManifest(
    { scenes: [{ title: "t", file: "a.png", width: "wide" }], video: "../evil.mp4", poster: 7 },
    new Set(["a.png"]),
  )
  assert.equal(m.scenes[0].width, 1520)
  assert.equal(m.video, "")
  assert.equal(m.poster, "")
})

test("cleanManifest survives a hostile manifest shape", () => {
  assert.deepEqual(cleanManifest(null, new Set()), { scenes: [], errors: [], video: "", poster: "" })
  assert.deepEqual(cleanManifest({ scenes: "nope", errors: {} }, new Set()), {
    scenes: [],
    errors: [],
    video: "",
    poster: "",
  })
})

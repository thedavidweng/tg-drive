import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import "@/index.css"
import { App } from "@/App"
import { samples } from "@/demo/samples"
import { MemoryBackend } from "@/testing/memory-backend"

// The website's live demo (ADR 0041): the real App on the in-memory
// backend the screen tests use, so it changes whenever the GUI does.
const backend = new MemoryBackend({
  channels: [{ id: "1001", title: "Open Media", discussion: "Open Media Discussion" }],
  transfers: { picks: { dir: "~/Downloads" } },
})
// Opening a sample previews it straight from its public host.
for (const s of samples) {
  backend.putFileForTest(s.path, s.size, s.date)
  backend.putPreviewForTest(s.path, { url: s.source })
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App backend={backend} languages={navigator.languages} demo />
  </StrictMode>,
)

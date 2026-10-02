import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { System } from "@wailsio/runtime"

import "./index.css"
import { App } from "@/App"
import { wailsBackend } from "@/wails-backend"

// The platform drives OS-specific chrome CSS (the macOS inset title bar
// floats its traffic lights over the header, which makes room via
// html[data-platform]). The sync flags read the environment the injected
// runtime already published; the async Environment() covers a slower
// injection. Without a Wails runtime (a plain browser) nothing is set and
// the default chrome CSS applies.
try {
  const os = System.IsMac() ? "darwin" : System.IsWindows() ? "windows" : System.IsLinux() ? "linux" : ""
  if (os) {
    document.documentElement.dataset.platform = os
  } else {
    void System.Environment().then(
      (env) => {
        document.documentElement.dataset.platform = env.OS
      },
      () => {},
    )
  }
} catch {
  // No runtime: the page chrome is platform-neutral.
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App backend={wailsBackend} languages={navigator.languages} />
  </StrictMode>,
)

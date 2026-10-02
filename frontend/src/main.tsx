import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import "./index.css"
import { App } from "@/App"
import { wailsBackend } from "@/wails-backend"

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App backend={wailsBackend} languages={navigator.languages} />
  </StrictMode>,
)

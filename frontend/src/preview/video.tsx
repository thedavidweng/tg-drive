import { lazy, Suspense } from "react"

import { useI18n } from "@/i18n"
import { baseMIME } from "@/preview/registry"
import type { PreviewProvider, PreviewViewProps } from "@/preview/types"

// The player is loaded on first use, keeping it out of the main bundle.
const VideoPlayer = lazy(() => import("@/preview/video-player"))

function VideoPreview(props: PreviewViewProps) {
  const { t } = useI18n()
  return (
    <Suspense fallback={<p className="py-16 text-center text-muted-foreground">{t("preview.loading")}</p>}>
      <VideoPlayer {...props} />
    </Suspense>
  )
}

export const videoPreview: PreviewProvider = {
  id: "video",
  mimeTypes: ["video/*"],
  extensions: ["mp4", "m4v", "webm", "mov", "mkv", "ogv"],
  // The index takes MIME types from the extension, and the system table
  // maps .ts and .mts (TypeScript) to MPEG transport streams, which no
  // supported webview plays; the extension then decides.
  canPreview: (d) => baseMIME(d.mime) !== "video/mp2t",
  View: VideoPreview,
}

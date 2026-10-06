import { useEffect, useRef } from "react"
import Artplayer from "artplayer"

import { useI18n } from "@/i18n"
import type { PreviewViewProps } from "@/preview/types"
import { useLatest } from "@/preview/use-latest"

// The player's own "version" context-menu entry links out to artplayer.org.
Artplayer.CONTEXTMENU = false
Artplayer.LOG_VERSION = false

const MEDIA_ERR_ABORTED = 1
const MEDIA_ERR_NETWORK = 2

export default function VideoPreview({ descriptor, onError }: PreviewViewProps) {
  const { locale, t } = useI18n()
  const hostRef = useRef<HTMLDivElement>(null)
  const onErrorRef = useLatest(onError)

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const art = new Artplayer({
      container: host,
      url: descriptor.url,
      lang: locale === "zh-CN" ? "zh-cn" : "en",
      autoSize: false,
      playbackRate: true,
      setting: true,
      pip: true,
      fullscreen: true,
      // The preview surface already fills the window; a second "web
      // fullscreen" would only fight it for Escape.
      fullscreenWeb: false,
      hotkey: true,
      mutex: true,
      miniProgressBar: true,
      theme: "var(--accent)",
    })

    // ArtPlayer retries every error up to RECONNECT_TIME_MAX times and then
    // only shows a toast. A codec or container the webview cannot decode
    // never succeeds on retry, so it fails over to the fallback at once;
    // a network error gets the player's retries first.
    let networkErrors = 0
    const video = art.video
    const onVideoError = () => {
      const code = video.error?.code
      if (code === MEDIA_ERR_ABORTED) return
      if (code === MEDIA_ERR_NETWORK && ++networkErrors <= Artplayer.RECONNECT_TIME_MAX) return
      onErrorRef.current(t("preview.media.unsupported"))
    }
    video.addEventListener("error", onVideoError)

    return () => {
      video.removeEventListener("error", onVideoError)
      art.destroy(true)
    }
  }, [descriptor.url, locale, t, onErrorRef])

  return (
    <div className="flex h-full min-h-0 items-center justify-center bg-black">
      <div ref={hostRef} aria-label={descriptor.name} role="region" className="h-full w-full" />
    </div>
  )
}

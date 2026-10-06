import { useEffect, useRef, useState } from "react"
import { Music, Pause, Play, Volume2, VolumeX } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n"
import type { PreviewProvider, PreviewViewProps } from "@/preview/types"

const MEDIA_ERR_ABORTED = 1

/** m:ss, or h:mm:ss past an hour; "--:--" while the length is unknown. */
function clock(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "--:--"
  const s = Math.floor(seconds)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const ss = String(s % 60).padStart(2, "0")
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`
}

/**
 * A compact in-app audio player over the media URL: play/pause, seek,
 * volume, and the platform media session (hardware keys, Now Playing).
 * Seeking works through the media route's byte ranges.
 */
function AudioPreview({ descriptor, onError }: PreviewViewProps) {
  const { t } = useI18n()
  const audioRef = useRef<HTMLAudioElement>(null)
  const [playing, setPlaying] = useState(false)
  const [time, setTime] = useState(0)
  const [duration, setDuration] = useState(Number.NaN)
  const [volume, setVolume] = useState(1)
  const [muted, setMuted] = useState(false)

  useEffect(() => {
    const session = typeof navigator !== "undefined" ? navigator.mediaSession : undefined
    const audio = audioRef.current
    if (!session || !audio || typeof MediaMetadata === "undefined") return
    session.metadata = new MediaMetadata({ title: descriptor.name })
    const handlers: [MediaSessionAction, MediaSessionActionHandler][] = [
      ["play", () => void audio.play()],
      ["pause", () => audio.pause()],
      ["seekbackward", (d) => (audio.currentTime = Math.max(0, audio.currentTime - (d.seekOffset ?? 10)))],
      ["seekforward", (d) => (audio.currentTime = audio.currentTime + (d.seekOffset ?? 10))],
      ["seekto", (d) => d.seekTime !== undefined && (audio.currentTime = d.seekTime)],
    ]
    for (const [action, handler] of handlers) {
      try {
        session.setActionHandler(action, handler)
      } catch {
        // An action this platform does not support.
      }
    }
    return () => {
      for (const [action] of handlers) {
        try {
          session.setActionHandler(action, null)
        } catch {
          // Unsupported action, as above.
        }
      }
      session.metadata = null
    }
  }, [descriptor.name])

  const toggle = () => {
    const audio = audioRef.current
    if (!audio) return
    if (audio.paused) void audio.play().catch(() => {})
    else audio.pause()
  }

  const seekable = Number.isFinite(duration) && duration > 0
  const shownVolume = muted ? 0 : volume

  return (
    <div className="flex h-full min-h-0 items-center justify-center p-6">
      <div className="w-full max-w-[460px] rounded-card border border-line bg-card p-5">
        <audio
          ref={audioRef}
          src={descriptor.url}
          preload="metadata"
          aria-label={descriptor.name}
          onPlay={() => setPlaying(true)}
          onPause={() => setPlaying(false)}
          onEnded={() => setPlaying(false)}
          onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
          onLoadedMetadata={(e) => setDuration(e.currentTarget.duration)}
          onDurationChange={(e) => setDuration(e.currentTarget.duration)}
          onVolumeChange={(e) => {
            setVolume(e.currentTarget.volume)
            setMuted(e.currentTarget.muted)
          }}
          onError={(e) => {
            if (e.currentTarget.error?.code === MEDIA_ERR_ABORTED) return
            onError(t("preview.media.unsupported"))
          }}
        />
        <div className="mb-4 flex items-center gap-3">
          <span aria-hidden className="grid size-10 shrink-0 place-items-center rounded-full bg-accent-soft text-primary">
            <Music className="size-5" />
          </span>
          <p className="min-w-0 truncate text-[13px] font-medium text-fg">{descriptor.name}</p>
        </div>
        <div className="flex items-center gap-3">
          <Button
            size="icon-sm"
            aria-label={playing ? t("preview.media.pause") : t("preview.media.play")}
            title={playing ? t("preview.media.pause") : t("preview.media.play")}
            onClick={toggle}
          >
            {playing ? <Pause aria-hidden className="size-4" /> : <Play aria-hidden className="size-4" />}
          </Button>
          <span className="w-12 shrink-0 text-right text-[11.5px] text-muted-foreground tabular-nums">{clock(time)}</span>
          <input
            type="range"
            aria-label={t("preview.media.seek")}
            min={0}
            max={seekable ? duration : 0}
            step="any"
            value={seekable ? Math.min(time, duration) : 0}
            disabled={!seekable}
            onChange={(e) => {
              const audio = audioRef.current
              if (audio) audio.currentTime = Number(e.currentTarget.value)
            }}
            className="min-w-0 flex-1 accent-[var(--accent)]"
          />
          <span className="w-12 shrink-0 text-[11.5px] text-muted-foreground tabular-nums">{clock(duration)}</span>
        </div>
        <div className="mt-3 flex items-center justify-end gap-2">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={muted ? t("preview.media.unmute") : t("preview.media.mute")}
            title={muted ? t("preview.media.unmute") : t("preview.media.mute")}
            onClick={() => {
              const audio = audioRef.current
              if (audio) audio.muted = !audio.muted
            }}
            className="text-ctl-fg"
          >
            {shownVolume === 0 ? <VolumeX aria-hidden className="size-4" /> : <Volume2 aria-hidden className="size-4" />}
          </Button>
          <input
            type="range"
            aria-label={t("preview.media.volume")}
            min={0}
            max={1}
            step={0.05}
            value={shownVolume}
            onChange={(e) => {
              const audio = audioRef.current
              if (!audio) return
              audio.volume = Number(e.currentTarget.value)
              audio.muted = audio.volume === 0
            }}
            className="w-28 accent-[var(--accent)]"
          />
        </div>
      </div>
    </div>
  )
}

export const audioPreview: PreviewProvider = {
  id: "audio",
  mimeTypes: ["audio/*"],
  extensions: ["mp3", "m4a", "aac", "flac", "ogg", "oga", "opus", "wav"],
  View: AudioPreview,
}

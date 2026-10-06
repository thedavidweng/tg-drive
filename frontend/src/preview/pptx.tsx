import { useEffect, useRef, useState } from "react"
import { ChevronLeft, ChevronRight } from "lucide-react"
import type { PPTXViewer } from "pptxviewjs"

import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n"
import { OfficeView, type OfficeRendererProps } from "@/preview/office"
import type { PreviewProvider, PreviewViewProps } from "@/preview/types"

// The renderer letterboxes each slide into the canvas, so one 16:9 frame
// fits both widescreen and 4:3 decks.
const FRAME_RATIO = 9 / 16
const MAX_FRAME_WIDTH = 1280
// The slide controls and the view's padding below and around the frame.
const CHROME_HEIGHT = 84

// Slides are painted onto a canvas, so slide text never becomes markup.
function PptxRenderer({ data, onError }: OfficeRendererProps) {
  const { t } = useI18n()
  const rootRef = useRef<HTMLDivElement>(null)
  const frameRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const viewerRef = useRef<PPTXViewer | null>(null)
  const [slide, setSlide] = useState(0)
  const [count, setCount] = useState(0)

  useEffect(() => {
    const canvas = canvasRef.current
    const frame = frameRef.current
    const root = rootRef.current
    if (!canvas || !frame || !root) return
    let live = true
    let viewer: PPTXViewer | null = null
    ;(async () => {
      const { PPTXViewer } = await import("pptxviewjs")
      const fitHeight = root.clientHeight > CHROME_HEIGHT ? (root.clientHeight - CHROME_HEIGHT) / FRAME_RATIO : Infinity
      const width = Math.round(Math.min(frame.clientWidth || MAX_FRAME_WIDTH, MAX_FRAME_WIDTH, fitHeight))
      canvas.style.width = `${width}px`
      canvas.style.height = `${Math.round(width * FRAME_RATIO)}px`
      viewer = new PPTXViewer({ canvas, enableThumbnails: false })
      await viewer.loadFile(data)
      if (!live) return
      if (viewer.getSlideCount() === 0) throw new Error("no slides")
      await viewer.render()
      if (!live) return
      viewerRef.current = viewer
      setCount(viewer.getSlideCount())
      setSlide(0)
    })().catch(() => {
      if (live) onError()
    })
    return () => {
      live = false
      viewerRef.current = null
      viewer?.destroy()
    }
  }, [data, onError])

  const go = (index: number) => {
    const viewer = viewerRef.current
    if (!viewer || index < 0 || index >= count) return
    viewer.goToSlide(index).then(
      () => setSlide(index),
      () => onError(),
    )
  }

  return (
    <div ref={rootRef} className="flex h-full min-h-0 flex-col items-center gap-3 p-4">
      <div ref={frameRef} className="flex w-full min-w-0 justify-center">
        <canvas ref={canvasRef} aria-label={count > 0 ? t("preview.pptx.slide", { n: slide + 1, total: count }) : undefined} role="img" className="rounded-control shadow-pop" />
      </div>
      {count > 0 && (
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="icon-sm"
            aria-label={t("preview.pptx.previous")}
            title={t("preview.pptx.previous")}
            disabled={slide === 0}
            onClick={() => go(slide - 1)}
          >
            <ChevronLeft aria-hidden />
          </Button>
          <span className="min-w-16 text-center text-[12px] text-muted-foreground tabular-nums">
            {slide + 1} / {count}
          </span>
          <Button
            variant="outline"
            size="icon-sm"
            aria-label={t("preview.pptx.next")}
            title={t("preview.pptx.next")}
            disabled={slide >= count - 1}
            onClick={() => go(slide + 1)}
          >
            <ChevronRight aria-hidden />
          </Button>
        </div>
      )}
    </div>
  )
}

function PptxPreview(props: PreviewViewProps) {
  return <OfficeView {...props} Renderer={PptxRenderer} />
}

export const pptxPreview: PreviewProvider = {
  id: "pptx",
  mimeTypes: ["application/vnd.openxmlformats-officedocument.presentationml.presentation"],
  extensions: ["pptx"],
  View: PptxPreview,
}

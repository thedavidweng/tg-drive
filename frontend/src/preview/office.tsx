import { useCallback, useEffect, useRef, useState, type ComponentType } from "react"
import { Download, TriangleAlert } from "lucide-react"

import { Button } from "@/components/ui/button"
import { formatSize } from "@/format"
import { useI18n } from "@/i18n"
import type { PreviewViewProps } from "@/preview/types"

/**
 * Office files are ZIP packages the browser-side renderers parse whole, so
 * from this size on the user confirms before the whole body is fetched.
 */
export const OFFICE_WARN_BYTES = 100 * 1024 * 1024

/** What an Office renderer receives once the whole file is in memory. */
export interface OfficeRendererProps {
  data: ArrayBuffer
  /**
   * The renderer could not show the file; the surface falls back to
   * Download. Stable across renders, so effects may depend on it.
   */
  onError: (message?: string) => void
}

/**
 * The view shared by the Office providers: the large-file warning, then one
 * whole-file fetch of the media URL, then the format's renderer.
 */
export function OfficeView({
  descriptor,
  onError,
  onDownload,
  Renderer,
}: PreviewViewProps & { Renderer: ComponentType<OfficeRendererProps> }) {
  const [confirmed, setConfirmed] = useState(descriptor.size < OFFICE_WARN_BYTES)
  if (!confirmed) {
    return <LargeFileWarning size={descriptor.size} onContinue={() => setConfirmed(true)} onDownload={onDownload} />
  }
  return <WholeFile url={descriptor.url} onError={onError} Renderer={Renderer} />
}

function LargeFileWarning({
  size,
  onContinue,
  onDownload,
}: {
  size: number
  onContinue: () => void
  onDownload: () => void
}) {
  const { t } = useI18n()
  return (
    <div className="flex h-full min-h-0 items-center justify-center p-6">
      <div className="w-full max-w-[420px] rounded-card border border-line bg-card p-5">
        <div className="mb-4 flex items-start gap-3">
          <span aria-hidden className="grid size-10 shrink-0 place-items-center rounded-full bg-pill text-ctl-fg">
            <TriangleAlert className="size-5" />
          </span>
          <div>
            <p className="text-[13px] font-semibold">{t("preview.office.largeTitle")}</p>
            <p className="mt-1 text-[12.5px] text-fg-2">{t("preview.office.largeBody", { size: formatSize(size, t) })}</p>
          </div>
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onDownload}>
            <Download data-icon="inline-start" />
            {t("drive.download")}
          </Button>
          <Button size="sm" onClick={onContinue}>
            {t("preview.office.continue")}
          </Button>
        </div>
      </div>
    </div>
  )
}

function WholeFile({
  url,
  onError,
  Renderer,
}: {
  url: string
  onError: (message?: string) => void
  Renderer: ComponentType<OfficeRendererProps>
}) {
  const { t } = useI18n()
  const [data, setData] = useState<ArrayBuffer | null>(null)
  // The surface passes a fresh onError each render; the fetch must not restart for it.
  const onErrorRef = useRef(onError)
  useEffect(() => {
    onErrorRef.current = onError
  })
  const fail = useCallback((message?: string) => onErrorRef.current(message), [])

  useEffect(() => {
    const abort = new AbortController()
    fetch(url, { signal: abort.signal })
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        return res.arrayBuffer()
      })
      .then(
        (buf) => setData(buf),
        () => {
          if (!abort.signal.aborted) fail()
        },
      )
    return () => abort.abort()
  }, [url, fail])

  if (!data) return <p className="py-16 text-center text-muted-foreground">{t("preview.loading")}</p>
  return <Renderer data={data} onError={fail} />
}

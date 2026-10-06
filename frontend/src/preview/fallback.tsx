import { Download, File } from "lucide-react"

import { Button } from "@/components/ui/button"
import { formatDate, formatSize } from "@/format"
import { useI18n } from "@/i18n"
import type { PreviewViewProps } from "@/preview/types"

/** The file facts the fallback can show even when no descriptor exists. */
export interface FileFacts {
  name: string
  path: string
  size: number
  date: string
  mime?: string
}

/**
 * File details and Download under a one-line notice: the unsupported-type
 * view, and what the surface shows when a preview fails. Download never
 * depends on the preview having worked.
 */
export function FileDetails({
  file,
  notice,
  error,
  onDownload,
}: {
  file: FileFacts
  notice: string
  /** A failure's own message, shown as an alert under the notice. */
  error?: string
  onDownload: () => void
}) {
  const { t, locale } = useI18n()
  const rows: [string, string][] = [
    [t("preview.info.path"), file.path],
    [t("preview.info.size"), formatSize(file.size, t)],
    [t("preview.info.modified"), formatDate(file.date, locale)],
  ]
  if (file.mime) rows.splice(1, 0, [t("preview.info.type"), file.mime])
  return (
    <div className="flex h-full min-h-0 items-center justify-center p-6">
      <div className="w-full max-w-[420px] rounded-card border border-line bg-card p-5">
        <div className="mb-3 flex items-center gap-3">
          <span aria-hidden className="grid size-10 shrink-0 place-items-center rounded-full bg-pill text-ctl-fg">
            <File className="size-5" />
          </span>
          <p className="text-[12.5px] text-fg-2">{notice}</p>
        </div>
        {error && (
          <p role="alert" className="mb-3 rounded-control bg-red-soft px-2 py-1.5 text-[12px] break-words text-red">
            {error}
          </p>
        )}
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[12px]">
          {rows.map(([label, value]) => (
            <div key={label} className="contents">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="min-w-0 break-all text-fg">{value}</dd>
            </div>
          ))}
        </dl>
        <div className="mt-4 flex justify-end">
          <Button size="sm" onClick={onDownload}>
            <Download data-icon="inline-start" />
            {t("drive.download")}
          </Button>
        </div>
      </div>
    </div>
  )
}

/** The fallback provider's view: no provider handles this type. */
export function FallbackPreview({ descriptor, onDownload }: PreviewViewProps) {
  const { t } = useI18n()
  return <FileDetails file={descriptor} notice={t("preview.unsupported")} onDownload={onDownload} />
}

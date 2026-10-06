import { useEffect } from "react"

import { Button } from "@/components/ui/button"
import { formatSize } from "@/format"
import { useI18n } from "@/i18n"
import type { TextChunks } from "@/preview/text-chunks"
import type { PreviewViewProps } from "@/preview/types"

/**
 * The "partial file" notice and Load more control under a text or Markdown
 * view; nothing once the whole file is shown.
 */
export function PartialNotice({ chunks, size }: { chunks: Extract<TextChunks, { state: "ready" }>; size: number }) {
  const { t } = useI18n()
  if (chunks.complete) return null
  return (
    <div
      role="status"
      className="flex shrink-0 items-center justify-between gap-3 border-t border-line bg-card px-4 py-2 text-[12px] text-fg-2"
    >
      <span>{t("preview.text.partial", { loaded: formatSize(chunks.loaded, t), size: formatSize(size, t) })}</span>
      <Button size="sm" variant="outline" disabled={chunks.loadingMore} onClick={chunks.loadMore}>
        {chunks.loadingMore ? t("preview.text.loadingMore") : t("preview.text.loadMore")}
      </Button>
    </div>
  )
}

/** Reports a failed read to the surface, which falls back to Download. */
export function useChunkFailure(chunks: TextChunks, onError: PreviewViewProps["onError"]) {
  const message = chunks.state === "failed" ? chunks.message : undefined
  useEffect(() => {
    if (message !== undefined) onError(message)
  }, [message, onError])
}

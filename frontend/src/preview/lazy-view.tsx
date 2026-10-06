import { lazy, Suspense, useEffect, type ComponentType } from "react"

import { useI18n } from "@/i18n"
import type { PreviewViewProps } from "@/preview/types"

function ChunkFailed({ onError }: PreviewViewProps) {
  useEffect(() => onError(), [onError])
  return null
}

/**
 * A provider View whose code (and libraries) load only when a file of its
 * type is first previewed, so they stay out of the app's initial bundle. A
 * chunk that fails to load reports a preview failure instead of throwing
 * past the surface.
 */
export function lazyView(load: () => Promise<ComponentType<PreviewViewProps>>): ComponentType<PreviewViewProps> {
  const View = lazy<ComponentType<PreviewViewProps>>(() => load().then((C) => ({ default: C }), () => ({ default: ChunkFailed })))
  return function LazyView(props: PreviewViewProps) {
    const { t } = useI18n()
    return (
      <Suspense fallback={<p className="py-16 text-center text-muted-foreground">{t("preview.loading")}</p>}>
        <View {...props} />
      </Suspense>
    )
  }
}

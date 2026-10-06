import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react"
import { Download, X } from "lucide-react"

import type { Backend, BackendError, Entry, PreviewDescriptor } from "@/backend"
import { Button } from "@/components/ui/button"
import { formatDate, formatSize } from "@/format"
import { useI18n } from "@/i18n"
import { FileDetails } from "@/preview/fallback"
import { previewProviders } from "@/preview/providers"
import { choosePreview, extensionOf, fallbackPreview } from "@/preview/registry"
import { focusableIn } from "@/sheet"

type Prepared =
  | { state: "loading" }
  | { state: "ready"; descriptor: PreviewDescriptor }
  | { state: "failed"; error: BackendError }

/** The topmost open modal dialog: the one Escape and Tab belong to. */
function topmostModal(): Element | null {
  const open = document.querySelectorAll('[role="dialog"][aria-modal="true"]')
  return open.length > 0 ? open[open.length - 1] : null
}

/**
 * The one preview surface: a full-window modal over the Drive listing, so
 * the directory and its scroll position stay exactly as they were. It
 * prepares the file through drive.preview, picks the registry's provider,
 * and falls back to file details and Download whenever preview fails.
 */
export function PreviewSurface({
  backend,
  entry,
  onDownload,
  onClose,
}: {
  backend: Backend
  entry: Entry
  onDownload: () => void
  onClose: () => void
}) {
  const { t, locale } = useI18n()
  const dialogRef = useRef<HTMLDivElement>(null)
  const closeRef = useRef<HTMLButtonElement>(null)
  // The opener, captured at mount before focus moves into the surface.
  const [returnFocus] = useState<HTMLElement | null>(() => document.activeElement as HTMLElement | null)
  const [prepared, setPrepared] = useState<Prepared>({ state: "loading" })
  // undefined: the view is fine; a string (possibly empty): it gave up.
  const [viewError, setViewError] = useState<string | undefined>(undefined)
  const onCloseRef = useRef(onClose)
  useEffect(() => {
    onCloseRef.current = onClose
  })

  useEffect(() => {
    let live = true
    backend.drive.preview(entry.path).then(
      (descriptor) => live && setPrepared({ state: "ready", descriptor }),
      (error: BackendError) => live && setPrepared({ state: "failed", error }),
    )
    return () => {
      live = false
    }
  }, [backend, entry.path])

  useEffect(() => {
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return
      // A fullscreen viewer owns Escape (the browser leaves fullscreen), and
      // a sheet opened above the preview closes first.
      if (document.fullscreenElement) return
      if (topmostModal() !== dialogRef.current) return
      e.stopPropagation()
      onCloseRef.current()
    }
    document.addEventListener("keydown", onKey)
    return () => {
      document.removeEventListener("keydown", onKey)
      returnFocus?.focus()
    }
  }, [returnFocus])

  const trapTab = (e: ReactKeyboardEvent) => {
    if (e.key !== "Tab" || !dialogRef.current || topmostModal() !== dialogRef.current) return
    const controls = focusableIn(dialogRef.current)
    if (controls.length === 0) {
      e.preventDefault()
      return
    }
    const first = controls[0]
    const last = controls[controls.length - 1]
    const active = document.activeElement
    if (e.shiftKey && (active === first || !dialogRef.current.contains(active))) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && (active === last || !dialogRef.current.contains(active))) {
      e.preventDefault()
      first.focus()
    }
  }

  const ext = extensionOf(entry.name)
  const meta = [ext ? ext.toUpperCase() : t("drive.file"), formatSize(entry.size, t), formatDate(entry.date, locale)]
  const facts = {
    name: entry.name,
    path: entry.path,
    size: entry.size,
    date: entry.date,
    ...(prepared.state === "ready" && prepared.descriptor.mime ? { mime: prepared.descriptor.mime } : {}),
  }

  let body
  if (prepared.state === "loading") {
    body = <p className="py-16 text-center text-muted-foreground">{t("preview.loading")}</p>
  } else if (prepared.state === "failed") {
    body = (
      <FileDetails file={facts} notice={t("preview.failed")} error={prepared.error.message} onDownload={onDownload} />
    )
  } else {
    const provider = choosePreview(prepared.descriptor, previewProviders)
    const { View } = provider
    body =
      viewError !== undefined || (prepared.descriptor.url === "" && provider !== fallbackPreview) ? (
        <FileDetails file={facts} notice={t("preview.failed")} error={viewError || undefined} onDownload={onDownload} />
      ) : (
        <View
          descriptor={prepared.descriptor}
          onError={(message) => setViewError(message ?? "")}
          onDownload={onDownload}
        />
      )
  }

  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-label={entry.name}
      tabIndex={-1}
      onKeyDown={trapTab}
      className="fixed inset-0 z-40 flex flex-col bg-background"
    >
      <header className="flex min-h-12 shrink-0 items-center gap-3 border-b border-line px-4 py-2">
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-[13px] font-semibold tracking-[-.01em]">{entry.name}</h2>
          <p className="truncate text-[11.5px] text-muted-foreground tabular-nums">{meta.join(" · ")}</p>
        </div>
        <Button variant="outline" size="sm" onClick={onDownload}>
          <Download data-icon="inline-start" />
          {t("drive.download")}
        </Button>
        <Button
          ref={closeRef}
          variant="ghost"
          size="icon-sm"
          aria-label={t("preview.close")}
          title={t("preview.close")}
          onClick={onClose}
          className="text-ctl-fg"
        >
          <X aria-hidden className="size-4" />
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-auto">{body}</div>
    </div>
  )
}

import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react"

import type { BackendError } from "@/backend"
import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n"

/** The focusable controls of a modal sheet, in tab order. */
export function focusableIn(root: HTMLElement): HTMLElement[] {
  return Array.from(
    root.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  )
}

/** The centred modal sheet every destructive or confirming action uses. */
export function Sheet({
  title,
  onClose,
  children,
}: {
  title: string
  onClose: () => void
  children: ReactNode
}) {
  const dialogRef = useRef<HTMLDivElement>(null)
  // The control that opened the sheet, captured at mount: by the time an
  // effect runs, a child's autoFocus may already have moved focus into
  // the sheet, so the capture happens in the (once-only) state initializer.
  const [returnFocus] = useState<HTMLElement | null>(() => document.activeElement as HTMLElement | null)
  // onClose changes identity every render; the keydown subscription below
  // subscribes once, so it calls through a ref kept current per commit.
  const onCloseRef = useRef(onClose)
  useEffect(() => {
    onCloseRef.current = onClose
  })

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    // Focus lands inside the sheet (a field's autoFocus counts); nothing
    // interactive stays behind the modal.
    if (!dialog.contains(document.activeElement)) {
      ;(focusableIn(dialog)[0] ?? dialog).focus()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation()
        onCloseRef.current()
      }
    }
    document.addEventListener("keydown", onKey)
    return () => {
      document.removeEventListener("keydown", onKey)
      // The opener may be gone (the row it acted on was deleted); focusing
      // a detached element is a no-op and focus falls back to the page.
      returnFocus?.focus()
    }
  }, [returnFocus])

  // Tab cycles inside the sheet: the modal owns focus while it is open.
  const trapTab = (e: ReactKeyboardEvent) => {
    if (e.key !== "Tab" || !dialogRef.current) return
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

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[18vh]">
      <div aria-hidden className="absolute inset-0 bg-black/25" onClick={onClose} />
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        onKeyDown={trapTab}
        className="relative max-h-[76vh] w-[380px] overflow-y-auto overscroll-contain rounded-card border border-line bg-popover p-4 shadow-pop"
      >
        <h2 className="mb-3 text-[13px] font-semibold">{title}</h2>
        {children}
      </div>
    </div>
  )
}

export function SheetError({ error }: { error: BackendError | null }) {
  if (!error) return null
  return (
    <p role="alert" className="mt-2 rounded-control bg-red-soft px-2 py-1.5 text-[12px] text-red">
      {error.message}
    </p>
  )
}

export function SheetButtons({
  confirmLabel,
  destructive,
  busy,
  disabled,
  onConfirm,
  onClose,
}: {
  confirmLabel: string
  destructive?: boolean
  busy: boolean
  /** Extra gating beyond busy (a confirmation the sheet waits on). */
  disabled?: boolean
  onConfirm: () => void
  onClose: () => void
}) {
  const { t } = useI18n()
  return (
    <div className="mt-4 flex justify-end gap-1.5">
      <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
        {t("sheet.cancel")}
      </Button>
      <Button
        variant={destructive ? "destructive" : "default"}
        size="sm"
        onClick={onConfirm}
        disabled={busy || disabled}
      >
        {confirmLabel}
      </Button>
    </div>
  )
}

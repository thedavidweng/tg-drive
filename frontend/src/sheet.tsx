import type { ReactNode } from "react"

import type { BackendError } from "@/backend"
import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n"

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
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[18vh]">
      <div aria-hidden className="absolute inset-0 bg-black/25" onClick={onClose} />
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="relative w-[380px] rounded-card border border-line bg-popover p-4 shadow-pop"
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
  onConfirm,
  onClose,
}: {
  confirmLabel: string
  destructive?: boolean
  busy: boolean
  onConfirm: () => void
  onClose: () => void
}) {
  const { t } = useI18n()
  return (
    <div className="mt-4 flex justify-end gap-1.5">
      <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
        {t("sheet.cancel")}
      </Button>
      <Button variant={destructive ? "destructive" : "default"} size="sm" onClick={onConfirm} disabled={busy}>
        {confirmLabel}
      </Button>
    </div>
  )
}

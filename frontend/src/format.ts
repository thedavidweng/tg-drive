import type { Locale, Translate } from "@/i18n"

/** A byte count as "512 B", "1.5 KB", "2 MB", "3 GB". */
export function formatSize(bytes: number, t: Translate): string {
  if (bytes < 1024) return t("size.b", { n: bytes })
  const units = ["size.kb", "size.mb", "size.gb"] as const
  let n = bytes / 1024
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return t(units[i], { n: n >= 10 ? Math.round(n) : Math.round(n * 10) / 10 })
}

/** An RFC3339 timestamp in the locale's medium date + short time form. */
export function formatDate(iso: string, locale: Locale): string {
  const d = new Date(iso)
  if (!iso || Number.isNaN(d.getTime())) return "—"
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(d)
}

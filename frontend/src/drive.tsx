import { useEffect, useState } from "react"
import { File, Folder } from "lucide-react"

import type { Backend, BackendError, Entry } from "@/backend"
import { useI18n, type Translate } from "@/i18n"

type Listing = { state: "loading" } | { state: "ready"; entries: Entry[] } | { state: "failed"; error: BackendError }

export function DriveScreen({ backend, path }: { backend: Backend; path: string }) {
  const { t } = useI18n()
  const [listing, setListing] = useState<Listing>({ state: "loading" })

  useEffect(() => {
    let live = true
    backend.drive.list(path).then(
      (entries) => live && setListing({ state: "ready", entries }),
      (error: BackendError) => live && setListing({ state: "failed", error }),
    )
    return () => {
      live = false
    }
  }, [backend, path])

  if (listing.state === "loading") {
    return <p className="px-1 py-6 text-center text-muted-foreground">{t("drive.loading")}</p>
  }
  if (listing.state === "failed") {
    return (
      <div role="alert" className="rounded-card border border-line bg-red-soft px-3.5 py-3 text-red">
        <p>{listing.error.message}</p>
        <p className="mt-1 font-mono text-[11.5px] opacity-80">{listing.error.code}</p>
      </div>
    )
  }
  if (listing.entries.length === 0) {
    return <p className="px-1 py-6 text-center text-muted-foreground">{t("drive.empty")}</p>
  }
  return (
    <ul
      aria-label={t("drive.listLabel", { path })}
      className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card"
    >
      {listing.entries.map((e) => (
        <li
          key={e.path}
          className="flex min-h-12 items-center gap-3 py-[7px] pr-3 pl-3.5"
        >
          {e.type === "dir" ? (
            <Folder aria-hidden className="size-[18px] shrink-0 text-primary" />
          ) : (
            <File aria-hidden className="size-[18px] shrink-0 text-muted-foreground" />
          )}
          <span className="min-w-0 flex-1 truncate font-medium tracking-[-.005em]">{e.name}</span>
          <span className="shrink-0 text-[11.5px] text-muted-foreground tabular-nums">
            {e.type === "dir" ? t("drive.folder") : formatSize(e.size, t)}
          </span>
        </li>
      ))}
    </ul>
  )
}

function formatSize(bytes: number, t: Translate): string {
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

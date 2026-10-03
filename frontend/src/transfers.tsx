import { useEffect, useState } from "react"
import { ArrowDownToLine, ArrowUpFromLine, RotateCcw, XCircle } from "lucide-react"

import type { Backend, BackendError, Transfer } from "@/backend"
import { isTerminalStage } from "@/backend"
import { Button } from "@/components/ui/button"
import { formatDate, formatSize } from "@/format"
import { isMessageKey, useI18n, type Locale, type Translate } from "@/i18n"
import { ErrorText } from "@/sheet"

/** Kinds counting bytes (single-file) versus items (multi-file). */
const byteKinds = new Set(["upload", "download"])

type ListState =
  | { state: "loading" }
  | { state: "ready" }
  | { state: "failed"; error: BackendError }

export function TransfersScreen({ backend }: { backend: Backend }) {
  const { t, locale } = useI18n()
  const [list, setList] = useState<ListState>({ state: "loading" })
  const [transfers, setTransfers] = useState<Transfer[]>([])
  const [actionError, setActionError] = useState<BackendError | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  // The initial load; the typed events keep it live from there.
  useEffect(() => {
    let live = true
    backend.transfers.list().then(
      (l) => {
        if (!live) return
        setTransfers([...(l.active ?? []), ...(l.history ?? [])])
        setList({ state: "ready" })
      },
      (error: BackendError) => live && setList({ state: "failed", error }),
    )
    return () => {
      live = false
    }
  }, [backend])

  useEffect(() => {
    // A stage event inserts a new Transfer at the top; progress events
    // update in place so a row does not jump while it runs.
    const offStage = backend.events.onTransferStage((tr) =>
      setTransfers((all) => [tr, ...all.filter((x) => x.id !== tr.id)]),
    )
    const offProgress = backend.events.onTransferProgress((tr) =>
      setTransfers((all) =>
        all.some((x) => x.id === tr.id) ? all.map((x) => (x.id === tr.id ? tr : x)) : [tr, ...all],
      ),
    )
    const offRemoved = backend.events.onTransferRemoved((e) =>
      setTransfers((all) => all.filter((x) => x.id !== e.id)),
    )
    return () => {
      offStage()
      offProgress()
      offRemoved()
    }
  }, [backend])

  const act = async (id: string, call: () => Promise<unknown>) => {
    if (busy) return
    setBusy(id)
    setActionError(null)
    try {
      await call()
    } catch (error) {
      setActionError(error as BackendError)
    } finally {
      setBusy(null)
    }
  }

  const clearFinished = () =>
    act("clear", async () => {
      await backend.transfers.clearFinished()
    })

  if (list.state === "loading") {
    return <p className="px-1 py-6 text-center text-muted-foreground">{t("transfers.loading")}</p>
  }
  if (list.state === "failed") {
    return (
      <div role="alert" className="rounded-card border border-line bg-red-soft px-3.5 py-3 text-red">
        <p>{list.error.message}</p>
        <p className="mt-1 font-mono text-[11.5px] opacity-80">{list.error.code}</p>
      </div>
    )
  }

  const active = transfers.filter((tr) => !isTerminalStage(tr.stage))
  const history = transfers.filter((tr) => isTerminalStage(tr.stage))

  return (
    <div>
      {actionError && (
        <p role="alert" className="mb-2 rounded-control bg-red-soft px-2 py-1.5 text-[12px] text-red">
          <ErrorText message={actionError.message} code={actionError.code} />
        </p>
      )}
      <section aria-label={t("transfers.active")} className="mb-4">
        <h2 className="mb-1.5 px-1 text-[11.5px] font-medium tracking-wide text-muted-foreground uppercase">
          {t("transfers.active")}
        </h2>
        {active.length === 0 ? (
          <p className="px-1 py-3 text-[12.5px] text-muted-foreground">{t("transfers.noActive")}</p>
        ) : (
          <ul
            aria-label={t("transfers.activeListLabel")}
            className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card"
          >
            {active.map((tr) => (
              <TransferRow
                key={tr.id}
                tr={tr}
                busy={busy === tr.id}
                locale={locale}
                t={t}
                onCancel={() => act(tr.id, () => backend.transfers.cancel(tr.id))}
                onRetry={() => act(tr.id, () => backend.transfers.retry(tr.id))}
              />
            ))}
          </ul>
        )}
      </section>
      <section aria-label={t("transfers.history")}>
        <div className="mb-1.5 flex items-center justify-between px-1">
          <h2 className="text-[11.5px] font-medium tracking-wide text-muted-foreground uppercase">
            {t("transfers.history")}
          </h2>
          {history.length > 0 && (
            <Button variant="ghost" size="sm" disabled={busy === "clear"} onClick={clearFinished}>
              {t("transfers.clearFinished")}
            </Button>
          )}
        </div>
        {history.length === 0 ? (
          <p className="px-1 py-3 text-[12.5px] text-muted-foreground">{t("transfers.noHistory")}</p>
        ) : (
          <ul
            aria-label={t("transfers.historyListLabel")}
            className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card"
          >
            {history.map((tr) => (
              <TransferRow
                key={tr.id}
                tr={tr}
                busy={busy === tr.id}
                locale={locale}
                t={t}
                onCancel={() => act(tr.id, () => backend.transfers.cancel(tr.id))}
                onRetry={() => act(tr.id, () => backend.transfers.retry(tr.id))}
              />
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

function TransferRow({
  tr,
  busy,
  locale,
  t,
  onCancel,
  onRetry,
}: {
  tr: Transfer
  busy: boolean
  locale: Locale
  t: Translate
  onCancel: () => void
  onRetry: () => void
}) {
  const terminal = isTerminalStage(tr.stage)
  const upload = tr.kind === "upload" || tr.kind === "album_upload" || tr.kind === "recursive_upload"
  const Icon = upload ? ArrowUpFromLine : ArrowDownToLine
  // The label a person recognizes: what is moving, and where to.
  const title = upload ? tr.dest : tr.source
  const detail = upload ? tr.source : tr.dest
  return (
    <li className="flex min-h-12 items-center gap-3 py-[7px] pr-3 pl-3.5">
      <Icon aria-hidden className="size-[18px] shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate font-medium tracking-[-.005em]">{title || tr.id}</span>
          <StagePill stage={tr.stage} t={t} />
          {tr.front_end === "cli" && (
            <span className="flex-none rounded-full bg-pill px-1.5 py-px text-[10.5px] text-muted-foreground">
              {t("transfers.cliBadge")}
            </span>
          )}
        </span>
        <span className="mt-0.5 block truncate text-[11.5px] text-muted-foreground">
          {kindLabel(tr, t)}
          {detail && detail !== title ? ` · ${detail}` : ""}
          {tr.items_failed ? ` · ${t("transfers.itemsFailed", { failed: tr.items_failed })}` : ""}
        </span>
        {tr.stage === "failed" && (
          <span className="mt-0.5 block text-[11.5px] text-red">
            <ErrorText message={tr.error_message} code={tr.error_code} />
          </span>
        )}
        {!terminal && <TransferProgress tr={tr} t={t} />}
        {terminal && tr.finished_at && (
          <span className="mt-0.5 block text-[11.5px] text-muted-foreground tabular-nums">
            {formatDate(tr.finished_at, locale)}
          </span>
        )}
      </span>
      {!terminal && (
        <span className="flex shrink-0 items-center gap-0.5">
          <button
            type="button"
            aria-label={t("transfers.cancel", { name: title })}
            title={t("transfers.cancel", { name: title })}
            disabled={busy || tr.cancel_requested}
            onClick={onCancel}
            className="grid size-[26px] place-items-center rounded-[7px] text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg disabled:opacity-40"
          >
            <XCircle aria-hidden className="size-4" />
          </button>
        </span>
      )}
      {terminal && tr.stage !== "completed" && (
        <span className="flex shrink-0 items-center gap-0.5">
          <button
            type="button"
            aria-label={t("transfers.retry", { name: title })}
            title={t("transfers.retry", { name: title })}
            disabled={busy}
            onClick={onRetry}
            className="grid size-[26px] place-items-center rounded-[7px] text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg disabled:opacity-40"
          >
            <RotateCcw aria-hidden className="size-4" />
          </button>
        </span>
      )}
    </li>
  )
}

// Stages and kinds come from the shared index, where a newer td may have
// written one this build has no string for; those show as written.
function kindLabel(tr: Transfer, t: Translate): string {
  const key = `transfer.kind.${tr.kind}`
  return isMessageKey(key) ? t(key) : tr.kind
}

function StagePill({ stage, t }: { stage: string; t: Translate }) {
  const tone = isTerminalStage(stage)
    ? stage === "completed"
      ? "bg-green-soft text-green"
      : stage === "failed"
        ? "bg-red-soft text-red"
        : "bg-pill text-muted-foreground"
    : stage === "queued"
      ? "bg-pill text-muted-foreground"
      : "bg-seg-track text-fg"
  const key = `transfer.stage.${stage}`
  return (
    <span data-stage={stage} className={`flex-none rounded-full px-1.5 py-px text-[10.5px] font-medium ${tone}`}>
      {isMessageKey(key) ? t(key) : stage}
    </span>
  )
}

function TransferProgress({ tr, t }: { tr: Transfer; t: Translate }) {
  const byBytes = byteKinds.has(tr.kind)
  const done = byBytes ? tr.bytes_done : tr.items_done
  const total = byBytes ? tr.bytes_total : tr.items_total
  const pct = total > 0 ? Math.min(1, done / total) : 0
  const text = byBytes
    ? total > 0
      ? `${formatSize(tr.bytes_done, t)} / ${formatSize(tr.bytes_total, t)} · ${Math.round(pct * 100)}%`
      : formatSize(tr.bytes_done, t)
    : t("transfers.items", { done: tr.items_done, total: tr.items_total })
  return (
    <span className="mt-1 flex items-center gap-2">
      <span
        role="progressbar"
        aria-valuenow={Math.round(pct * 100)}
        aria-valuemin={0}
        aria-valuemax={100}
        className="h-1.5 w-full max-w-56 overflow-hidden rounded-full bg-pill"
      >
        <span
          className="block h-full rounded-full bg-primary transition-[width] duration-150 ease-quiet"
          style={{ width: `${pct * 100}%` }}
        />
      </span>
      <span className="flex-none text-[11.5px] text-muted-foreground tabular-nums">{text}</span>
    </span>
  )
}

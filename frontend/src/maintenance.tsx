import { useState } from "react"
import { Stethoscope, Wrench } from "lucide-react"

import type {
  AdoptItem,
  AdoptOutcome,
  Backend,
  BackendError,
  DoctorCheck,
  DoctorReport,
  ItemEvent,
  PathCodecReport,
  RepairItem,
  RepairOutcome,
} from "@/backend"
import { Card, Segmented } from "@/card"
import { Button } from "@/components/ui/button"
import { Sheet, SheetButtons } from "@/sheet"
import { useI18n, type Translate } from "@/i18n"

export function MaintenanceScreen({ backend }: { backend: Backend }) {
  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-3.5">
      <AdoptCard backend={backend} />
      <RepairCard backend={backend} />
      <DiagnosticsCard backend={backend} />
    </div>
  )
}

function Checkbox({
  label,
  checked,
  onChange,
}: {
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="flex items-center gap-2 text-[12.5px] text-fg-2">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="size-3.5" />
      {label}
    </label>
  )
}

function FailedAlert({ error }: { error: BackendError }) {
  return (
    <div role="alert" className="rounded-card border border-line bg-red-soft px-3.5 py-3 text-red">
      <p>{error.message}</p>
      <p className="mt-1 font-mono text-[11.5px] opacity-80">{error.code}</p>
    </div>
  )
}

// --- adopt ------------------------------------------------------------------

type AdoptPhase =
  | { state: "idle" }
  | { state: "previewing" }
  | { state: "planned"; plan: AdoptOutcome }
  | { state: "confirming" }
  | { state: "running" }
  | { state: "done"; outcome: AdoptOutcome }
  | { state: "failed"; error: BackendError }

function AdoptCard({ backend }: { backend: Backend }) {
  const { t } = useI18n()
  const [into, setInto] = useState("")
  const [noHash, setNoHash] = useState(false)
  const [rewriteCaptions, setRewriteCaptions] = useState(false)
  const [phase, setPhase] = useState<AdoptPhase>({ state: "idle" })

  const busy = phase.state === "previewing" || phase.state === "running"
  const options = () => ({
    into: into.trim(),
    no_hash: noHash,
    rewrite_captions: rewriteCaptions,
  })

  const preview = async () => {
    setPhase({ state: "previewing" })
    try {
      const plan = await backend.maintenance.previewAdopt(options())
      setPhase({ state: "planned", plan })
    } catch (error) {
      setPhase({ state: "failed", error: error as BackendError })
    }
  }

  const run = async () => {
    setPhase({ state: "running" })
    try {
      const outcome = await backend.maintenance.adopt({ ...options(), confirm: true })
      setPhase({ state: "done", outcome })
    } catch (error) {
      setPhase({ state: "failed", error: error as BackendError })
    }
  }

  const outcome = phase.state === "planned" ? phase.plan : phase.state === "done" ? phase.outcome : null

  return (
    <Card label={t("maintenance.adoptHeading")}>
      <li className="flex flex-col gap-3 px-3.5 py-3">
        <p className="text-[12.5px] text-muted-foreground">{t("maintenance.adoptIntro")}</p>
        <label className="flex items-center gap-3 text-[12.5px]">
          <span className="w-28 shrink-0 text-fg-2">{t("maintenance.adoptInto")}</span>
          <input
            aria-label={t("maintenance.adoptInto")}
            value={into}
            onChange={(e) => setInto(e.target.value)}
            placeholder="/"
            spellCheck={false}
            className="min-w-0 flex-1 rounded-control border border-line bg-card-2 px-2 py-1 font-mono text-[12px] outline-none focus:border-primary"
          />
        </label>
        <Checkbox label={t("maintenance.noHash")} checked={noHash} onChange={setNoHash} />
        <Checkbox label={t("maintenance.rewriteCaptions")} checked={rewriteCaptions} onChange={setRewriteCaptions} />
        <div className="flex items-center gap-1.5 pt-1">
          <Button variant="outline" size="sm" onClick={preview} disabled={busy}>
            {phase.state === "previewing" ? t("maintenance.previewing") : t("maintenance.preview")}
          </Button>
          <Button size="sm" onClick={() => setPhase({ state: "confirming" })} disabled={busy}>
            {t("maintenance.adoptRun")}
          </Button>
        </div>
        {phase.state === "failed" && <FailedAlert error={phase.error} />}
        {outcome && (
          <>
            <p role="status" className="text-[12px] text-muted-foreground">
              {outcome.dry_run
                ? t("maintenance.adoptPlanSummary", { adopted: outcome.adopted, skipped: outcome.skipped })
                : t("maintenance.adoptSummary", {
                    adopted: outcome.adopted,
                    skipped: outcome.skipped,
                    failed: outcome.failed,
                  })}
            </p>
            {(outcome.items ?? []).length > 0 && (
              <ul
                aria-label={t("maintenance.adoptItemsLabel")}
                className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card-2"
              >
                {(outcome.items ?? []).map((item, i) => (
                  <AdoptItemRow key={`${item.message_id}-${i}`} item={item} />
                ))}
              </ul>
            )}
          </>
        )}
      </li>
      {phase.state === "confirming" && (
        <Sheet title={t("maintenance.adoptConfirmTitle")} onClose={() => setPhase({ state: "idle" })}>
          <p className="text-[12.5px] text-fg-2">{t("maintenance.adoptConfirmBody")}</p>
          <SheetButtons
            confirmLabel={t("maintenance.adoptConfirm")}
            busy={false}
            onConfirm={() => void run()}
            onClose={() => setPhase({ state: "idle" })}
          />
        </Sheet>
      )}
    </Card>
  )
}

function AdoptItemRow({ item }: { item: AdoptItem }) {
  const { t } = useI18n()
  return (
    <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5 text-[12.5px]">
      <span className="w-14 shrink-0 font-mono text-[11.5px] text-muted-foreground">#{item.message_id}</span>
      <span className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] ${adoptBadgeClass(item.action)}`}>
        {adoptActionLabel(item.action, t)}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-[12px]">
        {item.path || item.file_name || item.reason}
      </span>
    </li>
  )
}

function adoptBadgeClass(action: string): string {
  switch (action) {
    case "adopt":
    case "restore":
    case "manifest":
      return "bg-green-soft text-green"
    case "skip":
      return "bg-pill text-muted-foreground"
    default:
      return "bg-red-soft text-red"
  }
}

function adoptActionLabel(action: string, t: Translate): string {
  switch (action) {
    case "adopt":
      return t("maintenance.adoptAction.adopt")
    case "restore":
    case "manifest":
      return t("maintenance.adoptAction.restore")
    case "skip":
      return t("maintenance.adoptAction.skip")
    default:
      return t("maintenance.adoptAction.fail")
  }
}

// --- repair -----------------------------------------------------------------

type RepairMode = "pending" | "orphaned" | "scan_errors" | "hash" | "captions" | "path"

type RepairPhase =
  | { state: "idle" }
  | { state: "confirming" }
  | { state: "running"; items: ItemEvent[] }
  | { state: "done"; outcome: RepairOutcome }
  | { state: "failed"; error: BackendError }

function RepairCard({ backend }: { backend: Backend }) {
  const { t } = useI18n()
  const [mode, setMode] = useState<RepairMode>("pending")
  const [path, setPath] = useState("")
  const [dryRun, setDryRun] = useState(false)
  const [continueOnError, setContinueOnError] = useState(false)
  const [deleteOrphaned, setDeleteOrphaned] = useState(false)
  const [phase, setPhase] = useState<RepairPhase>({ state: "idle" })

  const busy = phase.state === "running"
  const wantsPath = mode === "path" || mode === "captions" || mode === "hash"

  const options = () => ({
    mode,
    path: wantsPath ? path.trim() : "",
    dry_run: mode === "captions" ? dryRun : false,
    continue_on_error: mode === "captions" ? continueOnError : false,
    delete_orphaned: mode === "orphaned" ? deleteOrphaned : false,
  })

  const run = async () => {
    setPhase({ state: "running", items: [] })
    const items: ItemEvent[] = []
    const off = backend.maintenance.onRepairItem((e) => {
      items.push(e)
      setPhase((p) => (p.state === "running" ? { state: "running", items: [...items] } : p))
    })
    try {
      const outcome = await backend.maintenance.repair({ ...options(), confirm: true })
      setPhase({ state: "done", outcome })
    } catch (error) {
      setPhase({ state: "failed", error: error as BackendError })
    } finally {
      off()
    }
  }

  const destructive = mode === "orphaned" && deleteOrphaned

  return (
    <Card label={t("maintenance.repairHeading")}>
      <li className="flex flex-col gap-3 px-3.5 py-3">
        <Segmented<RepairMode>
          label={t("maintenance.repairMode")}
          value={mode}
          onChange={setMode}
          options={[
            { value: "pending", name: t("maintenance.repairMode.pending") },
            { value: "orphaned", name: t("maintenance.repairMode.orphaned") },
            { value: "scan_errors", name: t("maintenance.repairMode.scanErrors") },
            { value: "hash", name: t("maintenance.repairMode.hash") },
            { value: "captions", name: t("maintenance.repairMode.captions") },
            { value: "path", name: t("maintenance.repairMode.path") },
          ]}
        />
        {wantsPath && (
          <label className="flex items-center gap-3 text-[12.5px]">
            <span className="w-28 shrink-0 text-fg-2">{t("maintenance.repairPath")}</span>
            <input
              aria-label={t("maintenance.repairPath")}
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder={t("maintenance.repairPathPlaceholder")}
              spellCheck={false}
              className="min-w-0 flex-1 rounded-control border border-line bg-card-2 px-2 py-1 font-mono text-[12px] outline-none focus:border-primary"
            />
          </label>
        )}
        {mode === "captions" && (
          <>
            <Checkbox label={t("maintenance.repairDryRun")} checked={dryRun} onChange={setDryRun} />
            <Checkbox
              label={t("maintenance.repairContinueOnError")}
              checked={continueOnError}
              onChange={setContinueOnError}
            />
          </>
        )}
        {mode === "orphaned" && (
          <Checkbox
            label={t("maintenance.repairDeleteOrphaned")}
            checked={deleteOrphaned}
            onChange={setDeleteOrphaned}
          />
        )}
        <div className="flex items-center gap-1.5 pt-1">
          <Button
            size="sm"
            onClick={() => (destructive ? setPhase({ state: "confirming" }) : void run())}
            disabled={busy || (mode === "path" && path.trim() === "")}
          >
            <Wrench data-icon="inline-start" />
            {busy ? t("maintenance.repairRunning") : t("maintenance.repairRun")}
          </Button>
        </div>
        {phase.state === "failed" && <FailedAlert error={phase.error} />}
        {phase.state === "running" && phase.items.length > 0 && (
          <ul
            aria-label={t("maintenance.repairItemsLabel")}
            className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card-2"
          >
            {phase.items.map((item, i) => (
              <RepairEventRow key={i} item={item} />
            ))}
          </ul>
        )}
        {phase.state === "done" && <RepairResult outcome={phase.outcome} />}
      </li>
      {phase.state === "confirming" && (
        <Sheet title={t("maintenance.repairConfirmOrphansTitle")} onClose={() => setPhase({ state: "idle" })}>
          <p className="text-[12.5px] text-fg-2">{t("maintenance.repairConfirmOrphansBody")}</p>
          <SheetButtons
            confirmLabel={t("maintenance.repairConfirmOrphans")}
            destructive
            busy={false}
            onConfirm={() => void run()}
            onClose={() => setPhase({ state: "idle" })}
          />
        </Sheet>
      )}
    </Card>
  )
}

function RepairEventRow({ item }: { item: ItemEvent }) {
  const { t } = useI18n()
  return (
    <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5 text-[12.5px]">
      <span className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] ${itemStatusBadgeClass(item.status)}`}>
        {itemStatusLabel(item.status, t)}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{item.path || item.error}</span>
    </li>
  )
}

function itemStatusBadgeClass(status: string): string {
  switch (status) {
    case "completed":
      return "bg-green-soft text-green"
    case "skipped":
      return "bg-pill text-muted-foreground"
    default:
      return "bg-red-soft text-red"
  }
}

function itemStatusLabel(status: string, t: Translate): string {
  switch (status) {
    case "completed":
      return t("import.action.imported")
    case "skipped":
      return t("import.action.skipped")
    default:
      return t("import.action.failed")
  }
}

/** The finished repair rendered as counter rows plus any per-item list. */
function RepairResult({ outcome }: { outcome: RepairOutcome }) {
  const { t } = useI18n()
  const body = outcome.captions ?? outcome.hash ?? outcome.path ?? outcome.pending ?? outcome.orphaned ?? outcome.scan_errors
  if (!body) return null
  const counters: [string, number][] = []
  let items: RepairItem[] | null = null
  if (outcome.captions) {
    const c = outcome.captions
    counters.push(["total", c.total], ["planned", c.planned], ["cleaned", c.cleaned], ["skipped", c.skipped], ["failed", c.failed])
    items = c.items
  } else if (outcome.hash) {
    const h = outcome.hash
    counters.push(["total", h.total], ["backfilled", h.backfilled], ["failed", h.failed])
    items = h.items
  } else if (outcome.pending) {
    const p = outcome.pending
    counters.push(["repaired", p.repaired], ["invalid", p.invalid], ["orphaned", p.orphaned], ["skipped", p.skipped], ["locks_cleared", p.locks_cleared])
  } else if (outcome.orphaned) {
    const o = outcome.orphaned
    counters.push(["repaired", o.repaired], ["deleted", o.deleted], ["invalid", o.invalid])
  } else if (outcome.scan_errors) {
    const s = outcome.scan_errors
    counters.push(["resolved", s.resolved], ["pending", s.pending])
  }
  return (
    <div className="flex flex-col gap-2">
      <p role="status" className="text-[12px] text-muted-foreground">
        {outcome.path
          ? t("maintenance.repairPathDone", { path: outcome.path.repaired })
          : counters.map(([key, n]) => `${t(counterKeys[key], { count: n })}`).join(" · ")}
      </p>
      {items && items.length > 0 && (
        <ul
          aria-label={t("maintenance.repairItemsLabel")}
          className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card-2"
        >
          {items.map((item) => (
            <li key={item.path} className="flex min-h-9 items-center gap-3 px-3.5 py-1.5 text-[12.5px]">
              <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{item.path}</span>
              <span className="shrink-0 text-[11.5px] text-muted-foreground">
                {item.action}
                {item.reason ? ` — ${item.reason}` : ""}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// Counter names are the service result's field names; each maps to one i18n
// key taking a count.
const counterKeys: Record<string, Parameters<Translate>[0]> = {
  total: "maintenance.count.total",
  planned: "maintenance.count.planned",
  cleaned: "maintenance.count.cleaned",
  skipped: "maintenance.count.skipped",
  failed: "maintenance.count.failed",
  backfilled: "maintenance.count.backfilled",
  repaired: "maintenance.count.repaired",
  invalid: "maintenance.count.invalid",
  orphaned: "maintenance.count.orphaned",
  deleted: "maintenance.count.deleted",
  resolved: "maintenance.count.resolved",
  pending: "maintenance.count.pending",
  locks_cleared: "maintenance.count.locksCleared",
}

// --- diagnostics --------------------------------------------------------------

type DiagnosticsPhase =
  | { state: "idle" }
  | { state: "running" }
  | { state: "done"; report: DoctorReport; pathCodec: PathCodecReport }
  | { state: "failed"; error: BackendError }

function DiagnosticsCard({ backend }: { backend: Backend }) {
  const { t } = useI18n()
  const [phase, setPhase] = useState<DiagnosticsPhase>({ state: "idle" })

  const run = async () => {
    setPhase({ state: "running" })
    try {
      const [report, pathCodec] = await Promise.all([
        backend.maintenance.doctor(),
        backend.maintenance.pathCodecDoctor(),
      ])
      setPhase({ state: "done", report, pathCodec })
    } catch (error) {
      setPhase({ state: "failed", error: error as BackendError })
    }
  }

  const tally = { pass: 0, warn: 0, fail: 0, unknown: 0 }
  const checks = phase.state === "done" ? (phase.report.checks ?? []) : []
  for (const c of checks) {
    if (c.status in tally) tally[c.status as keyof typeof tally]++
  }

  return (
    <Card label={t("maintenance.diagnosticsHeading")}>
      <li className="flex flex-col gap-3 px-3.5 py-3">
        <div className="flex items-center gap-1.5">
          <Button size="sm" onClick={run} disabled={phase.state === "running"}>
            <Stethoscope data-icon="inline-start" />
            {phase.state === "running" ? t("maintenance.diagnosticsRunning") : t("maintenance.diagnosticsRun")}
          </Button>
          {phase.state === "done" && (
            <p role="status" className="px-1 text-[12px] text-muted-foreground">
              {t("maintenance.diagnosticsSummary", {
                pass: tally.pass,
                warn: tally.warn,
                failed: tally.fail,
              })}
            </p>
          )}
        </div>
        {phase.state === "failed" && <FailedAlert error={phase.error} />}
        {phase.state === "done" && (
          <>
            <ul
              aria-label={t("maintenance.diagnosticsChecksLabel")}
              className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card-2"
            >
              {checks.map((check) => (
                <DoctorCheckRow key={check.name} check={check} />
              ))}
              {phase.report.max_upload_bytes != null && (
                <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5 text-[12.5px]">
                  <span className="min-w-0 flex-1 font-mono text-[12px]">max_upload</span>
                  <span className="shrink-0 text-[11.5px] text-muted-foreground tabular-nums">
                    {formatSize(phase.report.max_upload_bytes, t)}
                  </span>
                </li>
              )}
            </ul>
            <p className="px-1 text-[11.5px] font-medium tracking-[.04em] text-muted-foreground uppercase">
              {t("maintenance.pathCodec")}
            </p>
            <ul
              aria-label={t("maintenance.pathCodec")}
              className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card-2"
            >
              <PathCodecRow label={t("maintenance.fixedVectors")} status={phase.pathCodec.fixed_vectors} />
              <PathCodecRow label={t("maintenance.dbCheck")} status={phase.pathCodec.db_check} />
              <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5 text-[12.5px]">
                <span className="min-w-0 flex-1">{t("maintenance.dbRows")}</span>
                <span className="shrink-0 text-[11.5px] text-muted-foreground tabular-nums">
                  {phase.pathCodec.db_rows} / {t("maintenance.corruptRows")}: {phase.pathCodec.corrupt_rows}
                </span>
              </li>
            </ul>
          </>
        )}
      </li>
    </Card>
  )
}

function DoctorCheckRow({ check }: { check: DoctorCheck }) {
  return (
    <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5 text-[12.5px]">
      <span className="min-w-0 flex-1">
        <span className="font-mono text-[12px]">{check.name}</span>
        {check.hint && <span className="block text-[11.5px] text-muted-foreground">{check.hint}</span>}
      </span>
      <StatusBadge status={check.status} />
    </li>
  )
}

function PathCodecRow({ label, status }: { label: string; status: string }) {
  return (
    <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5 text-[12.5px]">
      <span className="min-w-0 flex-1">{label}</span>
      <StatusBadge status={status} />
    </li>
  )
}

function StatusBadge({ status }: { status: string }) {
  const { t } = useI18n()
  const cls =
    status === "pass"
      ? "bg-green-soft text-green"
      : status === "warn"
        ? "bg-amber-soft text-amber"
        : status === "fail"
          ? "bg-red-soft text-red"
          : "bg-pill text-muted-foreground"
  return (
    <span className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] ${cls}`}>
      {t(statusKeys[status] ?? "maintenance.status.unknown")}
    </span>
  )
}

const statusKeys: Record<string, Parameters<Translate>[0]> = {
  pass: "maintenance.status.pass",
  warn: "maintenance.status.warn",
  fail: "maintenance.status.fail",
  unknown: "maintenance.status.unknown",
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

import { useEffect, useState } from "react"
import { Download, FileInput } from "lucide-react"

import type {
  Backend,
  BackendError,
  ImportItem,
  ImportOutcome,
  ImportPrompt,
  ItemEvent,
} from "@/backend"
import { Button } from "@/components/ui/button"
import { Segmented } from "@/card"
import { Sheet, SheetButtons } from "@/sheet"
import { useI18n, type Translate } from "@/i18n"

/** How photos are republished; "" asks through the import.prompt event. */
type PhotosAs = "" | "document" | "photo"

type Phase =
  | { state: "idle" }
  | { state: "previewing" }
  | { state: "planned"; plan: ImportOutcome }
  | { state: "confirming" }
  | { state: "running"; items: ItemEvent[] }
  | { state: "done"; outcome: ImportOutcome }
  | { state: "failed"; error: BackendError }

export function ImportScreen({ backend }: { backend: Backend }) {
  const { t } = useI18n()
  const [into, setInto] = useState("/saved")
  const [photosAs, setPhotosAs] = useState<PhotosAs>("")
  const [mergeCaptions, setMergeCaptions] = useState(false)
  const [deleteSource, setDeleteSource] = useState(false)
  const [phase, setPhase] = useState<Phase>({ state: "idle" })
  const [prompt, setPrompt] = useState<ImportPrompt | null>(null)

  // The facade asks for the photo presentation mid-plan; the sheet below
  // answers it and the call resumes.
  useEffect(() => backend.import.onPrompt(setPrompt), [backend])

  const busy = phase.state === "previewing" || phase.state === "running"

  const options = () => ({
    into: into.trim(),
    photos_as: photosAs,
    merge_captions: mergeCaptions,
  })

  const preview = async () => {
    setPhase({ state: "previewing" })
    try {
      // Delete-source only executes; sending it on a preview would trip the
      // confirmation gate the real run is for.
      const plan = await backend.import.preview({ ...options(), delete_source: false })
      setPhase({ state: "planned", plan })
    } catch (error) {
      setPhase({ state: "failed", error: error as BackendError })
    }
  }

  const run = async () => {
    setPhase({ state: "running", items: [] })
    // Subscribe for the run's duration: every imported item arrives here.
    const items: ItemEvent[] = []
    const off = backend.import.onItem((e) => {
      items.push(e)
      setPhase((p) => (p.state === "running" ? { state: "running", items: [...items] } : p))
    })
    try {
      const outcome = await backend.import.run({ ...options(), delete_source: deleteSource, confirm: true })
      setPhase({ state: "done", outcome })
    } catch (error) {
      setPhase({ state: "failed", error: error as BackendError })
    } finally {
      off()
    }
  }

  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-3.5">
      <section aria-label={t("import.heading")}>
        <h2 className="px-1 pb-1.5 text-[11.5px] font-medium tracking-[.04em] text-muted-foreground uppercase">
          {t("import.heading")}
        </h2>
        <div className="flex flex-col gap-3 rounded-card border border-line bg-card px-3.5 py-3">
          <p className="text-[12.5px] text-muted-foreground">{t("import.intro")}</p>
          <label className="flex items-center gap-3 text-[12.5px]">
            <span className="w-28 shrink-0 text-fg-2">{t("import.destination")}</span>
            <input
              aria-label={t("import.destination")}
              value={into}
              onChange={(e) => setInto(e.target.value)}
              spellCheck={false}
              className="min-w-0 flex-1 rounded-control border border-line bg-card-2 px-2 py-1 font-mono text-[12px] outline-none focus:border-primary"
            />
          </label>
          <div className="flex items-center gap-3 text-[12.5px]">
            <span className="w-28 shrink-0 text-fg-2">{t("import.photosAs")}</span>
            <Segmented<PhotosAs>
              label={t("import.photosAs")}
              value={photosAs}
              onChange={setPhotosAs}
              options={[
                { value: "", name: t("import.photosAsk") },
                { value: "document", name: t("import.photosDocument") },
                { value: "photo", name: t("import.photosPhoto") },
              ]}
            />
          </div>
          <Checkbox label={t("import.mergeCaptions")} checked={mergeCaptions} onChange={setMergeCaptions} />
          <Checkbox label={t("import.deleteSource")} checked={deleteSource} onChange={setDeleteSource} />
          <div className="flex items-center gap-1.5 pt-1">
            <Button variant="outline" size="sm" onClick={preview} disabled={busy}>
              <FileInput data-icon="inline-start" />
              {phase.state === "previewing" ? t("import.previewing") : t("import.preview")}
            </Button>
            <Button size="sm" onClick={() => setPhase({ state: "confirming" })} disabled={busy}>
              <Download data-icon="inline-start" />
              {t("import.run")}
            </Button>
          </div>
        </div>
      </section>

      <ImportStatus phase={phase} />

      {phase.state === "confirming" && (
        <Sheet title={t("import.confirmTitle")} onClose={() => setPhase({ state: "idle" })}>
          <p className="text-[12.5px] text-fg-2">{t("import.confirmBody", { into: into.trim() || "/saved" })}</p>
          {deleteSource && (
            <p className="mt-2 rounded-control bg-amber-soft px-2 py-1.5 text-[12px] text-amber">
              {t("import.confirmDeleteSource")}
            </p>
          )}
          <SheetButtons
            confirmLabel={t("import.confirm")}
            destructive={deleteSource}
            busy={false}
            onConfirm={() => void run()}
            onClose={() => setPhase({ state: "idle" })}
          />
        </Sheet>
      )}

      {prompt && (
        <Sheet
          title={t("import.promptTitle")}
          onClose={() => {
            const id = prompt.id
            setPrompt(null)
            void backend.import.cancelPrompt(id)
          }}
        >
          <p className="text-[12.5px] text-fg-2">{t("import.promptBody", { photos: prompt.photos })}</p>
          <div className="mt-4 flex justify-end gap-1.5">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                const id = prompt.id
                setPrompt(null)
                void backend.import.cancelPrompt(id)
              }}
            >
              {t("sheet.cancel")}
            </Button>
            {(["document", "photo"] as const).map((choice) => (
              <Button
                key={choice}
                size="sm"
                onClick={() => {
                  const id = prompt.id
                  setPrompt(null)
                  void backend.import.answerPrompt(id, choice)
                }}
              >
                {choice === "document" ? t("import.photosDocument") : t("import.photosPhoto")}
              </Button>
            ))}
          </div>
        </Sheet>
      )}
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

/** The plan or progress below the form: summary line plus per-item rows. */
function ImportStatus({ phase }: { phase: Phase }) {
  const { t } = useI18n()
  if (phase.state === "idle") return null
  if (phase.state === "failed") {
    return (
      <div role="alert" className="rounded-card border border-line bg-red-soft px-3.5 py-3 text-red">
        <p>{phase.error.message}</p>
        <p className="mt-1 font-mono text-[11.5px] opacity-80">{phase.error.code}</p>
      </div>
    )
  }
  if (phase.state === "previewing" || phase.state === "confirming") {
    return <p role="status" className="px-1 text-[12px] text-muted-foreground">{t("import.previewing")}</p>
  }
  if (phase.state === "running") {
    const last = phase.items[phase.items.length - 1]
    return (
      <section aria-label={t("import.itemsLabel")} className="flex flex-col gap-2">
        <p role="status" className="px-1 text-[12px] text-muted-foreground">
          {t("import.running", {
            imported: last?.completed ?? 0,
            skipped: last?.skipped ?? 0,
            failed: last?.failed ?? 0,
          })}
        </p>
        <ul aria-label={t("import.itemsLabel")} className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card">
          {phase.items.map((item, i) => (
            <ItemEventRow key={i} item={item} />
          ))}
        </ul>
      </section>
    )
  }
  const outcome = phase.state === "planned" ? phase.plan : phase.outcome
  const items = outcome.items ?? []
  return (
    <section aria-label={t("import.itemsLabel")} className="flex flex-col gap-2">
      <p role="status" className="px-1 text-[12px] text-muted-foreground">
        {outcome.dry_run
          ? t("import.planSummary", {
              imported: outcome.imported,
              skipped: outcome.skipped,
              duplicates: outcome.duplicates,
            })
          : t("import.summary", {
              imported: outcome.imported,
              skipped: outcome.skipped,
              duplicates: outcome.duplicates,
              failed: outcome.failed,
            })}
        {!outcome.dry_run && outcome.sources_deleted > 0 && ` ${t("import.sourcesDeleted", { count: outcome.sources_deleted })}`}
        {!outcome.history_complete && ` ${t("import.partialHistory")}`}
      </p>
      {items.length === 0 ? (
        <p className="px-1 py-3 text-center text-[12.5px] text-muted-foreground">{t("import.emptyPlan")}</p>
      ) : (
        <ul aria-label={t("import.itemsLabel")} className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card">
          {items.map((item) => (
            <PlanItemRow key={item.message_id} item={item} planned={outcome.dry_run} />
          ))}
        </ul>
      )}
    </section>
  )
}

function actionBadgeClass(action: string): string {
  switch (action) {
    case "import":
    case "completed":
      return "bg-green-soft text-green"
    case "skip":
    case "skipped":
      return "bg-pill text-muted-foreground"
    default:
      return "bg-red-soft text-red"
  }
}

function actionLabel(action: string, t: Translate, planned: boolean): string {
  switch (action) {
    case "import":
    case "completed":
      return planned ? t("import.action.import.planned") : t("import.action.imported")
    case "skip":
    case "skipped":
      return planned ? t("import.action.skip.planned") : t("import.action.skipped")
    default:
      return t("import.action.failed")
  }
}

function PlanItemRow({ item, planned }: { item: ImportItem; planned: boolean }) {
  const { t } = useI18n()
  return (
    <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5 text-[12.5px]">
      <span className="w-14 shrink-0 font-mono text-[11.5px] text-muted-foreground">#{item.message_id}</span>
      <span className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] ${actionBadgeClass(item.action)}`}>
        {actionLabel(item.action, t, planned)}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-[12px]">
        {item.path || item.reason || item.error}
        {item.duplicate_of && (
          <span className="text-muted-foreground"> {t("import.duplicateOf", { path: item.duplicate_of })}</span>
        )}
      </span>
      <span className="shrink-0 text-[11.5px] text-muted-foreground">{item.kind}</span>
    </li>
  )
}

function ItemEventRow({ item }: { item: ItemEvent }) {
  const { t } = useI18n()
  return (
    <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5 text-[12.5px]">
      <span className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] ${actionBadgeClass(item.status)}`}>
        {actionLabel(item.status, t, false)}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{item.path || item.error}</span>
    </li>
  )
}

import { useEffect, useState } from "react"
import { File, Folder } from "lucide-react"

import type { Backend, BackendError, UploadPlan } from "@/backend"
import { Button } from "@/components/ui/button"
import { formatSize } from "@/format"
import { useI18n, type Translate } from "@/i18n"
import { Sheet, SheetError } from "@/sheet"

/**
 * The upload and download option sheets: every GUI transfer starts through
 * one. The upload sheet shows the facade's dry-run plan (td cp --dry-run's
 * data plus file sizes against the account's upload limit) before the
 * start button works; its options map one-to-one onto the service's upload
 * options.
 */

type PlanState =
  | { state: "loading" }
  | { state: "ready"; plan: UploadPlan }
  | { state: "failed"; error: BackendError }

function baseName(path: string): string {
  return path.slice(path.replace(/\/+$/, "").lastIndexOf("/") + 1)
}

export function UploadSheet({
  backend,
  paths,
  dest,
  onStarted,
  onClose,
}: {
  backend: Backend
  /** The picked or dropped local paths. */
  paths: string[]
  /** The remote directory the Drive tab shows. */
  dest: string
  onStarted: () => void
  onClose: () => void
}) {
  const { t } = useI18n()
  // The conflict policy: "" is fail, the CLI default without flags.
  const [policy, setPolicy] = useState<"" | "skip" | "replace">("")
  const [confirmReplace, setConfirmReplace] = useState(false)
  const [kind, setKind] = useState("")
  const [caption, setCaption] = useState("")
  const [noHash, setNoHash] = useState(false)
  const [continueOnError, setContinueOnError] = useState(false)
  const [includeEmptyDirs, setIncludeEmptyDirs] = useState(false)
  const [plan, setPlan] = useState<PlanState>({ state: "loading" })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<BackendError | null>(null)

  // The dry-run plan: re-run when the policy changes, because the plan's
  // would_replace depends on it. The previous plan stays on screen while
  // the new one loads, so the controls never vanish mid-edit.
  useEffect(() => {
    let live = true
    backend.transfers.planUpload(paths, dest, policy).then(
      (p) => live && setPlan({ state: "ready", plan: p }),
      (error: BackendError) => live && setPlan({ state: "failed", error }),
    )
    return () => {
      live = false
    }
  }, [backend, paths, dest, policy])

  const ready = plan.state === "ready" ? plan.plan : null
  // The Go slice marshals as null when empty.
  const files = ready?.files ?? []
  const hasDirs = files.some((f) => f.dir)
  // Several files together are one album, and albums cannot replace.
  const album = files.filter((f) => !f.dir).length > 1
  const startDisabled = busy || !ready || (policy === "replace" && !confirmReplace)

  const start = async () => {
    if (startDisabled) return
    setBusy(true)
    setError(null)
    try {
      await backend.transfers.upload(paths, dest, {
        policy,
        confirm_replace: confirmReplace,
        kind,
        caption,
        no_hash: noHash,
        continue_on_error: continueOnError,
        include_empty_dirs: includeEmptyDirs,
      })
      onStarted()
    } catch (err) {
      setError(err as BackendError)
      setBusy(false)
    }
  }

  return (
    <Sheet title={t("upload.title", { path: dest })} onClose={onClose}>
      {plan.state === "loading" && <p className="text-[12.5px] text-muted-foreground">{t("drive.loading")}</p>}
      {plan.state === "failed" && <SheetError error={plan.error} />}
      {ready && (
        <>
          <PlanSummary plan={ready} t={t} />
          {!hasDirs && (
            <>
              <RadioGroup
                label={t("upload.presentation")}
                name="kind"
                value={kind}
                onChange={setKind}
                options={[
                  { value: "", label: t("upload.kind.document") },
                  { value: "photo", label: t("upload.kind.photo") },
                  { value: "video", label: t("upload.kind.video") },
                ]}
              />
              <label className="mt-3 block text-[12px] text-muted-foreground">
                {t("upload.caption")}
                <input
                  value={caption}
                  onChange={(e) => setCaption(e.target.value)}
                  className="mt-1 h-8 w-full rounded-control border border-line bg-background px-2 text-[13px] text-fg outline-none focus:border-ring"
                />
              </label>
            </>
          )}
          <RadioGroup
            label={t("upload.conflict")}
            name="policy"
            value={policy}
            onChange={(v) => setPolicy(v as "" | "skip" | "replace")}
            options={[
              { value: "", label: t("upload.conflict.fail") },
              { value: "skip", label: t("upload.conflict.skip") },
              { value: "replace", label: t("upload.conflict.replace"), disabled: album },
            ]}
          />
          {album && <p className="mt-1 text-[11.5px] text-muted-foreground">{t("upload.replaceAlbumNote")}</p>}
          {policy === "replace" && (
            <Check label={t("upload.confirmReplace")} checked={confirmReplace} onChange={setConfirmReplace} />
          )}
          {hasDirs && (
            <>
              <Check label={t("upload.continueOnError")} checked={continueOnError} onChange={setContinueOnError} />
              <Check label={t("upload.includeEmptyDirs")} checked={includeEmptyDirs} onChange={setIncludeEmptyDirs} />
            </>
          )}
          <Check label={t("upload.noHash")} checked={noHash} onChange={setNoHash} />
        </>
      )}
      <SheetError error={error} />
      <div className="mt-4 flex justify-end gap-1.5">
        <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
          {t("sheet.cancel")}
        </Button>
        <Button size="sm" onClick={start} disabled={startDisabled}>
          {t("upload.start")}
        </Button>
      </div>
    </Sheet>
  )
}

/** The dry-run plan: the files with sizes, the upload limit, the replace warning. */
function PlanSummary({ plan, t }: { plan: UploadPlan; t: Translate }) {
  const limit = formatSize(plan.upload_limit_bytes, t)
  // The Go slice marshals as null when empty.
  const files = plan.files ?? []
  return (
    <section aria-label={t("upload.planLabel")} className="mb-3">
      <ul
        aria-label={t("upload.filesLabel")}
        className="mb-1.5 max-h-36 divide-y divide-line-2 overflow-auto rounded-card border border-line bg-card"
      >
        {files.map((f) => (
          <li key={f.local} className="flex min-h-8 items-center gap-2 px-2.5 py-1 text-[12.5px]">
            {f.dir ? (
              <Folder aria-hidden className="size-[15px] shrink-0 text-primary" />
            ) : (
              <File aria-hidden className="size-[15px] shrink-0 text-muted-foreground" />
            )}
            <span className="min-w-0 flex-1 truncate font-medium">{baseName(f.local)}</span>
            {f.dir ? (
              <span className="shrink-0 text-[11.5px] text-muted-foreground">{t("drive.folder")}</span>
            ) : (
              <span className="shrink-0 text-[11.5px] text-muted-foreground tabular-nums">
                {formatSize(f.size, t)}
              </span>
            )}
          </li>
        ))}
      </ul>
      {files
        .filter((f) => f.over_limit)
        .map((f) => (
          <p key={f.local} role="alert" className="mt-1 text-[11.5px] text-red">
            {t("upload.overLimit", { name: baseName(f.local), size: limit })}
          </p>
        ))}
      <p className="mt-1 text-[11.5px] text-muted-foreground">{t("upload.limit", { size: limit })}</p>
      {plan.would_replace && (
        <p className="mt-1 text-[11.5px] text-amber">{t("upload.wouldReplace", { path: plan.would_replace })}</p>
      )}
    </section>
  )
}

export function DownloadSheet({
  backend,
  remotePath,
  destDir,
  onStarted,
  onClose,
}: {
  backend: Backend
  /** The remote file or folder being downloaded. */
  remotePath: string
  /** The local directory the download lands in. */
  destDir: string
  onStarted: () => void
  onClose: () => void
}) {
  const { t } = useI18n()
  const [policy, setPolicy] = useState<"" | "skip" | "replace">("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<BackendError | null>(null)

  const start = async () => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await backend.transfers.download(remotePath, destDir, { policy })
      onStarted()
    } catch (err) {
      setError(err as BackendError)
      setBusy(false)
    }
  }

  return (
    <Sheet title={t("download.title")} onClose={onClose}>
      <p className="mb-1 truncate text-[12.5px] font-medium">{remotePath}</p>
      <p className="mb-3 truncate text-[11.5px] text-muted-foreground">
        {t("download.destination")}: <code className="font-mono">{destDir}</code>
      </p>
      <RadioGroup
        label={t("download.conflict")}
        name="policy"
        value={policy}
        onChange={(v) => setPolicy(v as "" | "skip" | "replace")}
        options={[
          { value: "", label: t("upload.conflict.fail") },
          { value: "skip", label: t("upload.conflict.skip") },
          { value: "replace", label: t("upload.conflict.replace") },
        ]}
      />
      <SheetError error={error} />
      <div className="mt-4 flex justify-end gap-1.5">
        <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
          {t("sheet.cancel")}
        </Button>
        <Button size="sm" onClick={start} disabled={busy}>
          {t("download.start")}
        </Button>
      </div>
    </Sheet>
  )
}

function RadioGroup({
  label,
  name,
  value,
  options,
  onChange,
}: {
  label: string
  name: string
  value: string
  options: readonly { value: string; label: string; disabled?: boolean }[]
  onChange: (value: string) => void
}) {
  return (
    <div role="radiogroup" aria-label={label} className="mt-3">
      <p className="mb-1 text-[12px] text-muted-foreground">{label}</p>
      <div className="flex flex-col gap-1">
        {options.map((o) => (
          <label
            key={o.value || "default"}
            className={`flex items-center gap-2 text-[12.5px] ${o.disabled ? "text-faint" : "text-fg-2"}`}
          >
            <input
              type="radio"
              name={name}
              value={o.value}
              checked={value === o.value}
              disabled={o.disabled}
              onChange={() => onChange(o.value)}
              className="accent-primary"
            />
            {o.label}
          </label>
        ))}
      </div>
    </div>
  )
}

function Check({
  label,
  checked,
  onChange,
}: {
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="mt-2 flex items-center gap-2 text-[12.5px] text-fg-2">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="accent-primary"
      />
      {label}
    </label>
  )
}

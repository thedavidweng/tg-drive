import { useCallback, useEffect, useRef, useState } from "react"
import { Check, ChevronDown, HardDrive, Plus } from "lucide-react"

import type { Backend, BackendError, BindChoices, BindRequest, ChannelInfo, ChannelStatus } from "@/backend"
import { Button } from "@/components/ui/button"
import { formatDate, formatSize } from "@/format"
import { Sheet, SheetError } from "@/sheet"
import { useI18n, type Translate } from "@/i18n"

/**
 * The localStorage key of the last selected drive channel, a GUI-side
 * preference like the theme override (src/theme.ts), not a td config key.
 * The facade keeps the selection for the process; this restores it across
 * restarts.
 */
export const channelStorageKey = "td-channel"

export function storedChannel(): string {
  return localStorage.getItem(channelStorageKey) ?? ""
}

export function storeChannel(id: string) {
  if (id === "") {
    localStorage.removeItem(channelStorageKey)
  } else {
    localStorage.setItem(channelStorageKey, id)
  }
}

/**
 * The header's drive switcher: shows the active channel, opens the sheet
 * that switches, binds, or creates drives, and reports switches up so the
 * Drive view remounts on the new channel. The initial binding is not
 * reported: the facade already opens on it, so the Drive view's first
 * render is correct without a remount.
 */
export function ChannelSwitcher({
  backend,
  onActiveChange,
}: {
  backend: Backend
  onActiveChange: (channelID: string) => void
}) {
  const { t } = useI18n()
  const [channels, setChannels] = useState<ChannelInfo[] | null>(null)
  const [sheet, setSheet] = useState<"closed" | "channels" | "bind">("closed")
  const [selectError, setSelectError] = useState<BackendError | null>(null)
  const restored = useRef(false)

  const reload = useCallback(() => {
    backend.channels.list().then(
      (list) => setChannels(list),
      () => setChannels([]),
    )
  }, [backend])
  useEffect(reload, [reload])
  // Bindings made elsewhere (td init in a terminal) arrive as
  // channels-changed from index sync.
  useEffect(() => backend.events.onChannelsChanged((e) => setChannels(e.channels ?? [])), [backend])

  const active = channels?.find((c) => c.active)

  const select = useCallback(
    async (channelID: string) => {
      setSelectError(null)
      try {
        await backend.channels.select(channelID)
      } catch (err) {
        setSelectError(err as BackendError)
        return
      }
      storeChannel(channelID)
      onActiveChange(channelID)
      reload()
      setSheet("closed")
    },
    [backend, onActiveChange, reload],
  )

  // Once the first list arrives, restore the stored selection when it
  // names another bound channel; the restore reports the switch up the
  // same way an explicit selection does.
  useEffect(() => {
    if (!channels || restored.current) return
    restored.current = true
    const stored = storedChannel()
    const activeID = channels.find((c) => c.active)?.channel_id
    if (!stored || stored === activeID || !channels.some((c) => c.channel_id === stored)) return
    let live = true
    backend.channels.select(stored).then(
      () => {
        if (!live) return
        storeChannel(stored)
        onActiveChange(stored)
        reload()
      },
      (err: BackendError) => live && setSelectError(err),
    )
    return () => {
      live = false
    }
  }, [channels, backend, onActiveChange, reload])

  return (
    <>
      <button
        type="button"
        aria-label={t("channels.switcher")}
        title={t("channels.switcher")}
        onClick={() => {
          setSelectError(null)
          setSheet("channels")
        }}
        className="flex h-7 max-w-44 items-center gap-1 rounded-control px-2 text-[12.5px] text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg"
      >
        <HardDrive aria-hidden className="size-3.5 shrink-0" />
        <span className="truncate">{active ? active.title : t("channels.none")}</span>
        <ChevronDown aria-hidden className="size-3 shrink-0 text-faint" />
      </button>
      {sheet === "channels" && (
        <ChannelsSheet
          backend={backend}
          channels={channels ?? []}
          selectError={selectError}
          onSelect={select}
          onBindOpen={() => setSheet("bind")}
          onClose={() => setSheet("closed")}
        />
      )}
      {sheet === "bind" && (
        <BindSheet
          backend={backend}
          onBound={(channelID) => {
            storeChannel(channelID)
            onActiveChange(channelID)
            reload()
            setSheet("closed")
          }}
          onClose={() => setSheet("channels")}
        />
      )}
    </>
  )
}

function ChannelsSheet({
  backend,
  channels,
  selectError,
  onSelect,
  onBindOpen,
  onClose,
}: {
  backend: Backend
  channels: ChannelInfo[]
  selectError: BackendError | null
  onSelect: (channelID: string) => void
  onBindOpen: () => void
  onClose: () => void
}) {
  const { t, locale } = useI18n()
  const active = channels.find((c) => c.active)
  // The fetch result carries the channel it was read for, so a stale read
  // of a previously active channel never shows (the way DriveScreen
  // carries the listed path).
  const [statusResult, setStatusResult] = useState<{
    channelID: string
    status: ChannelStatus | null
    error: BackendError | null
  } | null>(null)
  const [linking, setLinking] = useState(false)
  const activeID = active?.channel_id

  useEffect(() => {
    if (!activeID) return
    let live = true
    backend.channels.status().then(
      (s) => live && setStatusResult({ channelID: activeID, status: s, error: null }),
      (err: BackendError) => live && setStatusResult({ channelID: activeID, status: null, error: err }),
    )
    return () => {
      live = false
    }
  }, [backend, activeID])

  const current = statusResult && statusResult.channelID === activeID ? statusResult : null
  const status = current?.status ?? null
  const statusError = current?.error ?? null

  const linkDiscussion = async () => {
    if (linking || !activeID) return
    setLinking(true)
    try {
      await backend.channels.linkDiscussion()
      const s = await backend.channels.status()
      setStatusResult({ channelID: activeID, status: s, error: null })
    } catch (err) {
      setStatusResult((prev) =>
        prev && prev.channelID === activeID ? { ...prev, error: err as BackendError } : prev,
      )
    } finally {
      setLinking(false)
    }
  }

  return (
    <Sheet title={t("channels.sheetTitle")} onClose={onClose}>
      {channels.length === 0 ? (
        <p className="py-2 text-center text-[12.5px] text-muted-foreground">{t("channels.empty")}</p>
      ) : (
        <ul aria-label={t("channels.sheetTitle")} className="divide-y divide-line-2">
          {channels.map((ch) => (
            <li key={ch.channel_id} className="flex min-h-9 items-center gap-2 py-1">
              {ch.active ? (
                <>
                  <Check aria-hidden className="size-4 shrink-0 text-primary" />
                  <span aria-current="true" className="min-w-0 flex-1 truncate text-[13px] font-medium">
                    {ch.title}
                  </span>
                  <span className="shrink-0 rounded-full bg-pill px-2 py-0.5 text-[11px] text-fg-2">
                    {t("channels.active")}
                  </span>
                </>
              ) : (
                <button
                  type="button"
                  aria-label={t("channels.switchTo", { title: ch.title })}
                  onClick={() => onSelect(ch.channel_id)}
                  className="min-w-0 flex-1 truncate rounded-control px-1 py-0.5 text-left text-[13px] text-fg-2 transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg"
                >
                  {ch.title}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      <SheetError error={selectError} />

      {active && (
        <div className="mt-3 border-t border-line-2 pt-3">
          <h3 className="mb-2 text-[11.5px] font-medium tracking-wide text-muted-foreground uppercase">
            {t("channels.status")}
          </h3>
          {status ? (
            <dl className="flex flex-col gap-1.5 text-[12.5px]">
              <StatusRow
                label={t("channels.status.discussion")}
                value={
                  status.discussion_linked ? (
                    status.discussion_title || ""
                  ) : (
                    <span className="flex items-center justify-end gap-2">
                      <span className="text-muted-foreground">{t("channels.status.none")}</span>
                      <Button variant="outline" size="sm" onClick={linkDiscussion} disabled={linking}>
                        {t("channels.status.link")}
                      </Button>
                    </span>
                  )
                }
              />
              <StatusRow label={t("channels.status.permissions")} value={permissionsText(status, t)} />
              <StatusRow label={t("channels.status.uploadLimit")} value={formatSize(status.upload_limit_bytes, t)} />
              <StatusRow
                label={t("channels.status.lastScan")}
                value={status.last_scan_at ? formatDate(status.last_scan_at, locale) : t("channels.status.never")}
              />
              <StatusRow
                label={t("channels.status.filesLabel")}
                value={t("channels.status.files", { count: status.files })}
              />
            </dl>
          ) : (
            !statusError && <p className="text-[12px] text-muted-foreground">{t("drive.loading")}</p>
          )}
          <SheetError error={statusError} />
        </div>
      )}

      <div className="mt-4 flex items-center justify-between gap-1.5">
        <Button variant="outline" size="sm" onClick={onBindOpen}>
          <Plus data-icon="inline-start" />
          {t("channels.bind.title")}
        </Button>
        <Button variant="ghost" size="sm" onClick={onClose}>
          {t("sheet.close")}
        </Button>
      </div>
    </Sheet>
  )
}

/** The account's permissions on the channel: all granted, or the missing ones named. */
function permissionsText(status: ChannelStatus, t: Translate): string {
  const caps = status.capabilities
  if (!caps) return t("channels.status.permissionsUnknown")
  const missing = [
    !caps?.can_upload && t("channels.permission.upload"),
    !caps?.can_delete && t("channels.permission.delete"),
    !caps?.can_edit_captions && t("channels.permission.edit"),
    !caps?.can_invite && t("channels.permission.invite"),
  ].filter((m): m is string => !!m)
  if (missing.length === 0) return t("channels.status.allGranted")
  return t("channels.status.missing", { list: missing.join(t("channels.permission.separator")) })
}

function StatusRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 truncate text-right text-fg">{value}</dd>
    </div>
  )
}

function BindSheet({
  backend,
  onBound,
  onClose,
}: {
  backend: Backend
  onBound: (channelID: string) => void
  onClose: () => void
}) {
  const { t } = useI18n()
  const [choices, setChoices] = useState<BindChoices | null>(null)
  const [error, setError] = useState<BackendError | null>(null)
  const [title, setTitle] = useState("")
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let live = true
    backend.channels.choices().then(
      (c) => {
        if (!live) return
        setChoices(c)
        setTitle(c.default_title)
      },
      (err: BackendError) => live && setError(err),
    )
    return () => {
      live = false
    }
  }, [backend])

  const bind = async (req: BindRequest) => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const res = await backend.channels.bind(req)
      onBound(res.channel_id)
    } catch (err) {
      setError(err as BackendError)
      setBusy(false)
    }
  }

  return (
    <Sheet title={t("channels.bind.title")} onClose={onClose}>
      <h3 className="mb-2 text-[11.5px] font-medium tracking-wide text-muted-foreground uppercase">
        {t("channels.bind.existing")}
      </h3>
      {choices === null ? (
        <p className="text-[12px] text-muted-foreground">{t("drive.loading")}</p>
      ) : (choices.channels ?? []).length === 0 ? (
        <p className="text-[12.5px] text-muted-foreground">{t("channels.bind.empty")}</p>
      ) : (
        <ul aria-label={t("channels.bind.existing")} className="mb-1 divide-y divide-line-2">
          {(choices.channels ?? []).map((ch) => (
            <li key={ch.channel_id} className="flex min-h-9 items-center gap-2 py-1">
              {ch.bound ? (
                <>
                  <span className="min-w-0 flex-1 truncate px-1 text-[13px] text-muted-foreground">{ch.title}</span>
                  <span className="shrink-0 rounded-full bg-pill px-2 py-0.5 text-[11px] text-fg-2">
                    {t("channels.bind.bound")}
                  </span>
                </>
              ) : (
                <button
                  type="button"
                  aria-label={t("channels.bind.bind", { title: ch.title })}
                  disabled={busy}
                  onClick={() => void bind({ channel_id: ch.channel_id, title: "" })}
                  className="min-w-0 flex-1 truncate rounded-control px-1 py-0.5 text-left text-[13px] text-fg-2 transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg disabled:opacity-50"
                >
                  {ch.title}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      <div className="mt-3 border-t border-line-2 pt-3">
        <h3 className="mb-2 text-[11.5px] font-medium tracking-wide text-muted-foreground uppercase">
          {t("channels.bind.create")}
        </h3>
        <label className="block text-[12px] text-muted-foreground">
          {t("channels.bind.name")}
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            className="mt-1 h-8 w-full rounded-control border border-line bg-background px-2 text-[13px] text-fg outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
          />
        </label>
      </div>

      <SheetError error={error} />
      <div className="mt-4 flex items-center justify-end gap-1.5">
        <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
          {t("sheet.cancel")}
        </Button>
        <Button
          variant="default"
          size="sm"
          disabled={busy || title.trim() === ""}
          onClick={() => void bind({ channel_id: "", title: title.trim() })}
        >
          {t("channels.bind.createSubmit")}
        </Button>
      </div>
    </Sheet>
  )
}

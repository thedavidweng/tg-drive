import { useEffect, useState } from "react"
import {
  ChevronRight,
  Download,
  File,
  Folder,
  FolderPlus,
  FolderUp,
  List,
  ListTree,
  Pencil,
  RefreshCw,
  Share2,
  Trash2,
  Upload,
} from "lucide-react"

import type { Backend, BackendError, Entry, ScanOutcome, ShareLink, TreeNode } from "@/backend"
import { Button } from "@/components/ui/button"
import { formatDate, formatSize } from "@/format"
import { useI18n, type Translate } from "@/i18n"
import { Sheet, SheetButtons, SheetError } from "@/sheet"
import { DownloadSheet, UploadSheet } from "@/transfer-options"

type Listing = { state: "loading" } | { state: "ready"; entries: Entry[] } | { state: "failed"; error: BackendError }

// A fetch result is tagged with the path it answered, so the loading state
// after a navigation is derived (result.path !== path) instead of set
// synchronously from an effect.
type FetchResult<T> = { path: string } & (
  | { state: "ready"; value: T }
  | { state: "failed"; error: BackendError }
)

type SheetState =
  | { kind: "newFolder" }
  | { kind: "move"; entry: Entry }
  | { kind: "delete"; entry: Entry }
  | { kind: "share"; entry: Entry }
  | { kind: "upload"; paths: string[] }
  | { kind: "download"; entry: Entry; destDir: string }
  | null

type ScanState =
  | { state: "idle" }
  | { state: "running"; stage: string; indexed: number; failed: number }
  | { state: "done"; outcome: ScanOutcome }
  | { state: "failed"; error: BackendError }

// A transfer submission's one-line feedback; the Transfers tab carries the
// live progress.
type TransferNote =
  | { state: "idle" }
  | { state: "started"; kind: "upload" | "download" }
  | { state: "failed"; error: BackendError }

export function DriveScreen({ backend }: { backend: Backend }) {
  const { t } = useI18n()
  const [path, setPath] = useState("/")
  const [result, setResult] = useState<FetchResult<Entry[]> | null>(null)
  const [view, setView] = useState<"list" | "tree">("list")
  const [reloadNonce, setReloadNonce] = useState(0)
  const [sheet, setSheet] = useState<SheetState>(null)
  const [scan, setScan] = useState<ScanState>({ state: "idle" })
  const [transferNote, setTransferNote] = useState<TransferNote>({ state: "idle" })

  const reload = () => setReloadNonce((n) => n + 1)

  useEffect(() => {
    let live = true
    backend.drive.list(path).then(
      (entries) => live && setResult({ path, state: "ready", value: entries ?? [] }),
      (error: BackendError) => live && setResult({ path, state: "failed", error }),
    )
    return () => {
      live = false
    }
  }, [backend, path, reloadNonce])

  // Index sync: another process changed the index, and Go re-read the
  // directory being shown.
  useEffect(() => {
    return backend.events.onDirectoryChanged((e) => {
      if (e.path === path) {
        setResult({ path: e.path, state: "ready", value: e.entries ?? [] })
      }
    })
  }, [backend, path])

  const listing: Listing =
    !result || result.path !== path
      ? { state: "loading" }
      : result.state === "failed"
        ? { state: "failed", error: result.error }
        : { state: "ready", entries: result.value }

  useEffect(() => {
    return backend.events.onScanProgress((e) => {
      setScan((prev) =>
        prev.state === "running"
          ? { state: "running", stage: e.stage, indexed: e.indexed, failed: e.failed }
          : prev,
      )
    })
  }, [backend])

  const startScan = async () => {
    setScan({ state: "running", stage: "reading", indexed: 0, failed: 0 })
    try {
      const outcome = await backend.drive.scan()
      setScan({ state: "done", outcome })
      reload()
    } catch (error) {
      setScan({ state: "failed", error: error as BackendError })
    }
  }

  // Every upload — picker-chosen or dropped — lands in the directory being
  // shown, and starts through the options sheet, which previews the
  // dry-run plan before anything runs. The Transfer it starts is the one
  // the Transfers tab watches.
  const uploadPaths = (paths: string[]) => {
    const clean = paths.filter((p) => p !== "")
    if (clean.length === 0) return
    setSheet({ kind: "upload", paths: clean })
  }

  // The picker itself can fail (a server-mode build without a connected
  // dialog answers ERR_USAGE); that failure wears the same note as a
  // submission's.
  const pickAndUpload = async (pick: () => Promise<string[]>) => {
    try {
      uploadPaths(await pick())
    } catch (error) {
      setTransferNote({ state: "failed", error: error as BackendError })
    }
  }

  const downloadEntry = async (entry: Entry) => {
    try {
      const dir = await backend.transfers.pickDirectory()
      if (!dir) return
      setSheet({ kind: "download", entry, destDir: dir })
    } catch (error) {
      setTransferNote({ state: "failed", error: error as BackendError })
    }
  }

  // Files dropped from the OS arrive as the files-dropped event; the drop
  // target is the whole Drive screen (data-file-drop-target below).
  useEffect(() => {
    return backend.events.onFilesDropped((e) => {
      const paths = (e.paths ?? []).filter((p) => p !== "")
      if (paths.length === 0) return
      setSheet({ kind: "upload", paths })
    })
  }, [backend])

  return (
    <div data-file-drop-target="">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <Breadcrumbs path={path} onNavigate={setPath} />
        <div className="flex items-center gap-1.5">
          <Button variant="outline" size="sm" onClick={() => pickAndUpload(() => backend.transfers.pickFiles())}>
            <Upload data-icon="inline-start" />
            {t("drive.uploadFiles")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={async () => pickAndUpload(async () => [await backend.transfers.pickDirectory()])}
          >
            <FolderUp data-icon="inline-start" />
            {t("drive.uploadFolder")}
          </Button>
          <Button variant="outline" size="sm" onClick={() => setSheet({ kind: "newFolder" })}>
            <FolderPlus data-icon="inline-start" />
            {t("drive.newFolder")}
          </Button>
          <Button variant="outline" size="sm" onClick={startScan} disabled={scan.state === "running"}>
            <RefreshCw data-icon="inline-start" />
            {t("drive.scan")}
          </Button>
          <span
            role="group"
            aria-label={t("drive.viewLabel")}
            className="flex rounded-seg bg-seg-track p-0.5"
          >
            {(
              [
                { id: "list", icon: List, label: t("drive.view.list") },
                { id: "tree", icon: ListTree, label: t("drive.view.tree") },
              ] as const
            ).map(({ id, icon: Icon, label }) => (
              <button
                key={id}
                type="button"
                aria-pressed={view === id}
                onClick={() => setView(id)}
                className={`flex h-6 items-center gap-1 rounded-control px-2 text-[12px] transition-colors duration-150 ease-quiet ${
                  view === id ? "bg-seg-thumb text-fg shadow-seg" : "text-ctl-fg hover:text-fg-2"
                }`}
              >
                <Icon aria-hidden className="size-3.5" />
                {label}
              </button>
            ))}
          </span>
        </div>
      </div>

      {scan.state !== "idle" && (
        <p role="status" className="mb-2 text-[12px] text-muted-foreground">
          {scan.state === "running" &&
            (scan.stage === "indexing" ? t("scan.indexing", { indexed: scan.indexed }) : t("scan.reading"))}
          {scan.state === "done" && t("scan.done", { active: scan.outcome.active })}
          {scan.state === "failed" && t("scan.failed", { message: scan.error.message })}
        </p>
      )}

      {transferNote.state !== "idle" && (
        <p role="status" className="mb-2 text-[12px] text-muted-foreground">
          {transferNote.state === "started" &&
            (transferNote.kind === "upload" ? t("drive.uploadStarted") : t("drive.downloadStarted"))}
          {transferNote.state === "failed" && t("drive.transferFailed", { message: transferNote.error.message })}
        </p>
      )}

      {view === "list" ? (
        <ListingCard
          listing={listing}
          path={path}
          onNavigate={setPath}
          onAction={(kind, entry) => setSheet({ kind, entry } as SheetState)}
          onDownload={downloadEntry}
        />
      ) : (
        <TreeCard backend={backend} path={path} reloadNonce={reloadNonce} />
      )}

      {sheet?.kind === "newFolder" && (
        <NewFolderSheet
          backend={backend}
          path={path}
          onDone={() => {
            setSheet(null)
            reload()
          }}
          onClose={() => setSheet(null)}
        />
      )}
      {sheet?.kind === "move" && (
        <MoveSheet
          backend={backend}
          entry={sheet.entry}
          onDone={() => {
            setSheet(null)
            reload()
          }}
          onClose={() => setSheet(null)}
        />
      )}
      {sheet?.kind === "delete" && (
        <DeleteSheet
          backend={backend}
          entry={sheet.entry}
          onDone={() => {
            setSheet(null)
            reload()
          }}
          onClose={() => setSheet(null)}
        />
      )}
      {sheet?.kind === "share" && (
        <ShareSheet backend={backend} entry={sheet.entry} onClose={() => setSheet(null)} />
      )}
      {sheet?.kind === "upload" && (
        <UploadSheet
          backend={backend}
          paths={sheet.paths}
          dest={path}
          onStarted={() => {
            setSheet(null)
            setTransferNote({ state: "started", kind: "upload" })
          }}
          onClose={() => setSheet(null)}
        />
      )}
      {sheet?.kind === "download" && (
        <DownloadSheet
          backend={backend}
          remotePath={sheet.entry.path}
          destDir={sheet.destDir}
          onStarted={() => {
            setSheet(null)
            setTransferNote({ state: "started", kind: "download" })
          }}
          onClose={() => setSheet(null)}
        />
      )}
    </div>
  )
}

function Breadcrumbs({ path, onNavigate }: { path: string; onNavigate: (path: string) => void }) {
  const { t } = useI18n()
  const segments = path.split("/").filter(Boolean)
  const crumbClass =
    "rounded-control px-1 py-0.5 text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg"
  return (
    <nav aria-label={t("drive.breadcrumbs")} className="flex min-w-0 items-center gap-0.5 text-[12.5px]">
      <button type="button" onClick={() => onNavigate("/")} className={crumbClass}>
        {t("drive.root")}
      </button>
      {segments.map((segment, i) => {
        const target = "/" + segments.slice(0, i + 1).join("/")
        const last = i === segments.length - 1
        return (
          <span key={target} className="flex min-w-0 items-center gap-0.5">
            <ChevronRight aria-hidden className="size-3 shrink-0 text-faint" />
            {last ? (
              <span aria-current="page" className="truncate px-1 py-0.5 font-medium text-fg">
                {segment}
              </span>
            ) : (
              <button type="button" onClick={() => onNavigate(target)} className={crumbClass}>
                {segment}
              </button>
            )}
          </span>
        )
      })}
    </nav>
  )
}

function ListingCard({
  listing,
  path,
  onNavigate,
  onAction,
  onDownload,
}: {
  listing: Listing
  path: string
  onNavigate: (path: string) => void
  onAction: (kind: "move" | "share" | "delete", entry: Entry) => void
  onDownload: (entry: Entry) => void
}) {
  const { t, locale } = useI18n()
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
        <li key={e.path} className="flex min-h-12 items-center gap-3 py-[7px] pr-3 pl-3.5">
          {e.type === "dir" ? (
            <Folder aria-hidden className="size-[18px] shrink-0 text-primary" />
          ) : (
            <File aria-hidden className="size-[18px] shrink-0 text-muted-foreground" />
          )}
          {e.type === "dir" ? (
            <button
              type="button"
              onClick={() => onNavigate(e.path)}
              className="min-w-0 flex-1 truncate rounded-control text-left font-medium tracking-[-.005em] hover:text-primary"
            >
              {e.name}
            </button>
          ) : (
            <span className="min-w-0 flex-1 truncate font-medium tracking-[-.005em]">{e.name}</span>
          )}
          <span className="w-16 shrink-0 text-right text-[11.5px] text-muted-foreground">
            {e.type === "dir" ? t("drive.folder") : fileType(e.name, t)}
          </span>
          <span className="w-16 shrink-0 text-right text-[11.5px] text-muted-foreground tabular-nums">
            {e.type === "dir" ? "" : formatSize(e.size, t)}
          </span>
          <span className="w-32 shrink-0 text-right text-[11.5px] text-muted-foreground tabular-nums">
            {formatDate(e.date, locale)}
          </span>
          {e.type === "file" && (
            <span className="flex shrink-0 items-center gap-0.5">
              <RowAction label={t("drive.download")} onClick={() => onDownload(e)} icon={Download} />
              <RowAction label={t("drive.rename")} onClick={() => onAction("move", e)} icon={Pencil} />
              <RowAction label={t("drive.share")} onClick={() => onAction("share", e)} icon={Share2} />
              <RowAction label={t("drive.delete")} onClick={() => onAction("delete", e)} icon={Trash2} />
            </span>
          )}
          {e.type === "dir" && (
            <span className="flex shrink-0 items-center gap-0.5">
              <RowAction label={t("drive.download")} onClick={() => onDownload(e)} icon={Download} />
            </span>
          )}
        </li>
      ))}
    </ul>
  )
}

function RowAction({
  label,
  onClick,
  icon: Icon,
}: {
  label: string
  onClick: () => void
  icon: typeof Pencil
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className="grid size-[26px] place-items-center rounded-[7px] text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg"
    >
      <Icon aria-hidden className="size-4" />
    </button>
  )
}

function TreeCard({
  backend,
  path,
  reloadNonce,
}: {
  backend: Backend
  path: string
  reloadNonce: number
}) {
  const { t } = useI18n()
  const [result, setResult] = useState<FetchResult<TreeNode[]> | null>(null)

  useEffect(() => {
    let live = true
    backend.drive.tree(path, 0).then(
      (nodes) => live && setResult({ path, state: "ready", value: nodes ?? [] }),
      (error: BackendError) => live && setResult({ path, state: "failed", error }),
    )
    return () => {
      live = false
    }
  }, [backend, path, reloadNonce])

  const tree: { state: "loading" } | { state: "ready"; nodes: TreeNode[] } | { state: "failed"; error: BackendError } =
    !result || result.path !== path
      ? { state: "loading" }
      : result.state === "failed"
        ? { state: "failed", error: result.error }
        : { state: "ready", nodes: result.value }

  if (tree.state === "loading") {
    return <p className="px-1 py-6 text-center text-muted-foreground">{t("drive.loading")}</p>
  }
  if (tree.state === "failed") {
    return (
      <div role="alert" className="rounded-card border border-line bg-red-soft px-3.5 py-3 text-red">
        <p>{tree.error.message}</p>
        <p className="mt-1 font-mono text-[11.5px] opacity-80">{tree.error.code}</p>
      </div>
    )
  }
  if (tree.nodes.length === 0) {
    return <p className="px-1 py-6 text-center text-muted-foreground">{t("drive.empty")}</p>
  }
  return (
    <div className="rounded-card border border-line bg-card px-3.5 py-2">
      <ul role="tree" aria-label={t("drive.treeLabel", { path })}>
        {tree.nodes.map((node) => (
          <TreeItem key={node.path} node={node} />
        ))}
      </ul>
    </div>
  )
}

function TreeItem({ node }: { node: TreeNode }) {
  const children = node.children ?? []
  return (
    <li role="treeitem" aria-expanded={node.type === "dir" ? children.length > 0 : undefined}>
      <span className="flex min-h-7 items-center gap-2 text-[12.5px]">
        {node.type === "dir" ? (
          <Folder aria-hidden className="size-[15px] shrink-0 text-primary" />
        ) : (
          <File aria-hidden className="size-[15px] shrink-0 text-muted-foreground" />
        )}
        <span className={node.type === "dir" ? "font-medium" : ""}>{node.name}</span>
      </span>
      {children.length > 0 && (
        <ul role="group" className="ml-[9px] border-l border-line-2 pl-3">
          {children.map((child) => (
            <TreeItem key={child.path} node={child} />
          ))}
        </ul>
      )}
    </li>
  )
}

function joinPath(dir: string, name: string): string {
  return (dir === "/" ? "" : dir) + "/" + name
}

function NewFolderSheet({
  backend,
  path,
  onDone,
  onClose,
}: {
  backend: Backend
  path: string
  onDone: () => void
  onClose: () => void
}) {
  const { t } = useI18n()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<BackendError | null>(null)

  const create = async () => {
    if (!name.trim() || busy) return
    setBusy(true)
    setError(null)
    try {
      await backend.drive.mkdir(joinPath(path, name.trim()))
      onDone()
    } catch (err) {
      setError(err as BackendError)
      setBusy(false)
    }
  }

  return (
    <Sheet title={t("sheet.newFolder.title")} onClose={onClose}>
      <label className="block text-[12px] text-muted-foreground">
        {t("sheet.newFolder.name")}
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoFocus
          className="mt-1 h-8 w-full rounded-control border border-line bg-background px-2 text-[13px] text-fg outline-none focus:border-ring"
        />
      </label>
      <SheetError error={error} />
      <SheetButtons confirmLabel={t("sheet.create")} busy={busy} onConfirm={create} onClose={onClose} />
    </Sheet>
  )
}

function MoveSheet({
  backend,
  entry,
  onDone,
  onClose,
}: {
  backend: Backend
  entry: Entry
  onDone: () => void
  onClose: () => void
}) {
  const { t } = useI18n()
  const [dest, setDest] = useState(entry.path)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<BackendError | null>(null)

  const move = async () => {
    if (!dest.trim() || busy) return
    setBusy(true)
    setError(null)
    try {
      await backend.drive.move(entry.path, dest.trim(), { confirm: true })
      onDone()
    } catch (err) {
      setError(err as BackendError)
      setBusy(false)
    }
  }

  return (
    <Sheet title={t("sheet.move.title")} onClose={onClose}>
      <label className="block text-[12px] text-muted-foreground">
        {t("sheet.move.path")}
        <input
          value={dest}
          onChange={(e) => setDest(e.target.value)}
          autoFocus
          className="mt-1 h-8 w-full rounded-control border border-line bg-background px-2 font-mono text-[12.5px] text-fg outline-none focus:border-ring"
        />
      </label>
      <SheetError error={error} />
      <SheetButtons confirmLabel={t("sheet.confirm")} busy={busy} onConfirm={move} onClose={onClose} />
    </Sheet>
  )
}

function DeleteSheet({
  backend,
  entry,
  onDone,
  onClose,
}: {
  backend: Backend
  entry: Entry
  onDone: () => void
  onClose: () => void
}) {
  const { t } = useI18n()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<BackendError | null>(null)

  const remove = async () => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await backend.drive.delete(entry.path, { confirm: true })
      onDone()
    } catch (err) {
      setError(err as BackendError)
      setBusy(false)
    }
  }

  return (
    <Sheet title={t("sheet.delete.title")} onClose={onClose}>
      <p className="text-[12.5px] text-fg-2">{t("sheet.delete.body", { name: entry.name })}</p>
      <SheetError error={error} />
      <SheetButtons
        confirmLabel={t("drive.delete")}
        destructive
        busy={busy}
        onConfirm={remove}
        onClose={onClose}
      />
    </Sheet>
  )
}

function ShareSheet({ backend, entry, onClose }: { backend: Backend; entry: Entry; onClose: () => void }) {
  const { t } = useI18n()
  const [link, setLink] = useState<ShareLink | null>(null)
  const [error, setError] = useState<BackendError | null>(null)

  useEffect(() => {
    let live = true
    backend.drive.share(entry.path).then(
      (l) => live && setLink(l),
      (err: BackendError) => live && setError(err),
    )
    return () => {
      live = false
    }
  }, [backend, entry.path])

  return (
    <Sheet title={t("sheet.share.title")} onClose={onClose}>
      {link ? (
        <div>
          <code className="block break-all rounded-control bg-pill px-2 py-1.5 font-mono text-[12px] text-fg">
            {link.url}
          </code>
          {link.hashtag && (
            <code className="mt-1.5 block rounded-control bg-pill px-2 py-1.5 font-mono text-[12px] text-fg">
              {link.hashtag}
            </code>
          )}
          <p className="mt-2 text-[12px] text-muted-foreground">{t("sheet.share.hint", { channel: link.channel })}</p>
        </div>
      ) : (
        <p className="text-[12.5px] text-muted-foreground">{t("drive.loading")}</p>
      )}
      <SheetError error={error} />
      <div className="mt-4 flex justify-end">
        <Button variant="ghost" size="sm" onClick={onClose}>
          {t("sheet.close")}
        </Button>
      </div>
    </Sheet>
  )
}

function fileType(name: string, t: Translate): string {
  const i = name.lastIndexOf(".")
  if (i <= 0 || i === name.length - 1) return t("drive.file")
  return name.slice(i + 1).toUpperCase()
}

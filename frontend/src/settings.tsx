import { useEffect, useState } from "react"
import { Eye, EyeOff } from "lucide-react"

import type { Backend, BackendError, ConfigEntry, OmarchyState, Versions } from "@/backend"
import { Card, Row, Segmented } from "@/card"
import { useI18n } from "@/i18n"
import type { LanguagePref } from "@/i18n"
import { ErrorText } from "@/sheet"
import type { ThemeMode } from "@/theme"

export interface SettingsProps {
  backend: Backend
  theme: ThemeMode
  onTheme: (mode: ThemeMode) => void
  language: LanguagePref
  onLanguage: (pref: LanguagePref) => void
  /** Null until the first read resolves. */
  omarchy: OmarchyState | null
  omarchyOn: boolean
  onOmarchyToggle: (on: boolean) => void
}

export function SettingsScreen(props: SettingsProps) {
  return (
    <div className="mx-auto flex max-w-xl flex-col gap-3.5">
      <Appearance {...props} />
      <Configuration backend={props.backend} />
      <About backend={props.backend} />
    </div>
  )
}

function Appearance({ theme, onTheme, language, onLanguage, omarchy, omarchyOn, onOmarchyToggle }: SettingsProps) {
  const { t } = useI18n()
  const omarchyActive = omarchyOn && (omarchy?.available ?? false)
  return (
    <Card label={t("settings.appearance")}>
      <Row>
        <span className="flex-1">{t("settings.theme")}</span>
        {omarchyActive ? (
          <span className="text-[12px] text-muted-foreground">
            {t("omarchy.themeManaged", { name: omarchy?.theme?.name ?? "" })}
          </span>
        ) : (
          <Segmented<ThemeMode>
            label={t("settings.theme")}
            value={theme}
            onChange={onTheme}
            options={[
              { value: "system", name: t("theme.system") },
              { value: "light", name: t("theme.light") },
              { value: "dark", name: t("theme.dark") },
            ]}
          />
        )}
      </Row>
      <Row>
        <span className="flex-1">{t("settings.language")}</span>
        <Segmented<LanguagePref>
          label={t("settings.language")}
          value={language}
          onChange={onLanguage}
          options={[
            { value: "system", name: t("language.system") },
            { value: "en", name: t("language.en") },
            { value: "zh-CN", name: t("language.zh-CN") },
          ]}
        />
      </Row>
      {omarchy?.available && (
        <Row>
          <span className="flex-1">{t("omarchy.mode")}</span>
          {omarchy.theme?.name && (
            <span className="text-[12px] text-primary">{omarchy.theme.name}</span>
          )}
          <button
            type="button"
            role="switch"
            aria-checked={omarchyOn}
            aria-label={t("omarchy.toggle")}
            onClick={() => onOmarchyToggle(!omarchyOn)}
            className="relative h-5 w-9 rounded-full bg-seg-track transition-colors duration-150 ease-quiet aria-checked:bg-primary"
          >
            <span
              aria-hidden
              className="absolute top-0.5 left-0.5 size-4 rounded-full bg-white shadow-seg transition-transform duration-150 ease-quiet in-aria-checked:translate-x-4"
            />
          </button>
        </Row>
      )}
    </Card>
  )
}

type ConfigState =
  | { state: "loading" }
  | { state: "ready"; entries: ConfigEntry[] }
  | { state: "failed"; error: BackendError }

function Configuration({ backend }: { backend: Backend }) {
  const { t } = useI18n()
  const [config, setConfig] = useState<ConfigState>({ state: "loading" })

  useEffect(() => {
    let live = true
    backend.settings.listConfig().then(
      (entries) => live && setConfig({ state: "ready", entries }),
      (error: BackendError) => live && setConfig({ state: "failed", error }),
    )
    return () => {
      live = false
    }
  }, [backend])

  if (config.state === "loading") {
    return (
      <Card label={t("settings.configuration")}>
        <li className="px-3.5 py-3 text-muted-foreground">{t("settings.loading")}</li>
      </Card>
    )
  }
  if (config.state === "failed") {
    return (
      <Card label={t("settings.configuration")}>
        <li role="alert" className="px-3.5 py-3 text-red">
          <ErrorText message={config.error.message} code={config.error.code} />
        </li>
      </Card>
    )
  }
  return (
    <Card label={t("settings.configuration")}>
      {config.entries.map((e) => (
        <ConfigRow
          key={e.key}
          entry={e}
          onReveal={async () => {
            const revealed = await backend.settings.revealSecret(e.key, true)
            return String(revealed.value)
          }}
          onSave={async (value) => {
            const saved = await backend.settings.setConfig(e.key, value)
            setConfig((c) =>
              c.state === "ready"
                ? { state: "ready", entries: c.entries.map((x) => (x.key === e.key ? saved : x)) }
                : c,
            )
            return saved
          }}
        />
      ))}
    </Card>
  )
}

function ConfigRow({
  entry,
  onReveal,
  onSave,
}: {
  entry: ConfigEntry
  onReveal: () => Promise<string>
  onSave: (value: string) => Promise<ConfigEntry>
}) {
  const { t } = useI18n()
  const [draft, setDraft] = useState(String(entry.value))
  // The value the draft is compared against: the revealed one for a secret,
  // so merely revealing is not an edit.
  const [baseline, setBaseline] = useState(String(entry.value))
  const [revealed, setRevealed] = useState(false)
  const [error, setError] = useState<BackendError | null>(null)
  // Set by a save whose key td-gui reads only at startup.
  const [restartNote, setRestartNote] = useState(false)
  const masked = entry.secret && !revealed
  const dirty = !masked && draft !== baseline

  return (
    <li className="px-3.5 py-2">
      <div className="flex min-h-7 items-center gap-3">
        <span className="w-52 shrink-0 truncate font-mono text-[12px] text-fg-2">{entry.key}</span>
        {masked ? (
          <>
            <span className="flex-1 truncate font-mono text-[12px] text-muted-foreground">
              {String(entry.value)}
            </span>
            <button
              type="button"
              aria-label={t("settings.reveal", { key: entry.key })}
              onClick={() => {
                onReveal().then(
                  (value) => {
                    setDraft(value)
                    setBaseline(value)
                    setRevealed(true)
                  },
                  (err: BackendError) => setError(err),
                )
              }}
              className="grid size-6 shrink-0 place-items-center rounded-control text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg"
            >
              <Eye aria-hidden className="size-3.5" />
            </button>
          </>
        ) : (
          <>
            <input
              aria-label={entry.key}
              value={draft}
              onChange={(e) => {
                setDraft(e.target.value)
                setError(null)
                setRestartNote(false)
              }}
              spellCheck={false}
              className="min-w-0 flex-1 rounded-control border border-line bg-card-2 px-2 py-1 font-mono text-[12px] outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
            />
            {entry.secret && (
              <button
                type="button"
                aria-label={t("settings.hide", { key: entry.key })}
                onClick={() => {
                  setRevealed(false)
                  setError(null)
                }}
                className="grid size-6 shrink-0 place-items-center rounded-control text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg"
              >
                <EyeOff aria-hidden className="size-3.5" />
              </button>
            )}
            {dirty && (
              <button
                type="button"
                aria-label={t("settings.save", { key: entry.key })}
                onClick={() => {
                  onSave(draft).then(
                    (saved) => {
                      setDraft(String(saved.value))
                      setBaseline(String(saved.value))
                      setRestartNote(saved.restart_required ?? false)
                      if (saved.secret) setRevealed(false)
                    },
                    (err: BackendError) => setError(err),
                  )
                }}
                className="h-6 shrink-0 rounded-control bg-primary px-2.5 text-[12px] font-medium text-primary-foreground transition-colors duration-150 ease-quiet hover:bg-primary/80"
              >
                {t("settings.saveShort")}
              </button>
            )}
          </>
        )}
      </div>
      {error && (
        <p role="alert" className="pt-1 pl-2 text-[12px] text-red">
          <ErrorText message={error.message} code={error.code} />
        </p>
      )}
      {restartNote && !error && (
        <p role="status" className="pt-1 pl-2 text-[12px] text-muted-foreground">
          {t("settings.restartRequired")}
        </p>
      )}
    </li>
  )
}

function About({ backend }: { backend: Backend }) {
  const { t } = useI18n()
  const [versions, setVersions] = useState<Versions | null>(null)

  useEffect(() => {
    let live = true
    backend.settings.versions().then(
      (v) => live && setVersions(v),
      () => {},
    )
    return () => {
      live = false
    }
  }, [backend])

  return (
    <Card label={t("settings.about")}>
      <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5">
        <span className="flex-1">td-gui</span>
        <span className="font-mono text-[12px] text-muted-foreground">{versions?.gui ?? "…"}</span>
      </li>
      <li className="flex min-h-9 items-center gap-3 px-3.5 py-1.5">
        <span className="flex-1">td</span>
        <span className="font-mono text-[12px] text-muted-foreground">
          {versions === null ? "…" : versions.cli || t("settings.cliNotInstalled")}
        </span>
      </li>
    </Card>
  )
}

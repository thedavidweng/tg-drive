import { useCallback, useEffect, useState } from "react"
import { LogOut, Monitor, Moon, Sun } from "lucide-react"

import { ErrorAlert, LoginScreen, SetupScreen } from "@/auth"
import type { AuthStatus, AuthUser, Backend, BackendError, OmarchyState } from "@/backend"
import { ChannelSwitcher } from "@/channels"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { DriveScreen } from "@/drive"
import { ImportScreen } from "@/import"
import { I18nProvider, storeLanguage, storedLanguage, useI18n, type LanguagePref } from "@/i18n"
import type { MessageKey } from "@/i18n/en"
import { MaintenanceScreen } from "@/maintenance"
import {
  applyOmarchyTheme,
  clearOmarchyTheme,
  omarchyEnabled,
  storeOmarchyEnabled,
} from "@/omarchy"
import { SettingsScreen } from "@/settings"
import { applyTheme, storedTheme, type ThemeMode } from "@/theme"
import { TransfersScreen } from "@/transfers"

// inDemo marks the tabs the website's live demo (ADR 0041) keeps open.
const tabs = [
  { id: "drive", label: "tab.drive", inDemo: true },
  { id: "transfers", label: "tab.transfers", inDemo: true },
  { id: "import", label: "tab.import", inDemo: false },
  { id: "maintenance", label: "tab.maintenance", inDemo: false },
  { id: "settings", label: "tab.settings", inDemo: false },
] as const satisfies readonly { id: string; label: MessageKey; inDemo: boolean }[]

/**
 * demo is the website's live demo (ADR 0041): the same screens on an
 * in-memory backend, with everything that would reach the account or the
 * local disk (uploads, logout, drive binding, Import, Maintenance,
 * Settings) disabled.
 */
export function App({
  backend,
  languages,
  demo = false,
}: {
  backend: Backend
  languages: readonly string[]
  demo?: boolean
}) {
  const [theme, setTheme] = useState<ThemeMode>(storedTheme)
  const [language, setLanguage] = useState<LanguagePref>(storedLanguage)
  const [omarchy, setOmarchy] = useState<OmarchyState | null>(null)
  const [omarchyOn, setOmarchyOn] = useState(omarchyEnabled)

  // Adopt the Omarchy look when it is detected and not switched off, and
  // follow its theme changes from Go.
  useEffect(() => {
    let live = true
    backend.settings.omarchy().then(
      (state) => {
        if (!live) return
        setOmarchy(state)
        if (state.available && state.theme && omarchyEnabled()) applyOmarchyTheme(state.theme)
      },
      () => {},
    )
    const off = backend.settings.onOmarchyTheme((theme) => {
      setOmarchy((s) => (s?.available ? { ...s, theme } : s))
      if (omarchyEnabled()) applyOmarchyTheme(theme)
    })
    return () => {
      live = false
      off()
    }
  }, [backend])

  return (
    <I18nProvider languages={languages} language={language}>
      <Shell
        backend={backend}
        demo={demo}
        theme={theme}
        onTheme={(mode) => {
          applyTheme(mode)
          setTheme(mode)
        }}
        language={language}
        onLanguage={(pref) => {
          storeLanguage(pref)
          setLanguage(pref)
        }}
        omarchy={omarchy}
        omarchyOn={omarchyOn}
        onOmarchyToggle={(on) => {
          storeOmarchyEnabled(on)
          setOmarchyOn(on)
          if (on && omarchy?.theme) {
            applyOmarchyTheme(omarchy.theme)
          } else {
            clearOmarchyTheme()
          }
        }}
      />
    </I18nProvider>
  )
}

type Gate =
  | { state: "loading" }
  | { state: "failed"; error: BackendError }
  | { state: "ready"; status: AuthStatus }

interface ShellProps {
  backend: Backend
  demo: boolean
  theme: ThemeMode
  onTheme: (mode: ThemeMode) => void
  language: LanguagePref
  onLanguage: (pref: LanguagePref) => void
  omarchy: OmarchyState | null
  omarchyOn: boolean
  onOmarchyToggle: (on: boolean) => void
}

function Shell(props: ShellProps) {
  const { backend, demo } = props
  const { t } = useI18n()
  const [gate, setGate] = useState<Gate>({ state: "loading" })
  // The channel the drive is bound to; switching remounts the Drive view,
  // which then lists the new channel from its root.
  const [activeChannel, setActiveChannel] = useState("")
  const refresh = useCallback(() => {
    backend.auth.status().then(
      (status) => setGate({ state: "ready", status }),
      (error: BackendError) => setGate({ state: "failed", error }),
    )
  }, [backend])
  useEffect(refresh, [refresh])

  // The Drive and other tabs are reachable only with a usable session:
  // without credentials the app lands on setup, without a login on login.
  if (gate.state === "loading") {
    return (
      <main className="app-drag h-full p-3.5">
        <p className="px-1 py-6 text-center text-muted-foreground">{t("auth.checking")}</p>
      </main>
    )
  }
  if (gate.state === "failed") {
    return (
      <main className="app-drag h-full p-3.5">
        <ErrorAlert error={gate.error} />
      </main>
    )
  }
  if (!gate.status.configured) {
    return <SetupScreen backend={backend} onDone={refresh} />
  }
  if (!gate.status.authenticated) {
    return <LoginScreen backend={backend} hasPhone={gate.status.has_phone} onLoggedIn={refresh} />
  }
  return (
    <Tabs defaultValue="drive" className="h-full gap-0">
      <header className="app-drag app-header grid h-13 shrink-0 grid-cols-[1fr_auto_1fr] items-center gap-3 border-b border-line px-3.5">
        <div className="flex min-w-0 items-center gap-1.5">
          <Logo />
          <ChannelSwitcher backend={backend} onActiveChange={setActiveChannel} disabled={demo} />
        </div>
        <TabsList
          aria-label={t("tabs.label")}
          className="h-7 rounded-seg bg-seg-track p-0.5 group-data-horizontal/tabs:h-7"
        >
          {tabs.map((tab) => (
            <TabsTrigger
              key={tab.id}
              value={tab.id}
              disabled={demo && !tab.inDemo}
              title={demo && !tab.inDemo ? t("demo.unavailable") : undefined}
              className="h-6 flex-none rounded-control border-0 px-3 text-[12.5px] font-normal text-ctl-fg transition-colors duration-180 ease-quiet hover:text-fg-2 data-active:bg-seg-thumb data-active:text-fg data-active:shadow-seg dark:data-active:border-0 dark:data-active:bg-seg-thumb"
            >
              {t(tab.label)}
            </TabsTrigger>
          ))}
        </TabsList>
        <div className="flex min-w-0 items-center justify-end gap-1">
          {gate.status.user && (
            <AccountChip
              user={gate.status.user}
              logoutDisabled={demo}
              onLogout={() => {
                // A failed logout leaves the session as it was; refresh
                // either way so the chip reflects the truth.
                void backend.auth.logout().then(refresh, refresh)
              }}
            />
          )}
          <ThemeToggle theme={props.theme} onTheme={props.onTheme} />
        </div>
      </header>
      <main className="min-h-0 flex-1 overflow-auto p-3.5">
        <TabsContent value="drive">
          <DriveScreen key={activeChannel} backend={backend} uploadsDisabled={demo} />
        </TabsContent>
        <TabsContent value="transfers">
          <TransfersScreen backend={backend} />
        </TabsContent>
        <TabsContent value="import">
          <ImportScreen backend={backend} />
        </TabsContent>
        <TabsContent value="maintenance">
          <MaintenanceScreen backend={backend} />
        </TabsContent>
        <TabsContent value="settings">
          <SettingsScreen
            backend={backend}
            theme={props.theme}
            onTheme={props.onTheme}
            language={props.language}
            onLanguage={props.onLanguage}
            omarchy={props.omarchy}
            omarchyOn={props.omarchyOn}
            onOmarchyToggle={props.onOmarchyToggle}
          />
        </TabsContent>
      </main>
    </Tabs>
  )
}

function Logo() {
  return (
    <span aria-hidden className="grid size-7 shrink-0 place-items-center text-primary">
      <svg viewBox="0 0 24 24" className="size-[18px]">
        <path
          fill="currentColor"
          d="M21.4 3.6 2.9 10.7c-1 .4-1 1.8 0 2.1l4.6 1.5 1.8 5.5c.3.8 1.3 1 1.9.4l2.6-2.5 4.6 3.4c.7.5 1.6.1 1.8-.7L23 4.8c.2-.9-.7-1.6-1.6-1.2ZM9.9 14.6l-.5 3.6-1.3-4.3 9.6-6.3-7.8 7Z"
        />
      </svg>
    </span>
  )
}

/** The logged-in account with the logout action, in the header's right. */
function AccountChip({
  user,
  logoutDisabled,
  onLogout,
}: {
  user: AuthUser
  logoutDisabled: boolean
  onLogout: () => void
}) {
  const { t } = useI18n()
  return (
    <span
      aria-label={t("auth.account", { name: user.display_name })}
      className="flex h-7 min-w-0 items-center gap-1 rounded-full bg-pill pr-0.5 pl-3 text-[12px] text-fg-2"
    >
      <span className="flex min-w-0 items-baseline gap-1.5">
        <span className="max-w-32 truncate font-medium text-fg">{user.display_name}</span>
        {user.phone && <span className="shrink-0 text-muted-foreground tabular-nums">{user.phone}</span>}
      </span>
      <button
        type="button"
        aria-label={t("auth.logout")}
        title={logoutDisabled ? t("demo.unavailable") : t("auth.logout")}
        disabled={logoutDisabled}
        onClick={onLogout}
        className="grid size-6 shrink-0 place-items-center rounded-full text-ctl-fg transition-[background-color,color,scale] duration-150 ease-quiet hover:bg-pill-hover hover:text-fg active:scale-94 disabled:pointer-events-none disabled:opacity-40"
      >
        <LogOut aria-hidden className="size-3.5" />
      </button>
    </span>
  )
}

const nextMode: Record<ThemeMode, ThemeMode> = { system: "light", light: "dark", dark: "system" }
const modeIcon = { system: Monitor, light: Sun, dark: Moon }

function ThemeToggle({ theme, onTheme }: { theme: ThemeMode; onTheme: (mode: ThemeMode) => void }) {
  const { t } = useI18n()
  const Icon = modeIcon[theme]
  const label = t("theme.toggle", { mode: t(`theme.${theme}`) })
  return (
    // Omarchy owns the theme while its look applies.
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={() => onTheme(nextMode[theme])}
      className="om-hide grid size-7 place-items-center rounded-control text-ctl-fg transition-[background-color,color,scale] duration-150 ease-quiet hover:bg-pill-hover hover:text-fg active:scale-94"
    >
      <Icon aria-hidden className="size-[15px]" />
    </button>
  )
}

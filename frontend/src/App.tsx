import { useEffect, useState } from "react"
import { Monitor, Moon, Sun } from "lucide-react"

import type { Backend, OmarchyState } from "@/backend"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { DriveScreen } from "@/drive"
import { I18nProvider, storeLanguage, storedLanguage, useI18n, type LanguagePref } from "@/i18n"
import type { MessageKey } from "@/i18n/en"
import {
  applyOmarchyTheme,
  clearOmarchyTheme,
  omarchyEnabled,
  storeOmarchyEnabled,
} from "@/omarchy"
import { SettingsScreen } from "@/settings"
import { applyTheme, storedTheme, type ThemeMode } from "@/theme"

const tabs = [
  { id: "drive", label: "tab.drive" },
  { id: "transfers", label: "tab.transfers" },
  { id: "import", label: "tab.import" },
  { id: "maintenance", label: "tab.maintenance" },
  { id: "settings", label: "tab.settings" },
] as const satisfies readonly { id: string; label: MessageKey }[]

export function App({ backend, languages }: { backend: Backend; languages: readonly string[] }) {
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

interface ShellProps {
  backend: Backend
  theme: ThemeMode
  onTheme: (mode: ThemeMode) => void
  language: LanguagePref
  onLanguage: (pref: LanguagePref) => void
  omarchy: OmarchyState | null
  omarchyOn: boolean
  onOmarchyToggle: (on: boolean) => void
}

function Shell(props: ShellProps) {
  const { backend } = props
  const { t } = useI18n()
  return (
    <Tabs defaultValue="drive" className="h-full gap-0">
      <header className="grid h-12 shrink-0 grid-cols-[1fr_auto_1fr] items-center border-b border-line px-3.5">
        <Logo />
        <TabsList
          aria-label={t("tabs.label")}
          className="h-7 rounded-seg bg-seg-track p-0.5 group-data-horizontal/tabs:h-7"
        >
          {tabs.map((tab) => (
            <TabsTrigger
              key={tab.id}
              value={tab.id}
              className="h-6 flex-none rounded-control border-0 px-3 text-[12.5px] font-normal text-ctl-fg transition-colors duration-180 ease-quiet hover:text-fg-2 data-active:bg-seg-thumb data-active:text-fg data-active:shadow-seg dark:data-active:border-0 dark:data-active:bg-seg-thumb"
            >
              {t(tab.label)}
            </TabsTrigger>
          ))}
        </TabsList>
        <div className="flex justify-end">
          <ThemeToggle theme={props.theme} onTheme={props.onTheme} />
        </div>
      </header>
      <main className="min-h-0 flex-1 overflow-auto p-3.5">
        <TabsContent value="drive">
          <DriveScreen backend={backend} path="/" />
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
        {tabs.slice(1, 4).map((tab) => (
          <TabsContent key={tab.id} value={tab.id}>
            <p className="px-1 py-6 text-center text-muted-foreground">{t("placeholder.notYet")}</p>
          </TabsContent>
        ))}
      </main>
    </Tabs>
  )
}

function Logo() {
  return (
    <div className="flex items-center gap-2 font-semibold tracking-[-.01em]">
      <svg aria-hidden viewBox="0 0 24 24" className="size-5 text-primary">
        <path
          fill="currentColor"
          d="M21.4 3.6 2.9 10.7c-1 .4-1 1.8 0 2.1l4.6 1.5 1.8 5.5c.3.8 1.3 1 1.9.4l2.6-2.5 4.6 3.4c.7.5 1.6.1 1.8-.7L23 4.8c.2-.9-.7-1.6-1.6-1.2ZM9.9 14.6l-.5 3.6-1.3-4.3 9.6-6.3-7.8 7Z"
        />
      </svg>
      <span>td</span>
    </div>
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
      className="om-hide grid size-[26px] place-items-center rounded-[7px] text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg"
    >
      <Icon aria-hidden className="size-4" />
    </button>
  )
}

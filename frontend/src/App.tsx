import { useState } from "react"
import { Monitor, Moon, Sun } from "lucide-react"

import type { Backend } from "@/backend"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { DriveScreen } from "@/drive"
import { I18nProvider, useI18n } from "@/i18n"
import type { MessageKey } from "@/i18n/en"
import { applyTheme, storedTheme, type ThemeMode } from "@/theme"

const tabs = [
  { id: "drive", label: "tab.drive" },
  { id: "transfers", label: "tab.transfers" },
  { id: "import", label: "tab.import" },
  { id: "maintenance", label: "tab.maintenance" },
  { id: "settings", label: "tab.settings" },
] as const satisfies readonly { id: string; label: MessageKey }[]

export function App({ backend, languages }: { backend: Backend; languages: readonly string[] }) {
  return (
    <I18nProvider languages={languages}>
      <Shell backend={backend} />
    </I18nProvider>
  )
}

function Shell({ backend }: { backend: Backend }) {
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
          <ThemeToggle />
        </div>
      </header>
      <main className="min-h-0 flex-1 overflow-auto p-3.5">
        <TabsContent value="drive">
          <DriveScreen backend={backend} path="/" />
        </TabsContent>
        {tabs.slice(1).map((tab) => (
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

function ThemeToggle() {
  const { t } = useI18n()
  const [mode, setMode] = useState(storedTheme)
  const Icon = modeIcon[mode]
  const label = t("theme.toggle", { mode: t(`theme.${mode}`) })
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={() => {
        const next = nextMode[mode]
        applyTheme(next)
        setMode(next)
      }}
      className="grid size-[26px] place-items-center rounded-[7px] text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg"
    >
      <Icon aria-hidden className="size-4" />
    </button>
  )
}

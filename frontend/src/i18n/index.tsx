import { createContext, useContext, useMemo, type ReactNode } from "react"

import { en, type MessageKey } from "./en"
import { zhCN } from "./zh-CN"

export type Locale = "en" | "zh-CN"

const catalogues: Record<Locale, Record<MessageKey, string>> = { en, "zh-CN": zhCN }

/**
 * The first of the user's preferred languages td has a catalogue for, or
 * English. Any Chinese tag selects Simplified Chinese, the only Chinese
 * catalogue.
 */
export function resolveLocale(languages: readonly string[]): Locale {
  for (const tag of languages) {
    const primary = tag.toLowerCase().split("-")[0]
    if (primary === "en") return "en"
    if (primary === "zh") return "zh-CN"
  }
  return "en"
}

export type Translate = (key: MessageKey, vars?: Record<string, string | number>) => string

const I18nContext = createContext<{ locale: Locale; t: Translate } | null>(null)

export function I18nProvider({ languages, children }: { languages: readonly string[]; children: ReactNode }) {
  const value = useMemo(() => {
    const locale = resolveLocale(languages)
    const catalogue = catalogues[locale]
    const t: Translate = (key, vars) =>
      (catalogue[key] ?? en[key]).replace(/\{(\w+)\}/g, (m, name: string) => String(vars?.[name] ?? m))
    return { locale, t }
  }, [languages])
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  const ctx = useContext(I18nContext)
  if (!ctx) throw new Error("useI18n outside I18nProvider")
  return ctx
}

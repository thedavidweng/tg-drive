import { createContext, useContext, useEffect, useMemo, type ReactNode } from "react"

import { en, type MessageKey } from "./en"
import { zhCN } from "./zh-CN"

export type Locale = "en" | "zh-CN"

/** The Settings language override: "system" follows the system language. */
export type LanguagePref = "system" | Locale

/**
 * The localStorage key of the manual language override, a GUI-side display
 * preference like the theme override (src/theme.ts), not a td config key.
 */
export const languageStorageKey = "td-locale"

export function storedLanguage(): LanguagePref {
  const v = localStorage.getItem(languageStorageKey)
  return v === "en" || v === "zh-CN" ? v : "system"
}

export function storeLanguage(pref: LanguagePref) {
  if (pref === "system") {
    localStorage.removeItem(languageStorageKey)
  } else {
    localStorage.setItem(languageStorageKey, pref)
  }
}

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

export function I18nProvider({
  languages,
  language = "system",
  children,
}: {
  languages: readonly string[]
  language?: LanguagePref
  children: ReactNode
}) {
  const value = useMemo(() => {
    const locale = language === "system" ? resolveLocale(languages) : language
    const catalogue = catalogues[locale]
    const t: Translate = (key, vars) =>
      (catalogue[key] ?? en[key]).replace(/\{(\w+)\}/g, (m, name: string) => String(vars?.[name] ?? m))
    return { locale, t }
  }, [languages, language])
  useEffect(() => {
    document.documentElement.lang = value.locale
  }, [value.locale])
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  const ctx = useContext(I18nContext)
  if (!ctx) throw new Error("useI18n outside I18nProvider")
  return ctx
}

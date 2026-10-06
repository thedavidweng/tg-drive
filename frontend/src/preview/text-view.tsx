import { useMemo } from "react"
import hljs from "highlight.js/lib/core"
import bash from "highlight.js/lib/languages/bash"
import c from "highlight.js/lib/languages/c"
import cpp from "highlight.js/lib/languages/cpp"
import csharp from "highlight.js/lib/languages/csharp"
import css from "highlight.js/lib/languages/css"
import diff from "highlight.js/lib/languages/diff"
import dockerfile from "highlight.js/lib/languages/dockerfile"
import go from "highlight.js/lib/languages/go"
import ini from "highlight.js/lib/languages/ini"
import java from "highlight.js/lib/languages/java"
import javascript from "highlight.js/lib/languages/javascript"
import json from "highlight.js/lib/languages/json"
import kotlin from "highlight.js/lib/languages/kotlin"
import lua from "highlight.js/lib/languages/lua"
import makefile from "highlight.js/lib/languages/makefile"
import php from "highlight.js/lib/languages/php"
import python from "highlight.js/lib/languages/python"
import ruby from "highlight.js/lib/languages/ruby"
import rust from "highlight.js/lib/languages/rust"
import scss from "highlight.js/lib/languages/scss"
import sql from "highlight.js/lib/languages/sql"
import swift from "highlight.js/lib/languages/swift"
import typescript from "highlight.js/lib/languages/typescript"
import xml from "highlight.js/lib/languages/xml"
import yaml from "highlight.js/lib/languages/yaml"

import { useI18n } from "@/i18n"
import { SafeHTML, type Allowlist } from "@/preview/safe-html"
import { languageFor } from "@/preview/text-languages"
import { useTextChunks } from "@/preview/text-chunks"
import { PartialNotice, useChunkFailure } from "@/preview/text-notice"
import type { PreviewViewProps } from "@/preview/types"
import "@/preview/text.css"

const languages = {
  bash, c, cpp, csharp, css, diff, dockerfile, go, ini, java, javascript, json,
  kotlin, lua, makefile, php, python, ruby, rust, scss, sql, swift, typescript, xml, yaml,
}
for (const [name, lang] of Object.entries(languages)) hljs.registerLanguage(name, lang)

// Highlighting 1 MiB of source takes highlight.js close to a second and
// yields hundreds of thousands of spans; past this many characters the
// text stays plain so a large file never freezes the view.
const highlightLimit = 256 << 10

// highlight.js escapes the source; its output is still rebuilt against
// this allowlist so a highlighter bug can never turn file content into
// live markup.
const highlightAllow: Allowlist = { span: ["class"] }

/** Highlighted HTML for text, or undefined to show it plain. */
function highlighted(text: string, language: string | undefined): string | undefined {
  if (!language || !hljs.getLanguage(language) || text.length > highlightLimit) return undefined
  return hljs.highlight(text, { language, ignoreIllegals: true }).value
}

// Every text-like file, HTML included, renders as inert source text: never
// in a frame and never as markup.
export function TextView({ descriptor, onError }: PreviewViewProps) {
  const { t } = useI18n()
  const chunks = useTextChunks(descriptor)
  useChunkFailure(chunks, onError)
  const language = languageFor(descriptor.name, descriptor.mime)
  const text = chunks.state === "ready" ? chunks.text : ""
  const html = useMemo(() => highlighted(text, language), [text, language])

  if (chunks.state !== "ready") {
    return <p className="py-16 text-center text-muted-foreground">{t("preview.loading")}</p>
  }
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 overflow-auto bg-card">
        <pre
          aria-label={t("preview.text.label", { name: descriptor.name })}
          tabIndex={0}
          className="td-code m-0 p-4 font-mono text-[12.5px] leading-[1.55] whitespace-pre text-fg"
        >
          {html !== undefined ? (
            <SafeHTML as="code" className="hljs" data-language={language} html={html} allow={highlightAllow} />
          ) : (
            <code>{chunks.text}</code>
          )}
        </pre>
      </div>
      <PartialNotice chunks={chunks} size={descriptor.size} />
    </div>
  )
}

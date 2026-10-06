import { useMemo, type MouseEvent } from "react"
import { Marked } from "marked"

import { useI18n } from "@/i18n"
import { SafeHTML, safeHref, type Allowlist } from "@/preview/safe-html"
import { PartialNotice, useChunkFailure } from "@/preview/text-notice"
import { useTextChunks } from "@/preview/text-chunks"
import type { PreviewViewProps } from "@/preview/types"
import "@/preview/markdown.css"

function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`)
}

// Raw HTML in a note is shown as its source text, the way HTML files are,
// so a note's markup can never become live elements; Markdown's own syntax
// is the only thing that renders. Images never load: a remote one would
// reach the network and a relative one points at nothing, so its alt text
// stands in.
const marked = new Marked({
  gfm: true,
  async: false,
  renderer: {
    html: ({ text }) => escapeHTML(text),
    image: ({ text }) => escapeHTML(text),
    link({ href, title, tokens }) {
      const label = this.parser.parseInline(tokens)
      if (!safeHref(href)) return label
      return `<a href="${escapeHTML(href)}" title="${escapeHTML(title || href)}">${label}</a>`
    },
  },
})

// What marked itself emits; the rebuild drops anything else, so the
// renderer above is not the only line of defence.
const markdownAllow: Allowlist = {
  p: [], br: [], hr: [], h1: [], h2: [], h3: [], h4: [], h5: [], h6: [],
  strong: [], em: [], del: [], code: ["class"], pre: [], blockquote: [],
  ul: [], ol: ["start"], li: [], input: ["type", "checked", "disabled"],
  table: [], thead: [], tbody: [], tr: [], th: ["align"], td: ["align"],
  a: ["href", "title"],
}

/** Links in a note never navigate the app away; their address shows as a tooltip. */
function stayInApp(e: MouseEvent) {
  if ((e.target as Element).closest("a")) e.preventDefault()
}

export function MarkdownView({ descriptor, onError }: PreviewViewProps) {
  const { t } = useI18n()
  const chunks = useTextChunks(descriptor)
  useChunkFailure(chunks, onError)
  const source = chunks.state === "ready" ? chunks.text : ""
  const html = useMemo(() => marked.parse(source) as string, [source])

  if (chunks.state !== "ready") {
    return <p className="py-16 text-center text-muted-foreground">{t("preview.loading")}</p>
  }
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 overflow-auto bg-card">
        <SafeHTML
          as="article"
          aria-label={t("preview.text.label", { name: descriptor.name })}
          className="td-markdown mx-auto max-w-[760px] px-6 py-5 text-[14px] leading-[1.6] text-fg"
          onClick={stayInApp}
          onAuxClick={stayInApp}
          html={html}
          allow={markdownAllow}
        />
      </div>
      <PartialNotice chunks={chunks} size={descriptor.size} />
    </div>
  )
}

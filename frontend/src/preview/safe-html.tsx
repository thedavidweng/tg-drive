import { useLayoutEffect, useRef, type HTMLAttributes } from "react"

/** Allowed elements, each with the attributes it may keep. */
export type Allowlist = Readonly<Record<string, readonly string[]>>

const xhtml = "http://www.w3.org/1999/xhtml"

// Elements whose content is never text worth keeping once the element goes.
const dropWithContent = new Set([
  "script", "style", "template", "noscript", "iframe", "frame", "frameset", "object", "embed",
  "textarea", "select", "title", "svg", "math", "head",
])

/** Whether a link target is safe to keep: web, mail, in-page, or relative. */
export function safeHref(href: string): boolean {
  // URL parsing drops tabs and newlines anywhere and leading controls and
  // spaces, so "java\tscript:" is still a javascript: URL.
  const cleaned = href.replace(/[\t\n\r]/g, "").replace(/^[\0-\x20]+/, "")
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(cleaned)?.[1]?.toLowerCase()
  return scheme === undefined || scheme === "http" || scheme === "https" || scheme === "mailto"
}

function copyChildren(from: Node, to: Node, allow: Allowlist, doc: Document) {
  for (const child of Array.from(from.childNodes)) {
    if (child.nodeType === Node.TEXT_NODE) {
      to.appendChild(doc.createTextNode(child.nodeValue ?? ""))
      continue
    }
    if (child.nodeType !== Node.ELEMENT_NODE) continue
    const el = child as Element
    const tag = el.localName
    if (el.namespaceURI !== xhtml || dropWithContent.has(tag)) continue
    const attrs = Object.hasOwn(allow, tag) ? allow[tag] : undefined
    if (!attrs) {
      copyChildren(el, to, allow, doc)
      continue
    }
    const copy = doc.createElement(tag)
    for (const name of attrs) {
      const value = el.getAttribute(name)
      if (value === null || (name === "href" && !safeHref(value))) continue
      copy.setAttribute(name, value)
    }
    copyChildren(el, copy, allow, doc)
    to.appendChild(copy)
  }
}

/**
 * Renderer-produced HTML rebuilt from scratch: the markup is parsed in an
 * inert document, and only allowlisted elements and attributes are created
 * anew in the page, so nothing else (script, handlers, styles, frames,
 * resource loads) can survive. Rebuilding nodes rather than re-serialising
 * a string leaves no parse round trip to mutate the result.
 */
export function sanitizedFragment(html: string, allow: Allowlist, doc: Document = document): DocumentFragment {
  const parsed = new DOMParser().parseFromString(html, "text/html")
  const out = doc.createDocumentFragment()
  copyChildren(parsed.body, out, allow, doc)
  return out
}

/** An element whose children are html, sanitised against allow. */
export function SafeHTML({
  as: Tag = "div",
  html,
  allow,
  ...rest
}: { as?: "div" | "code" | "article"; html: string; allow: Allowlist } & HTMLAttributes<HTMLElement>) {
  const ref = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    ref.current?.replaceChildren(sanitizedFragment(html, allow))
  }, [html, allow])
  return <Tag ref={ref as never} {...rest} />
}

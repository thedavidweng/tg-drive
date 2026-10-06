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

// CSS functions that only compute a value. Any other function (url,
// image-set, src, cross-fade, ...) may name a resource to load.
const inertCSSFunctions = new Set([
  "rgb", "rgba", "hsl", "hsla", "hwb", "lab", "lch", "oklab", "oklch", "color", "color-mix",
  "var", "calc", "min", "max", "clamp", "counter", "counters", "attr",
  "rect", "inset", "polygon", "circle", "ellipse",
  "matrix", "matrix3d", "translate", "translatex", "translatey", "translate3d",
  "scale", "scalex", "scaley", "scale3d", "rotate", "rotatex", "rotatey", "rotatez", "rotate3d",
  "skew", "skewx", "skewy", "perspective",
  "linear-gradient", "radial-gradient", "conic-gradient",
  "repeating-linear-gradient", "repeating-radial-gradient", "repeating-conic-gradient",
  "not", "is", "where", "has", "lang", "dir", "nth-child", "nth-last-child", "nth-of-type", "nth-last-of-type",
])

const embeddedImage = /^\s*data:image\//i

function decodeCSSEscapes(css: string): string {
  return css.replace(/\\([0-9a-f]{1,6})[ \t\n\r\f]?|\\([^\n\r\f0-9a-f])/gi, (_, hex: string | undefined, ch: string | undefined) => {
    if (ch !== undefined) return ch
    const cp = parseInt(hex!, 16)
    return cp > 0 && cp <= 0x10ffff ? String.fromCodePoint(cp) : "\ufffd"
  })
}

// Comments and strings share one scan: a quote in a comment cannot hide
// live CSS, and comment delimiters in a string are only string content.
function maskCSS(css: string): string | null {
  let masked = ""
  for (let i = 0; i < css.length;) {
    const ch = css[i]
    if (ch === "/" && css[i + 1] === "*") {
      const end = css.indexOf("*/", i + 2)
      i = end < 0 ? css.length : end + 2
    } else if (ch === '"' || ch === "'") {
      const start = ++i
      while (i < css.length && css[i] !== ch) {
        if (/[\n\r\f]/.test(css[i])) return null
        if (css[i] === "\\") {
          i += css[i + 1] === "\r" && css[i + 2] === "\n" ? 3 : 2
        } else {
          i++
        }
      }
      if (i >= css.length) return null
      const value = decodeCSSEscapes(css.slice(start, i))
      masked += embeddedImage.test(value) ? '"data:image/"' : '""'
      i++
    } else if (ch === "\\") {
      // An escaped quote or slash is not a string/comment delimiter.
      masked += css.slice(i, i + 2)
      i += 2
    } else {
      masked += ch
      i++
    }
  }
  return masked
}

/**
 * Whether CSS can apply without the page reaching for anything: no
 * @import, and no url() or other resource-naming function except an
 * embedded data: image. Escapes are decoded first, so "u\72l(" is still
 * url(. Anything this cannot vouch for is refused rather than repaired.
 */
export function inertCSS(css: string): boolean {
  // Strings stand in only for whether they hold an embedded image, so a
  // parenthesis in content: "(1)" is not read as a function call.
  const masked = maskCSS(css)
  if (masked === null) return false
  const plain = decodeCSSEscapes(masked)
  if (/@import/i.test(plain)) return false
  for (const m of plain.matchAll(/([a-z0-9_-]+)\(/gi)) {
    const name = m[1].toLowerCase()
    if (name === "url") {
      const arg = plain.slice(m.index + m[0].length).replace(/^\s*["']?/, "")
      if (!embeddedImage.test(arg)) return false
    } else if (!inertCSSFunctions.has(name)) {
      return false
    }
  }
  return true
}

/** What a rebuild keeps. */
export interface SanitizePolicy {
  /** Allowed elements, each with the attributes it may keep. */
  allow: Allowlist
  /**
   * Keep <style> elements and style attributes whose CSS is inert (see
   * inertCSS); off, every style is dropped.
   */
  css?: boolean
}

function keepAttribute(name: string, value: string, policy: SanitizePolicy): boolean {
  switch (name) {
    case "href":
      return safeHref(value)
    case "src":
      return embeddedImage.test(value)
    case "style":
      return policy.css === true && inertCSS(value)
    default:
      return true
  }
}

function copyChildren(from: Node, to: Node, policy: SanitizePolicy, doc: Document) {
  for (const child of Array.from(from.childNodes)) {
    if (child.nodeType === Node.TEXT_NODE) {
      to.appendChild(doc.createTextNode(child.nodeValue ?? ""))
      continue
    }
    if (child.nodeType !== Node.ELEMENT_NODE) continue
    const el = child as Element
    const tag = el.localName
    if (el.namespaceURI !== xhtml) continue
    if (tag === "style" && policy.css) {
      const css = el.textContent ?? ""
      if (inertCSS(css)) to.appendChild(doc.createElement("style")).textContent = css
      continue
    }
    if (dropWithContent.has(tag)) continue
    const attrs = Object.hasOwn(policy.allow, tag) ? policy.allow[tag] : undefined
    if (!attrs) {
      copyChildren(el, to, policy, doc)
      continue
    }
    const copy = doc.createElement(tag)
    for (const name of attrs) {
      const value = el.getAttribute(name)
      if (value === null || !keepAttribute(name, value, policy)) continue
      copy.setAttribute(name, value)
    }
    copyChildren(el, copy, policy, doc)
    to.appendChild(copy)
  }
}

/**
 * The children of a rendered but detached subtree, rebuilt from scratch
 * the way sanitizedFragment rebuilds parsed HTML. The source must never
 * have been connected to a page, or its resources may already have loaded.
 */
export function sanitizedCopy(from: Node, policy: SanitizePolicy, doc: Document = document): DocumentFragment {
  const out = doc.createDocumentFragment()
  copyChildren(from, out, policy, doc)
  return out
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
  return sanitizedCopy(parsed.body, { allow }, doc)
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

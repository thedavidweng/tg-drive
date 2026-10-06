import { baseMIME, extensionOf } from "@/preview/registry"

export const languageByExtension: Record<string, string> = {
  sh: "bash", bash: "bash", zsh: "bash",
  c: "c", h: "c",
  cc: "cpp", cpp: "cpp", cxx: "cpp", hh: "cpp", hpp: "cpp", hxx: "cpp",
  cs: "csharp",
  css: "css",
  diff: "diff", patch: "diff",
  dockerfile: "dockerfile",
  go: "go",
  ini: "ini", cfg: "ini", conf: "ini", toml: "ini", properties: "ini",
  java: "java",
  js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript",
  json: "json", jsonc: "json", json5: "json", geojson: "json",
  kt: "kotlin", kts: "kotlin",
  lua: "lua",
  mk: "makefile", mak: "makefile",
  php: "php",
  py: "python", pyi: "python",
  rb: "ruby",
  rs: "rust",
  scss: "scss",
  sql: "sql",
  swift: "swift",
  ts: "typescript", tsx: "typescript", mts: "typescript", cts: "typescript",
  html: "xml", htm: "xml", xhtml: "xml", xml: "xml", plist: "xml", svg: "xml", xsl: "xml",
  yaml: "yaml", yml: "yaml",
}

const languageByMIME: Record<string, string> = {
  "application/json": "json",
  "application/yaml": "yaml",
  "application/x-yaml": "yaml",
  "text/yaml": "yaml",
  "application/toml": "ini",
  "text/html": "xml",
  "application/xhtml+xml": "xml",
  "application/xml": "xml",
  "text/xml": "xml",
  "text/css": "css",
  "text/javascript": "javascript",
  "application/javascript": "javascript",
  "application/sql": "sql",
  "application/x-sh": "bash",
}

/** The highlight.js language for a file, or undefined for plain text. */
export function languageFor(name: string, mime: string): string | undefined {
  const ext = extensionOf(name)
  return (
    (ext ? languageByExtension[ext] : undefined) ??
    languageByMIME[baseMIME(mime)]
  )
}

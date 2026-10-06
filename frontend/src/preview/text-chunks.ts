import { useCallback, useEffect, useRef, useState } from "react"

import type { PreviewDescriptor } from "@/backend"

/** How much of a text file one read fetches: the first view, and each "load more". */
export const TEXT_CHUNK_BYTES = 2 << 20

/** One chunk of a file; eof says nothing follows it. */
export interface ByteRange {
  bytes: Uint8Array
  eof: boolean
}

function concat(parts: Uint8Array[], n: number): Uint8Array {
  const out = new Uint8Array(n)
  let at = 0
  for (const p of parts) {
    out.set(p.subarray(0, n - at), at)
    at += Math.min(p.length, n - at)
    if (at === n) break
  }
  return out
}

/**
 * Reads bytes [offset, offset+length) of url with a Range request. A server
 * (or data URL) that ignores Range answers 200 with the whole body; that
 * body is streamed only as far as the chunk's end and then cancelled, so a
 * huge file is never read past the chunk being shown.
 */
export async function fetchByteRange(url: string, offset: number, length: number, signal?: AbortSignal): Promise<ByteRange> {
  const res = await fetch(url, { headers: { Range: `bytes=${offset}-${offset + length - 1}` }, signal })
  if (res.status === 416) {
    await res.body?.cancel()
    return { bytes: new Uint8Array(0), eof: true }
  }
  if (!res.ok) {
    await res.body?.cancel()
    throw new Error(`HTTP ${res.status}`)
  }
  if (res.status === 206) {
    const bytes = new Uint8Array(await res.arrayBuffer()).subarray(0, length)
    const total = Number(/\/(\d+)\s*$/.exec(res.headers.get("Content-Range") ?? "")?.[1] ?? NaN)
    const eof = Number.isFinite(total) ? offset + bytes.length >= total : bytes.length < length
    return { bytes, eof }
  }

  // 200: the whole body from byte 0. One byte past the chunk tells whether more follows.
  const want = offset + length + 1
  if (!res.body) {
    const all = new Uint8Array(await res.arrayBuffer())
    return { bytes: all.slice(offset, offset + length), eof: all.length <= offset + length }
  }
  const reader = res.body.getReader()
  const parts: Uint8Array[] = []
  let got = 0
  let done = false
  while (got < want) {
    const next = await reader.read()
    if (next.done) {
      done = true
      break
    }
    parts.push(next.value)
    got += next.value.length
  }
  if (!done) await reader.cancel()
  const head = concat(parts, Math.min(got, offset + length))
  return { bytes: head.slice(Math.min(offset, head.length)), eof: done && got <= offset + length }
}

export type TextChunks =
  | { state: "loading" }
  | { state: "failed"; message: string }
  | {
      state: "ready"
      text: string
      /** Bytes shown so far. */
      loaded: number
      /** False until the whole file is shown. */
      complete: boolean
      loadingMore: boolean
      loadMore: () => void
    }

/**
 * The descriptor's file as text, one TEXT_CHUNK_BYTES chunk at a time: the
 * first chunk on mount, each further one only when loadMore is called.
 * Decoding streams across chunks, so a character split by a chunk boundary
 * is joined rather than garbled.
 */
export function useTextChunks(descriptor: PreviewDescriptor): TextChunks {
  const [text, setText] = useState("")
  const [loaded, setLoaded] = useState(0)
  const [complete, setComplete] = useState(false)
  const [busy, setBusy] = useState(true)
  const [failed, setFailed] = useState<string | undefined>(undefined)
  const decoder = useRef<TextDecoder | null>(null)
  const abort = useRef<AbortController | null>(null)
  // The indexed size is not what the URL serves for a text message; only
  // the route's own length (or the response ending) marks the end.
  const mediaSize = descriptor.capabilities.media_size

  const read = useCallback(
    (offset: number) => {
      const ctl = new AbortController()
      abort.current = ctl
      if (offset === 0) decoder.current = new TextDecoder("utf-8")
      fetchByteRange(descriptor.url, offset, TEXT_CHUNK_BYTES, ctl.signal).then(
        ({ bytes, eof }) => {
          if (ctl.signal.aborted) return
          // A NUL byte means a binary file under a text name (an MPEG
          // stream called .ts); the fallback's Download serves it better.
          if (offset === 0 && bytes.includes(0)) {
            setFailed("")
            return
          }
          const end = eof || (mediaSize >= 0 && offset + bytes.length >= mediaSize)
          const piece = decoder.current!.decode(bytes, { stream: !end })
          setText((t) => (offset === 0 ? piece : t + piece))
          setLoaded(offset + bytes.length)
          setComplete(end)
          setBusy(false)
        },
        (err: unknown) => {
          if (ctl.signal.aborted) return
          setFailed(err instanceof Error ? err.message : String(err))
        },
      )
    },
    [descriptor.url, mediaSize],
  )

  useEffect(() => {
    read(0)
    return () => abort.current?.abort()
  }, [read])

  const loadMore = useCallback(() => {
    if (busy || complete) return
    setBusy(true)
    read(loaded)
  }, [busy, complete, loaded, read])

  if (failed !== undefined) return { state: "failed", message: failed }
  if (busy && loaded === 0) return { state: "loading" }
  return { state: "ready", text, loaded, complete, loadingMore: busy, loadMore }
}

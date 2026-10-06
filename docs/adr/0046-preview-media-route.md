# 0046: In-app file preview over a same-origin media route

Status: Accepted. Amends ADR 0031 (the frontend's "no HTTP API" rule) for
preview bytes only.

Context: The Drive tab can only download a remote file before anyone can
look at it. An in-app preview needs the file's bytes inside the webview,
and the consumers that show them (`<img>`, `<video>`, `<audio>`, PDF and
document renderers) take a URL and issue HTTP byte-range requests, so
seeking a large video or reading one page of a PDF fetches only that part.
ADR 0031 has the frontend reach Go only through Wails-generated bindings and
typed events. A binding call returns its result as one JSON message, so
passing a multi-gigabyte file through it would copy whole buffers across
the Go/JavaScript boundary and throw away Telegram's ability to read a
byte range. Telegram's own range reads (`upload.getFile`) have alignment
and size rules no browser range respects.

Decision: Preview bytes travel through one narrow, same-origin media route;
everything else stays on bindings and typed events.

- **The control plane does not change.** Opening a preview calls the typed
  `Drive.Preview` binding, which returns a descriptor: display name, remote
  path, MIME type, indexed size, date, capabilities (whether the URL serves
  byte ranges and the exact size it serves) and the media URL. It carries no
  Telegram channel or message IDs, access hashes, session data, or local
  paths. Errors stay `{code, category, message}` binding errors.
- **One reserved route in the existing asset handler.** `cmd/td-gui`
  mounts the media handler at `/td-media/` through the Wails asset
  middleware, ahead of the embedded frontend. Desktop builds therefore open
  no listener or port; server mode serves the route over Wails' existing
  loopback server. That server's write timeout is raised from Wails' 30
  seconds to 24 hours: a player may stream a whole film as one open-ended
  range response, and the default would cut it mid-playback. The timeout
  bounds only how long one response may take to write; a closed preview or
  a disconnected client still ends it at once through the request context.
  The handler is a framework-neutral `http.Handler` in `internal/gui`, so
  Wails stays confined to `cmd/td-gui` and the handler is tested with
  `httptest`.
- **A per-process capability.** Every media URL carries a random 256-bit
  capability generated when the GUI opens. It is never persisted or logged
  (it travels as a query value because the Wails request log prints paths
  only); a request without it is refused. Another local web page cannot
  guess a URL, and a URL dies with the process.
- **URLs are pinned to a file row.** The URL names an opaque per-preview
  token mapped to the files row resolved when the preview was prepared, not
  to a path in whichever channel is active. A channel switch, a move, or a
  replace never makes an existing URL serve another file; the handler
  serves only rows the current index still holds as active. A bounded
  number of prepared URLs stays servable; an older one answers 404 and the
  frontend prepares it again.
- **HTTP semantics.** HEAD and GET with at most one byte range: 200 for the
  whole body, 206 with `Content-Range`, 416 with the total size for an
  unsatisfiable range. Ranges are advertised only for a representation
  Telegram can read exactly (documents, attributed videos, and native
  photos whose largest size Telegram states the exact length of); any other
  native photo and every text message is served whole without a length. Responses are
  `private, no-store`, `nosniff`, inline with an RFC 5987 file name, and
  sandboxed by CSP if opened as a document; HTML, XHTML, XML, and script
  types are served as plain text so a stored page is shown, never run.
  Error bodies are the status text only. The request's context bounds the
  Telegram read, so closing a preview or a client disconnect cancels it.
- **A new Telegram seam for exact ranges.** `MediaClient.ReadMediaRange`
  returns `MediaInfo` (exact size when provable, seekability, MIME) and
  writes exactly the requested interval. The gotd adapter translates it to
  precise `upload.getFile` requests (1 KiB aligned, at most 1 MiB, never
  crossing a 1 MiB boundary) and trims the overfetch, so a range near the
  end of a large file fetches only that range. `DownloadMedia` stays the
  whole-body read for downloads, repair, adopt, and import. A preview
  describes its file once (a zero-length read) and the handler reuses that
  description, because each read re-fetches the message.
- **Preview is not a Transfer.** Preview reads take no operation lock,
  create no Transfer row, stage, or history, and leave the index untouched.
  They go through the GUI's existing Telegram session and its session lock.
- **A frontend preview registry.** Providers declare MIME types and
  extensions; the registry tries every provider's MIME types before any
  extension and ends with a fallback (file details and Download), which is
  also what a failed preview shows. Each provider lives in its own module
  and registers with one line. Images (JPEG, PNG, GIF, WebP, SVG through
  `<img>`, which runs no SVG script) need no library.
- **Bundled preview libraries.** The viewers that need one use, all
  exact-pinned and bundled into the frontend build with no runtime CDN:
  ArtPlayer for video, EmbedPDF
  (`@embedpdf/react-pdf-viewer`, `@embedpdf/pdfium` and its WASM asset) for
  PDF, highlight.js and marked for text, code, and Markdown, and
  docx-preview, ExcelJS, and pptxviewjs for Office files. pptxviewjs
  requires its peers chart.js and jszip as direct dependencies. Each is
  loaded only when its viewer first opens. Audio needs no library: an
  APlayer-style control drives the webview's own `<audio>`.
- **Renderer HTML is rebuilt, not sanitized in place.** Markup a renderer
  produces (highlight.js, marked, docx-preview) is rebuilt node by node
  against a per-viewer element and attribute allowlist, rather than passed
  to a sanitizer library: DOMPurify throws or misbehaves under the
  happy-dom test DOM, so its output could not be verified in tests. Word
  documents keep their generated stylesheet and inline styles, inside a
  shadow root, only when the CSS cannot load anything: no `@import`, and
  no `url()` or other resource-naming function except an embedded `data:`
  image, checked after CSS escapes are decoded. Images keep only embedded
  `data:` sources. A document that smuggles a remote reference into its
  styles loses that stylesheet, not its text, so viewing a file never
  reaches the network.
- **Unreachable library fallbacks.** pptxviewjs carries a last-resort
  loader that injects jszip from cdnjs when no jszip is in scope. It never
  runs in td-gui: the bundled module sets `globalThis.JSZip` from its
  bundled jszip import when it is evaluated, before any file is opened,
  and the loader checks that global first. EmbedPDF's own CDN defaults
  (jsDelivr fonts and stamps, Google Fonts) are turned off in its
  configuration.

Considered options:

- Base64 or chunked bytes through bindings. Rejected: whole-buffer copies
  across IPC for every image, and no browser-native seeking for media.
- A second localhost HTTP server. Rejected: a new listener and port on the
  desktop, with its own origin and CORS surface, for what the asset handler
  already serves.
- Blob URLs filled by the frontend from binding calls. Rejected: the same
  IPC copies, and a whole file held in webview memory before it shows.
- Path-addressed media URLs (`/td-media/<channel>/<path>`). Rejected: they
  re-resolve on every request, so a channel switch could redirect an open
  preview to another file, and they leak channel identity into URLs.

Consequences: The frontend now has one HTTP surface besides the asset
files, limited to preview bytes behind a capability; the rule "no HTTP API"
still holds for commands and metadata. Whether a desktop webview's custom
scheme handler passes `Range` and 206 through (WKWebView, WebView2,
WebKitGTK) is verified by a manual smoke check per OS, not by CI; a viewer
that cannot seek still plays from the whole-body response. Codec and
document support is whatever the system webview and bundled libraries
provide; there is no transcoding. The fake Telegram serves the same exact
ranges and logs them (`TD_FAKE_RANGE_LOG`) so preview tests run offline.

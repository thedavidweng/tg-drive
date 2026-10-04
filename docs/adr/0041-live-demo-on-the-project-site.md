# 0041: Live demo on the project site

Status: Accepted.

Context: The project site (ADR 0040) shows a static screenshot of td-gui.
The GUI frontend already runs in any browser: `App` takes its `Backend` as
a prop, and the screen tests drive it with an in-memory backend
(`src/testing/memory-backend.ts`). A separately built mock for the site
would drift from the real GUI and be one more thing to maintain. The site
must stay fast and cheap to load, and the demo must not imply it touches a
Telegram account or the visitor's disk.

Decision: The site's showcase is the real frontend, built as a second
Vite entry, running on the existing in-memory backend.

- `bun run build:demo` (`vite build --mode demo`) builds `demo.html` and
  `src/demo/main.tsx` into `frontend/dist-demo/`. The entry renders `App`
  with `demo` set, on `MemoryBackend` unchanged; the Wails runtime is not
  in the bundle.
- `App`'s `demo` prop disables what would reach the account or the local
  disk: uploads, logout, the drive switcher, and the Import, Maintenance,
  and Settings tabs. Browsing, the tree view, folder edits, and downloads
  run against the in-memory drive and reset on reload.
- The drive holds well-known public test media (Blender open movies,
  Kodak images, sample audio and documents) listed in `src/demo/samples.ts`
  by path, real size, and source URL. No media is in the repository or
  fetched by the demo; the source URL is for a future file preview.
- On screens at least 760px wide, `site.js` shows an empty window and
  loads the demo into it in an iframe once the showcase scrolls near. The
  app lays out at the desktop window's size and is scaled to fit, so a
  narrower screen sees the same layout smaller. The iframe isolates the
  app's global styles from the site's. The screenshot goes stale as the
  GUI changes, so it is only a fallback: phones, no JavaScript, a demo
  that does not start within 15 seconds, and `og:image`. Screens that get
  the demo never download it.
- The site workflow also runs on `frontend/**`, builds the demo, and
  `site/publish.mjs` copies it to `demo/`, so the demo follows `main`.

Considered options:

- A purpose-built mock page. Rejected: it duplicates the GUI and drifts.
- Mounting the app directly in the site page. Rejected: Tailwind's base
  styles and the app's tokens would collide with the site's CSS.
- Hosting the sample media in the repository. Rejected: it adds tens of
  megabytes for files the demo never opens.

Consequences: The site now has a build step and needs bun in its workflow.
`MemoryBackend` is shared by the screen tests and the demo, so a change to
it shows on the site; demo-specific behavior belongs behind `App`'s `demo`
prop, not in the backend. A new GUI feature that reaches the account or
the disk has to decide whether `demo` disables it.

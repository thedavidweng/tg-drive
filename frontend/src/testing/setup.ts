import { GlobalRegistrator } from "@happy-dom/global-registrator"
import { plugin } from "bun"
import { basename, dirname } from "node:path"

// Vite serves ?url imports as browser assets; Bun's file loader instead
// returns a host path, which Windows would interpret as a URL scheme.
plugin({
  name: "vite-asset-urls",
  setup(build) {
    build.onResolve({ filter: /\?url$/ }, ({ path, importer }) => ({
      path: Bun.resolveSync(path.slice(0, -4), dirname(importer)),
      namespace: "vite-asset-url",
    }))
    build.onLoad({ filter: /.*/, namespace: "vite-asset-url" }, ({ path }) => ({
      loader: "object",
      exports: { default: `/assets/${basename(path)}` },
    }))
  },
})

GlobalRegistrator.register()

// happy-dom cannot decode media, but assigning src synchronously emits
// canplay during player construction. Real browsers queue readiness after
// decoding. Tests supply media events explicitly, so only reflect src here.
Object.defineProperty(HTMLMediaElement.prototype, "src", {
  ...Object.getOwnPropertyDescriptor(HTMLMediaElement.prototype, "src"),
  set(value: string) {
    this.setAttribute("src", value)
  },
})
await import("./fullscreen")

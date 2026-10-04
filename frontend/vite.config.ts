import { resolve } from "node:path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// https://vite.dev/config/
// --mode demo builds the website's live demo (ADR 0041) into dist-demo/,
// with relative asset URLs so it works under any site path.
export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": resolve(import.meta.dirname, "./src"),
    },
  },
  ...(mode === "demo" && {
    base: "./",
    build: {
      outDir: "dist-demo",
      rollupOptions: { input: resolve(import.meta.dirname, "demo.html") },
    },
  }),
}))

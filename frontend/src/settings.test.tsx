import { afterEach, expect, test } from "bun:test"
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react"

import { App } from "@/App"
import type { OmarchyTheme } from "@/backend"
import { eventEmitter, memoryBackend } from "@/testing/memory-backend"

afterEach(() => {
  cleanup()
  localStorage.clear()
  document.documentElement.classList.remove("omarchy")
  delete document.documentElement.dataset.theme
  delete document.documentElement.dataset.omarchyMode
  document.getElementById("omarchy-theme")?.remove()
})

async function openSettings() {
  // The auth gate resolves before the shell renders its tabs.
  const tab = await screen.findByRole("tab", { name: "Settings" })
  fireEvent.mouseDown(tab)
  fireEvent.click(tab)
}

const baseConfig = {
  "telegram.api_id": 1,
  "telegram.api_hash": "hash-value",
  "telegram.phone": "+15551234567",
  "transfers.concurrency": 2,
}

const omarchyTheme: OmarchyTheme = {
  name: "test-night",
  mode: "dark",
  stamp: "s1",
  vars: { "--bg": "#1a1b26", "--accent": "#7aa2f7", "--om-radius": "6px" },
}

test("Settings lists every config key with secrets masked until revealed", async () => {
  const backend = memoryBackend(
    { "/": [] },
    { config: baseConfig, secrets: ["telegram.api_hash", "telegram.phone"] },
  )
  render(<App backend={backend} languages={["en"]} />)
  await openSettings()

  expect(await screen.findByText("telegram.api_hash")).toBeTruthy()
  expect(screen.getByText("transfers.concurrency")).toBeTruthy()
  // secrets render masked; the stored values are nowhere in the document
  expect(screen.queryByText("hash-value")).toBeNull()
  expect(screen.queryByText("+15551234567")).toBeNull()

  fireEvent.click(screen.getByRole("button", { name: "Reveal telegram.api_hash" }))
  expect((await screen.findByLabelText("telegram.api_hash")) as HTMLInputElement).toHaveProperty(
    "value",
    "hash-value",
  )
  // the other secret stays masked
  expect(screen.queryByText("+15551234567")).toBeNull()
  // revealing alone is not an edit: no save offers itself
  expect(screen.queryByRole("button", { name: "Save telegram.api_hash" })).toBeNull()
})

test("editing a key saves it through the backend and shows the saved value", async () => {
  const backend = memoryBackend({ "/": [] }, { config: baseConfig, secrets: ["telegram.api_hash"] })
  render(<App backend={backend} languages={["en"]} />)
  await openSettings()

  const input = (await screen.findByLabelText("transfers.concurrency")) as HTMLInputElement
  expect(input.value).toBe("2")
  fireEvent.change(input, { target: { value: "5" } })
  // act flushes the fake backend's resolved promise and the re-render.
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Save transfers.concurrency" }))
  })

  expect(screen.queryByRole("button", { name: "Save transfers.concurrency" })).toBeNull()
  expect((screen.getByLabelText("transfers.concurrency") as HTMLInputElement).value).toBe("5")
})

test("a rejected edit shows the service error", async () => {
  const backend = memoryBackend(
    { "/": [] },
    {
      config: baseConfig,
      setError: {
        code: "ERR_CONFIG_INVALID",
        category: "config",
        message: "transfers.concurrency must be a positive integer",
      },
    },
  )
  render(<App backend={backend} languages={["en"]} />)
  await openSettings()

  const input = (await screen.findByLabelText("transfers.concurrency")) as HTMLInputElement
  fireEvent.change(input, { target: { value: "0" } })
  fireEvent.click(screen.getByRole("button", { name: "Save transfers.concurrency" }))

  const alert = await screen.findByRole("alert")
  expect(alert.textContent).toContain("transfers.concurrency must be a positive integer")
})

test("the theme override applies the attribute and persists", async () => {
  const backend = memoryBackend({ "/": [] }, { config: baseConfig })
  render(<App backend={backend} languages={["en"]} />)
  await openSettings()

  fireEvent.click(await screen.findByRole("button", { name: "Dark" }))

  expect(document.documentElement.dataset.theme).toBe("dark")
  expect(localStorage.getItem("td-theme")).toBe("dark")
})

test("the language override re-renders in Simplified Chinese", async () => {
  const backend = memoryBackend({ "/": [] }, { config: baseConfig })
  render(<App backend={backend} languages={["en"]} />)
  await openSettings()

  fireEvent.click(await screen.findByRole("button", { name: "简体中文" }))

  expect(await screen.findByText("外观")).toBeTruthy()
  expect(screen.getByRole("tab", { name: "设置" })).toBeTruthy()
  expect(localStorage.getItem("td-locale")).toBe("zh-CN")
})

test("Omarchy mode applies the theme, follows changes, and can be switched off", async () => {
  const emitter = eventEmitter<OmarchyTheme>()
  const backend = memoryBackend(
    { "/": [] },
    {
      config: baseConfig,
      omarchy: { available: true, theme: omarchyTheme },
      onOmarchyTheme: emitter.subscribe,
    },
  )
  render(<App backend={backend} languages={["en"]} />)
  await openSettings()

  // detected and on: the theme's vars land on the root
  await screen.findByText("test-night")
  expect(document.documentElement.classList.contains("omarchy")).toBe(true)
  expect(document.getElementById("omarchy-theme")?.textContent).toContain("--accent: #7aa2f7")

  // a theme change from Go re-applies
  emitter.emit({ ...omarchyTheme, stamp: "s2", vars: { ...omarchyTheme.vars, "--accent": "#ff0000" } })
  await new Promise((r) => setTimeout(r, 0))
  expect(document.getElementById("omarchy-theme")?.textContent).toContain("--accent: #ff0000")

  // the switch turns it off and the window goes back to the normal theme
  fireEvent.click(screen.getByRole("switch", { name: "Omarchy mode" }))
  expect(document.documentElement.classList.contains("omarchy")).toBe(false)
  expect(document.getElementById("omarchy-theme")).toBeNull()
  expect(localStorage.getItem("td-omarchy")).toBe("off")
})

test("no Omarchy row when Omarchy is not detected", async () => {
  const backend = memoryBackend({ "/": [] }, { config: baseConfig })
  render(<App backend={backend} languages={["en"]} />)
  await openSettings()

  await screen.findByText("telegram.api_hash")
  expect(screen.queryByRole("switch", { name: "Omarchy mode" })).toBeNull()
})

test("About shows the versions of td-gui and td", async () => {
  const backend = memoryBackend(
    { "/": [] },
    { config: baseConfig, versions: { gui: "1.4.0", cli: "1.4.0" } },
  )
  render(<App backend={backend} languages={["en"]} />)
  await openSettings()

  const about = await screen.findByRole("region", { name: "About" })
  const rows = within(about).getAllByRole("listitem").map((r) => r.textContent)
  expect(rows).toEqual(["td-gui1.4.0", "td1.4.0"])
})

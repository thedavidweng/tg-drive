import { afterEach, expect, test } from "bun:test"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"

import { App } from "@/App"
import { memoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

// The interactive elements the global focus-visible rule in index.css
// covers; this suite walks exactly that set, so keyboard reachability and
// the focus ring are guaranteed over the same controls.
const interactiveSelector = 'button, a[href], input, select, textarea, [role="tab"], [role="switch"]'

function interactiveIn(root: ParentNode): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(interactiveSelector)).filter(
    (el) => !el.hasAttribute("disabled") && el.closest("[hidden]") === null,
  )
}

const seed = {
  "/": [
    { name: "photos", path: "/photos", type: "dir" as const, size: 0, date: "2026-01-01T00:00:00Z" },
    { name: "notes.txt", path: "/notes.txt", type: "file" as const, size: 2048, date: "2026-01-01T00:00:00Z" },
  ],
  "/photos": [],
}

test("every interactive control is a natively focusable element that takes focus", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  const controls = interactiveIn(document.body)
  // The shell alone — tabs, switcher, theme toggle, toolbar — is well past
  // this; the number only guards against the walk silently finding nothing.
  expect(controls.length).toBeGreaterThan(15)
  const failures: string[] = []
  for (const el of controls) {
    const name = `${el.tagName.toLowerCase()} "${(el.getAttribute("aria-label") ?? el.textContent?.trim() ?? "").slice(0, 40)}"`
    // A positive tabindex hijacks the natural tab order. Inactive tabs
    // carry -1 on purpose: the strip is a roving-tabindex widget reached
    // by arrows from the active tab.
    if (el.tabIndex > 0) failures.push(`${name} has tabindex ${el.tabIndex}`)
    el.focus()
    if (document.activeElement !== el) failures.push(`${name} did not take focus`)
  }
  expect(failures).toEqual([])
})

test("the tab strip moves focus and selection with the arrow keys", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  const drive = await screen.findByRole("tab", { name: "Drive" })

  // act() flushes the roving-tabindex state the focus triggers.
  act(() => drive.focus())
  fireEvent.keyDown(drive, { key: "ArrowRight" })
  const transfers = screen.getByRole("tab", { name: "Transfers" })
  // Radix moves focus and selection together, a tick after the keydown.
  await waitFor(() => expect(document.activeElement === transfers).toBe(true))
  expect(transfers.getAttribute("aria-selected")).toBe("true")
  // The newly active panel is in tab reach; the previous one is gone.
  expect(await screen.findByRole("region", { name: "Active" })).toBeTruthy()

  fireEvent.keyDown(transfers, { key: "ArrowLeft" })
  await waitFor(() => expect(document.activeElement === drive).toBe(true))
  expect(drive.getAttribute("aria-selected")).toBe("true")
})

test("a sheet traps Tab, closes on Escape, and returns focus to its trigger", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })
  const trigger = screen.getByRole("button", { name: "New folder" })
  trigger.focus()

  fireEvent.click(trigger)
  const dialog = await screen.findByRole("dialog", { name: "New folder" })
  // Focus moved into the sheet: the name field has autoFocus.
  expect(dialog.contains(document.activeElement)).toBe(true)

  const controls = interactiveIn(dialog)
  expect(controls.length).toBeGreaterThan(1)
  // Tab past the last control wraps to the first, Shift+Tab from the
  // first wraps to the last: focus never leaves the sheet.
  act(() => controls[controls.length - 1].focus())
  fireEvent.keyDown(controls[controls.length - 1], { key: "Tab" })
  expect(document.activeElement === controls[0]).toBe(true)
  fireEvent.keyDown(controls[0], { key: "Tab", shiftKey: true })
  expect(document.activeElement === controls[controls.length - 1]).toBe(true)

  fireEvent.keyDown(document.activeElement!, { key: "Escape" })
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(document.activeElement === trigger).toBe(true)
})

test("a sheet without a field focuses its first control, not the page behind it", async () => {
  render(<App backend={memoryBackend(seed)} languages={["en"]} />)
  await screen.findByRole("list", { name: "Files in /" })

  fireEvent.click(screen.getByRole("button", { name: "Delete" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete file" })
  const active = document.activeElement
  expect(active instanceof HTMLElement && dialog.contains(active)).toBe(true)
  expect(active === interactiveIn(dialog)[0]).toBe(true)
})

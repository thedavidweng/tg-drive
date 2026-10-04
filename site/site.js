// Header border once the page scrolls under it.
const header = document.querySelector("[data-header]")
const onScroll = () => header.classList.toggle("is-stuck", window.scrollY > 4)
onScroll()
window.addEventListener("scroll", onScroll, { passive: true })

// Reveal sections as they scroll in, once. Cards in a group cascade.
document.querySelectorAll(".reveal-group").forEach((group) => {
  group.querySelectorAll(":scope > .reveal").forEach((el, i) => el.style.setProperty("--i", String(i % 3)))
})
const reveals = document.querySelectorAll(".reveal")
if ("IntersectionObserver" in window) {
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        e.target.classList.add("in")
        io.unobserve(e.target)
      }
    },
    { rootMargin: "0px 0px -80px 0px" },
  )
  reveals.forEach((el) => io.observe(el))
} else {
  reveals.forEach((el) => el.classList.add("in"))
}

// The showcase is the live demo on screens wide enough for the desktop
// layout: an empty window until the showcase scrolls near, then the demo
// in an iframe. The screenshot is only the fallback (narrow screens, no
// IntersectionObserver, a demo that does not start), so a wide screen
// never downloads it.
const shot = document.querySelector("[data-demo]")

function showScreenshot() {
  shot.replaceChildren()
  const img = document.createElement("img")
  img.src = "img/screenshot.png"
  img.width = 2142
  img.height = 1502
  img.alt = shot.dataset.shotAlt
  shot.append(img)
}

if ("IntersectionObserver" in window && matchMedia("(min-width: 1000px)").matches) {
  const win = document.createElement("div")
  win.className = "demo-window"
  const lights = document.createElement("div")
  lights.className = "demo-lights"
  lights.setAttribute("aria-hidden", "true")
  lights.append(...[0, 1, 2].map(() => document.createElement("span")))
  win.append(lights)
  shot.replaceChildren(win)

  const io = new IntersectionObserver(
    (entries) => {
      if (!entries.some((e) => e.isIntersecting)) return
      io.disconnect()
      const frame = document.createElement("iframe")
      frame.src = "demo/demo.html"
      frame.title = "td-gui live demo"
      frame.addEventListener("load", () => {
        // The page loads even when its bundle does not; give React a
        // moment, then fall back if nothing rendered.
        setTimeout(() => {
          const root = frame.contentDocument?.getElementById("root")
          if (!root || root.childElementCount === 0) return showScreenshot()
          win.classList.add("live")
          document.querySelector("[data-demo-note]").hidden = false
        }, 300)
      })
      win.prepend(frame)
    },
    { rootMargin: "200px 0px" },
  )
  io.observe(shot)
} else {
  showScreenshot()
}

// Install method tabs and copy-to-clipboard.
const install = document.querySelector("[data-install]")
const tabs = [...install.querySelectorAll('[role="tab"]')]
const cmdText = install.querySelector("[data-cmd-text]")
const copyButton = install.querySelector("[data-copy]")
const status = install.querySelector("[data-copy-status]")
let copiedTimer = 0

function select(tab) {
  for (const t of tabs) {
    const on = t === tab
    t.setAttribute("aria-selected", String(on))
    t.tabIndex = on ? 0 : -1
  }
  cmdText.textContent = tab.dataset.cmd
  copyButton.classList.remove("copied")
}

tabs.forEach((tab, i) => {
  tab.addEventListener("click", () => select(tab))
  tab.addEventListener("keydown", (e) => {
    const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0
    if (!step) return
    e.preventDefault()
    const next = tabs[(i + step + tabs.length) % tabs.length]
    select(next)
    next.focus()
  })
})

copyButton.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(cmdText.textContent)
  } catch {
    return
  }
  copyButton.classList.add("copied")
  status.textContent = "Copied to clipboard"
  clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => {
    copyButton.classList.remove("copied")
    status.textContent = ""
  }, 1800)
})

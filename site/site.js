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

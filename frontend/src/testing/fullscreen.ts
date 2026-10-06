// happy-dom has no Fullscreen API, and players detect it once at import
// time, so this stand-in must be installed before any test imports them.
// It follows the standard API closely enough for a player to enter and
// leave fullscreen: fullscreenElement, fullscreenchange, the two methods.

let current: Element | null = null

function change(el: Element | null): Promise<void> {
  current = el
  document.dispatchEvent(new Event("fullscreenchange"))
  return Promise.resolve()
}

// The registered document's classes are not the global Document/Element,
// so the API goes on the prototypes the live document actually uses.
function protoNamed(obj: object, name: string): object {
  for (let p = Object.getPrototypeOf(obj); p; p = Object.getPrototypeOf(p)) {
    if (Object.prototype.hasOwnProperty.call(p, "constructor") && p.constructor.name === name) return p
  }
  throw new Error(`no ${name} prototype`)
}

const documentProto = protoNamed(document, "Document")
const elementProto = protoNamed(document.documentElement, "Element")

Object.defineProperty(documentProto, "fullscreenEnabled", { configurable: true, get: () => true })
Object.defineProperty(documentProto, "fullscreenElement", { configurable: true, get: () => current })
Object.defineProperty(documentProto, "exitFullscreen", {
  configurable: true,
  value: () => change(null),
})
Object.defineProperty(elementProto, "requestFullscreen", {
  configurable: true,
  value: function (this: Element) {
    return change(this)
  },
})

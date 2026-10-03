import { afterEach, expect, test } from "bun:test"
import { cleanup, fireEvent, render, screen } from "@testing-library/react"

import { App } from "@/App"
import { failingBackend, memoryBackend } from "@/testing/memory-backend"

afterEach(cleanup)

test("a machine without credentials lands on the setup form", async () => {
  render(<App backend={memoryBackend({}, { auth: { configured: false } })} languages={["en"]} />)

  const form = await screen.findByRole("form", { name: "Connect to Telegram" })
  expect(form).toBeTruthy()
  expect(screen.getByLabelText("api_id")).toBeTruthy()
  expect(screen.getByLabelText("api_hash")).toBeTruthy()
  expect(screen.getByLabelText("Phone")).toBeTruthy()
  // The tabs stay unreachable until login completes.
  expect(screen.queryByRole("tab", { name: "Drive" })).toBeNull()
})

test("the setup form says where to obtain api_id and api_hash", async () => {
  render(<App backend={memoryBackend({}, { auth: { configured: false } })} languages={["en"]} />)

  const link = await screen.findByRole("link", { name: /my\.telegram\.org/ })
  expect(link.getAttribute("href")).toBe("https://my.telegram.org")
})

test("the my.telegram.org link opens in the system browser", async () => {
  const backend = memoryBackend({}, { auth: { configured: false } })
  render(<App backend={backend} languages={["en"]} />)

  // The webview ignores target=_blank; the link goes through the backend.
  fireEvent.click(await screen.findByRole("link", { name: /my\.telegram\.org/ }))
  expect(backend.openedURLs).toEqual(["https://my.telegram.org"])
})

test("the api_hash field is masked until shown", async () => {
  render(<App backend={memoryBackend({}, { auth: { configured: false } })} languages={["en"]} />)

  const hash = (await screen.findByLabelText("api_hash")) as HTMLInputElement
  expect(hash.type).toBe("password")
  const show = screen.getByRole("button", { name: "Show api_hash" })
  expect(show.getAttribute("aria-pressed")).toBe("false")
  fireEvent.click(show)
  expect(hash.type).toBe("text")
  const hide = screen.getByRole("button", { name: "Hide api_hash" })
  expect(hide.getAttribute("aria-pressed")).toBe("true")
  fireEvent.click(hide)
  expect(hash.type).toBe("password")
})

test("setup saves the credentials and moves on to login", async () => {
  render(<App backend={memoryBackend({}, { auth: { configured: false } })} languages={["en"]} />)

  fireEvent.change(await screen.findByLabelText("api_id"), { target: { value: "1234" } })
  fireEvent.change(screen.getByLabelText("api_hash"), { target: { value: "0123456789abcdef" } })
  fireEvent.change(screen.getByLabelText("Phone"), { target: { value: "+15550001" } })
  fireEvent.click(screen.getByRole("button", { name: "Save and continue" }))

  expect(await screen.findByRole("heading", { name: "Log in to Telegram" })).toBeTruthy()
})

test("a setup error from the service shows in the form", async () => {
  render(<App backend={memoryBackend({}, { auth: { configured: false } })} languages={["en"]} />)

  fireEvent.change(await screen.findByLabelText("api_id"), { target: { value: "not-a-number" } })
  fireEvent.change(screen.getByLabelText("api_hash"), { target: { value: "hash" } })
  fireEvent.change(screen.getByLabelText("Phone"), { target: { value: "+15550001" } })
  fireEvent.click(screen.getByRole("button", { name: "Save and continue" }))

  const alert = await screen.findByRole("alert")
  expect(alert.textContent).toContain("ERR_CONFIG_INVALID")
  expect(screen.queryByRole("heading", { name: "Log in to Telegram" })).toBeNull()
})

test("login asks for the code and shows the account afterwards", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false } })} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))
  expect(await screen.findByText("Enter the login code Telegram sent you.")).toBeTruthy()
  // The attempt budget shows from the first attempt on.
  expect(screen.getByText("Attempt 1 of 3.")).toBeTruthy()
  fireEvent.change(screen.getByLabelText("Login code"), { target: { value: "12345" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify code" }))

  // Logged in: the header shows the account and the tabs are reachable.
  expect(await screen.findByLabelText("Logged in as Test User")).toBeTruthy()
  expect(screen.getByRole("tab", { name: "Drive" })).toBeTruthy()
})

test("a wrong code re-asks with the attempt count", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false } })} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))
  fireEvent.change(await screen.findByLabelText("Login code"), { target: { value: "00000" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify code" }))

  expect(await screen.findByText("Invalid code, try again.")).toBeTruthy()
  expect(screen.getByText("Attempt 2 of 3.")).toBeTruthy()
  fireEvent.change(screen.getByLabelText("Login code"), { target: { value: "12345" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify code" }))
  expect(await screen.findByLabelText("Logged in as Test User")).toBeTruthy()
})

test("two-step verification asks for the password after the code", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false, password: "hunter2" } })} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))
  fireEvent.change(await screen.findByLabelText("Login code"), { target: { value: "12345" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify code" }))

  expect(await screen.findByText("Your account has two-step verification. Enter your password.")).toBeTruthy()
  fireEvent.change(screen.getByLabelText("Two-step verification password"), { target: { value: "wrong" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify password" }))
  expect(await screen.findByText("Invalid password, try again.")).toBeTruthy()
  fireEvent.change(screen.getByLabelText("Two-step verification password"), { target: { value: "hunter2" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify password" }))

  expect(await screen.findByLabelText("Logged in as Test User")).toBeTruthy()
})

test("restarting login reuses the pending code and says so", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false } })} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))
  await screen.findByLabelText("Login code")
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }))

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))
  expect(await screen.findByText("Reusing the code Telegram sent earlier.")).toBeTruthy()
  expect(screen.getByText("Attempt 1 of 3.")).toBeTruthy()

  // A wrong reused code: the retry, the reuse note, and the count together.
  fireEvent.change(screen.getByLabelText("Login code"), { target: { value: "00000" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify code" }))
  expect(await screen.findByText("Invalid code, try again.")).toBeTruthy()
  expect(screen.getByText("Reusing the code Telegram sent earlier.")).toBeTruthy()
  expect(screen.getByText("Attempt 2 of 3.")).toBeTruthy()
})

test("requesting a new code after cancelling sends a fresh one", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false } })} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))
  await screen.findByLabelText("Login code")
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }))

  fireEvent.click(await screen.findByRole("button", { name: "Request a new code instead" }))
  // A fresh code, not the reused pending one.
  expect(await screen.findByText("Enter the login code Telegram sent you.")).toBeTruthy()
  expect(screen.queryByText("Reusing the code Telegram sent earlier.")).toBeNull()
})

test("an expired reused code is resent and says so", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false, expiresReusedCode: true } })} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))
  await screen.findByLabelText("Login code")
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }))

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))
  fireEvent.change(await screen.findByLabelText("Login code"), { target: { value: "12345" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify code" }))

  expect(await screen.findByText("The previous code expired; Telegram sent a new one.")).toBeTruthy()
  expect(screen.getByText("Attempt 1 of 3.")).toBeTruthy()
  // A wrong resent code keeps the resent note beside the retry and the count.
  fireEvent.change(screen.getByLabelText("Login code"), { target: { value: "00000" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify code" }))
  expect(await screen.findByText("Attempt 2 of 3.")).toBeTruthy()
  expect(screen.getByText("Invalid code, try again.")).toBeTruthy()
  expect(screen.getByText("The previous code expired; Telegram sent a new one.")).toBeTruthy()
  fireEvent.change(screen.getByLabelText("Login code"), { target: { value: "12345" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify code" }))
  expect(await screen.findByLabelText("Logged in as Test User")).toBeTruthy()
})

test("a rate limit shows how long to wait", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false, rateLimitSeconds: 85286 } })} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))

  const alert = await screen.findByRole("alert")
  expect(alert.textContent).toContain("Telegram rate-limited logins for this account. Try again in 23h 41m.")
  expect(alert.textContent).toContain("ERR_TELEGRAM_RATE_LIMITED")
  // Back returns to the start of the login screen.
  fireEvent.click(screen.getByRole("button", { name: "Back" }))
  expect(await screen.findByRole("button", { name: "Send login code" })).toBeTruthy()
})

test("a config without a phone asks for it on the login screen", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false, phone: "" } })} languages={["en"]} />)

  const phone = await screen.findByLabelText("Phone")
  fireEvent.change(phone, { target: { value: "+15550001" } })
  fireEvent.click(screen.getByRole("button", { name: "Send login code" }))

  fireEvent.change(await screen.findByLabelText("Login code"), { target: { value: "12345" } })
  fireEvent.click(screen.getByRole("button", { name: "Verify code" }))
  expect(await screen.findByLabelText("Logged in as Test User")).toBeTruthy()
})

test("cancelling the code prompt returns to the login start without an error", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false } })} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "Send login code" }))
  await screen.findByLabelText("Login code")
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }))

  expect(await screen.findByRole("button", { name: "Send login code" })).toBeTruthy()
  expect(screen.queryByRole("alert")).toBeNull()
})

test("when the login status check fails, the app shows the error", async () => {
  render(
    <App
      backend={failingBackend({ code: "ERR_DB", category: "internal", message: "database is locked" })}
      languages={["en"]}
    />,
  )

  const alert = await screen.findByRole("alert")
  expect(alert.textContent).toContain("database is locked")
  expect(alert.textContent).toContain("ERR_DB")
})

test("logging out returns to the login screen", async () => {
  render(<App backend={memoryBackend({})} languages={["en"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "Log out" }))

  expect(await screen.findByRole("heading", { name: "Log in to Telegram" })).toBeTruthy()
  expect(screen.queryByLabelText("Logged in as Test User")).toBeNull()
})

test("Chinese: the setup form and guidance are translated", async () => {
  render(<App backend={memoryBackend({}, { auth: { configured: false } })} languages={["zh-CN"]} />)

  expect(await screen.findByRole("form", { name: "连接 Telegram" })).toBeTruthy()
  expect(screen.getByLabelText("手机号")).toBeTruthy()
  expect(screen.getByRole("link", { name: /my\.telegram\.org/ })).toBeTruthy()
  expect(screen.getByRole("button", { name: "保存并继续" })).toBeTruthy()
})

test("Chinese: the login flow is translated", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false } })} languages={["zh-CN"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "发送登录验证码" }))
  expect(await screen.findByText("请输入 Telegram 发送的登录验证码。")).toBeTruthy()
  fireEvent.change(screen.getByLabelText("登录验证码"), { target: { value: "00000" } })
  fireEvent.click(screen.getByRole("button", { name: "验证" }))
  expect(await screen.findByText("验证码错误，请重试。")).toBeTruthy()
  expect(screen.getByText("第 2 次尝试，共 3 次。")).toBeTruthy()
  fireEvent.change(screen.getByLabelText("登录验证码"), { target: { value: "12345" } })
  fireEvent.click(screen.getByRole("button", { name: "验证" }))
  expect(await screen.findByLabelText("已登录：Test User")).toBeTruthy()
})

test("Chinese: a rate limit shows how long to wait", async () => {
  render(<App backend={memoryBackend({}, { auth: { authenticated: false, rateLimitSeconds: 85286 } })} languages={["zh-CN"]} />)

  fireEvent.click(await screen.findByRole("button", { name: "发送登录验证码" }))

  const alert = await screen.findByRole("alert")
  expect(alert.textContent).toContain("请在23 小时 41 分钟后重试")
})

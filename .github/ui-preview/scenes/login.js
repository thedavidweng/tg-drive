/* Scene: first-run setup and login (issue #69).
 *
 * Contract: node login.js <base-url> <out-dir>
 * The server under test is td-gui built with `-tags gui,server`, started
 * with a sandboxed HOME (no saved credentials), TD_FAKE_TELEGRAM=1 and
 * TD_FAKE_TELEGRAM_STATE pointed at a fresh state file. See the README in
 * the parent directory.
 */
const { chromium } = require("playwright")
const path = require("node:path")

const [baseURL, outDir] = process.argv.slice(2)
if (!baseURL || !outDir) {
  console.error("usage: node login.js <base-url> <out-dir>")
  process.exit(2)
}

async function shot(page, name) {
  await page.screenshot({ path: path.join(outDir, `${name}.png`) })
  console.log("shot:", name)
}

async function main() {
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage({ viewport: { width: 1920, height: 1280 } })

    // A machine without credentials lands on the setup form.
    await page.goto(baseURL)
    await page.getByRole("form", { name: "Connect to Telegram" }).waitFor()
    await shot(page, "login-1-setup")

    // Saving credentials moves on to the login screen.
    await page.getByLabel("api_id").fill("12345")
    await page.getByLabel("api_hash").fill("0123456789abcdef0123456789abcdef")
    await page.getByLabel("Phone").fill("+15550001")
    await page.getByRole("button", { name: "Save and continue" }).click()
    await page.getByRole("heading", { name: "Log in to Telegram" }).waitFor()
    await shot(page, "login-2-start")

    // The login code arrives as a prompt the page answers.
    await page.getByRole("button", { name: "Send login code" }).click()
    await page.getByLabel("Login code").waitFor()
    await shot(page, "login-3-code")

    // The fake rejects a wrong code outright; the login screen shows the
    // service error and offers the way back.
    await page.getByLabel("Login code").fill("00000")
    await page.getByRole("button", { name: "Verify code" }).click()
    await page.getByRole("alert").waitFor()
    await shot(page, "login-4-error")
    await page.getByRole("button", { name: "Back" }).click()
    await page.getByRole("button", { name: "Send login code" }).click()

    // The right code logs in: tabs and the account chip appear.
    await page.getByLabel("Login code").fill("12345")
    await page.getByRole("button", { name: "Verify code" }).click()
    await page.getByLabel("Logged in as Test User").waitFor()
    await shot(page, "login-5-logged-in")

    // Logging out returns to the login screen.
    await page.getByRole("button", { name: "Log out" }).click()
    await page.getByRole("heading", { name: "Log in to Telegram" }).waitFor()
    await shot(page, "login-6-after-logout")
  } finally {
    await browser.close()
  }
}

main().catch((e) => {
  console.error(e)
  process.exit(1)
})

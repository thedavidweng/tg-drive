import { useEffect, useState, type FormEvent, type ReactNode } from "react"
import { Eye, EyeOff } from "lucide-react"

import type { AuthPrompt, Backend, BackendError } from "@/backend"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useI18n, type Translate } from "@/i18n"

/** The centred card the setup and login screens share. */
export function AuthCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    // app-drag: with macOS' hidden title bar the page itself is the
    // window's drag region; the form controls opt out via index.css.
    <div className="app-drag grid h-full place-items-center">
      <div className="w-full max-w-sm rounded-card border border-line bg-card p-5 shadow-seg">
        <h1 className="mb-4 text-[15px] font-semibold tracking-[-.01em]">{title}</h1>
        {children}
      </div>
    </div>
  )
}

export function ErrorAlert({ error }: { error: BackendError }) {
  return (
    <div role="alert" className="rounded-card border border-line bg-red-soft px-3.5 py-3 text-red">
      <p>{error.message}</p>
      <p className="mt-1 font-mono text-[11.5px] opacity-80">{error.code}</p>
    </div>
  )
}

const credentialsURL = "https://my.telegram.org"

export function SetupScreen({ backend, onDone }: { backend: Backend; onDone: () => void }) {
  const { t } = useI18n()
  const [apiID, setApiID] = useState("")
  const [apiHash, setApiHash] = useState("")
  const [showHash, setShowHash] = useState(false)
  const [phone, setPhone] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<BackendError | null>(null)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    backend.auth.setup(apiID, apiHash, phone).then(
      () => onDone(),
      (err: BackendError) => {
        setError(err)
        setBusy(false)
      },
    )
  }

  return (
    <AuthCard title={t("auth.setupTitle")}>
      <form aria-label={t("auth.setupTitle")} onSubmit={submit} className="flex flex-col gap-3.5">
        <p className="text-[13px] text-muted-foreground">{t("auth.setupIntro")}</p>
        {/* The webview does not follow target=_blank; the system browser
            opens the page instead. */}
        <a
          href={credentialsURL}
          onClick={(e) => {
            e.preventDefault()
            void backend.system.openURL(credentialsURL)
          }}
          className="text-[13px] text-primary underline-offset-4 hover:underline"
        >
          {t("auth.getCredentials")}
        </a>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="setup-api-id">{t("auth.apiID")}</Label>
          <Input
            id="setup-api-id"
            value={apiID}
            onChange={(e) => setApiID(e.target.value)}
            inputMode="numeric"
            autoComplete="off"
            required
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="setup-api-hash">{t("auth.apiHash")}</Label>
          <div className="relative">
            <Input
              id="setup-api-hash"
              type={showHash ? "text" : "password"}
              value={apiHash}
              onChange={(e) => setApiHash(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              required
              className="pr-8"
            />
            <button
              type="button"
              aria-label={showHash ? t("auth.hideApiHash") : t("auth.showApiHash")}
              title={showHash ? t("auth.hideApiHash") : t("auth.showApiHash")}
              aria-pressed={showHash}
              onClick={() => setShowHash((v) => !v)}
              className="absolute inset-y-0 right-1 my-auto grid size-6 place-items-center rounded-control text-ctl-fg transition-colors duration-150 ease-quiet hover:bg-pill-hover hover:text-fg"
            >
              {showHash ? <EyeOff aria-hidden className="size-3.5" /> : <Eye aria-hidden className="size-3.5" />}
            </button>
          </div>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="setup-phone">{t("auth.phone")}</Label>
          <Input
            id="setup-phone"
            value={phone}
            onChange={(e) => setPhone(e.target.value)}
            placeholder={t("auth.phoneHint")}
            autoComplete="tel"
            required
          />
        </div>
        {error && <ErrorAlert error={error} />}
        <Button type="submit" disabled={busy}>
          {t("auth.setupSubmit")}
        </Button>
      </form>
    </AuthCard>
  )
}

type LoginState =
  | { state: "start" }
  | { state: "prompt"; prompt: AuthPrompt }
  | { state: "working" }
  | { state: "failed"; error: BackendError }

export function LoginScreen({
  backend,
  hasPhone,
  onLoggedIn,
}: {
  backend: Backend
  hasPhone: boolean
  onLoggedIn: () => void
}) {
  const { t } = useI18n()
  const [state, setState] = useState<LoginState>({ state: "start" })
  const [phone, setPhone] = useState("")

  // The facade emits auth.prompt when the login flow needs a code or the
  // 2FA password; answering resumes it.
  useEffect(() => backend.auth.onPrompt((prompt) => setState({ state: "prompt", prompt })), [backend])

  const start = (forceNewCode: boolean) => {
    setState({ state: "working" })
    backend.auth.login(phone, forceNewCode).then(
      () => onLoggedIn(),
      (error: BackendError) => {
        // Cancelling a prompt is the user backing out, not a failure.
        setState(error.code === "ERR_CANCELLED" ? { state: "start" } : { state: "failed", error })
      },
    )
  }

  if (state.state === "failed") {
    return (
      <AuthCard title={t("auth.loginTitle")}>
        <div className="flex flex-col gap-3.5">
          <LoginError error={state.error} />
          <Button variant="outline" onClick={() => setState({ state: "start" })}>
            {t("auth.back")}
          </Button>
        </div>
      </AuthCard>
    )
  }

  if (state.state === "prompt") {
    return (
      <AuthCard title={t("auth.loginTitle")}>
        <PromptForm
          prompt={state.prompt}
          onAnswer={(value) => {
            const id = state.prompt.id
            setState({ state: "working" })
            backend.auth.answerPrompt(id, value).catch((error: BackendError) => setState({ state: "failed", error }))
          }}
          onCancel={() => void backend.auth.cancelPrompt(state.prompt.id)}
        />
      </AuthCard>
    )
  }

  return (
    <AuthCard title={t("auth.loginTitle")}>
      <form
        aria-label={t("auth.loginTitle")}
        onSubmit={(e: FormEvent) => {
          e.preventDefault()
          start(false)
        }}
        className="flex flex-col gap-3.5"
      >
        <p className="text-[13px] text-muted-foreground">{t("auth.loginIntro")}</p>
        {!hasPhone && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="login-phone">{t("auth.phone")}</Label>
            <Input
              id="login-phone"
              value={phone}
              onChange={(e) => setPhone(e.target.value)}
              placeholder={t("auth.phoneHint")}
              autoComplete="tel"
              required
            />
          </div>
        )}
        <Button type="submit" disabled={state.state === "working"}>
          {state.state === "working" ? t("auth.signingIn") : t("auth.loginStart")}
        </Button>
        <button
          type="button"
          disabled={state.state === "working"}
          onClick={() => start(true)}
          className="text-[12.5px] text-muted-foreground underline-offset-4 transition-colors duration-150 ease-quiet hover:text-fg hover:underline disabled:opacity-50"
        >
          {t("auth.loginFresh")}
        </button>
      </form>
    </AuthCard>
  )
}

/** The code or password form for one pending auth prompt. */
function PromptForm({
  prompt,
  onAnswer,
  onCancel,
}: {
  prompt: AuthPrompt
  onAnswer: (value: string) => void
  onCancel: () => void
}) {
  const { t } = useI18n()
  const [value, setValue] = useState("")
  const isCode = prompt.kind === "code"

  // A code prompt can carry several facts at once: a retry, which code is
  // in play (resent or reused), and the attempt budget. Each gets a line.
  const hints: string[] = []
  if (isCode) {
    if (prompt.attempt > 1) hints.push(t("auth.codeInvalid"))
    if (prompt.resent) hints.push(t("auth.codeResent"))
    else if (prompt.reused) hints.push(t("auth.codeReused"))
    else if (prompt.attempt <= 1) hints.push(t("auth.codeSent"))
    if (prompt.max_attempts > 0) hints.push(t("auth.codeAttempt", { attempt: prompt.attempt, max: prompt.max_attempts }))
  } else {
    hints.push(prompt.attempt > 1 ? t("auth.passwordRetry") : t("auth.passwordPrompt"))
  }

  return (
    <form
      onSubmit={(e: FormEvent) => {
        e.preventDefault()
        onAnswer(value)
      }}
      className="flex flex-col gap-3.5"
    >
      <div className="flex flex-col gap-1">
        {hints.map((hint) => (
          <p key={hint} className="text-[13px] text-muted-foreground">
            {hint}
          </p>
        ))}
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`prompt-${prompt.kind}`}>{isCode ? t("auth.codeLabel") : t("auth.passwordLabel")}</Label>
        <Input
          id={`prompt-${prompt.kind}`}
          type={isCode ? "text" : "password"}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          inputMode={isCode ? "numeric" : undefined}
          autoComplete={isCode ? "one-time-code" : "current-password"}
          autoFocus
          required
        />
      </div>
      <div className="flex gap-2">
        <Button type="submit" className="flex-1">
          {isCode ? t("auth.codeSubmit") : t("auth.passwordSubmit")}
        </Button>
        <Button type="button" variant="outline" onClick={onCancel}>
          {t("auth.cancel")}
        </Button>
      </div>
    </form>
  )
}

/** A login failure; a rate limit leads with how long to wait. */
function LoginError({ error }: { error: BackendError }) {
  const { t } = useI18n()
  const wait = error.details?.retry_after_seconds
  if (error.code === "ERR_TELEGRAM_RATE_LIMITED" && typeof wait === "number") {
    return (
      <div role="alert" className="rounded-card border border-line bg-amber-soft px-3.5 py-3 text-amber">
        <p>{t("auth.rateLimited", { wait: formatWait(wait, t) })}</p>
        <p className="mt-1 font-mono text-[11.5px] opacity-80">{error.code}</p>
      </div>
    )
  }
  return <ErrorAlert error={error} />
}

function formatWait(seconds: number, t: Translate): string {
  if (seconds >= 3600) {
    return t("duration.hm", { h: Math.floor(seconds / 3600), m: Math.round((seconds % 3600) / 60) })
  }
  if (seconds >= 60) {
    return t("duration.ms", { m: Math.floor(seconds / 60), s: seconds % 60 })
  }
  return t("duration.s", { s: seconds })
}

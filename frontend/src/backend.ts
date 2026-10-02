import type {
  AuthPrompt,
  AuthStatus,
  AuthUser,
  BindChoices,
  BindRequest,
  BindResult,
  ChannelChoice,
  ChannelInfo,
  ChannelStatus,
  ConfigEntry,
  DeleteOutcome,
  DirectoryChanged,
  DiscussionLink,
  Entry,
  LoginResult,
  OmarchyState,
  OmarchyTheme,
  ScanOutcome,
  ScanProgress,
  ShareLink,
  TreeNode,
  Versions,
} from "../bindings/github.com/thedavidweng/tg-drive-cli/internal/gui/models"

export type {
  AuthPrompt,
  AuthStatus,
  AuthUser,
  BindChoices,
  BindRequest,
  BindResult,
  ChannelChoice,
  ChannelInfo,
  ChannelStatus,
  ConfigEntry,
  DeleteOutcome,
  DirectoryChanged,
  DiscussionLink,
  Entry,
  LoginResult,
  OmarchyState,
  OmarchyTheme,
  ScanOutcome,
  ScanProgress,
  ShareLink,
  TreeNode,
  Versions,
}

/**
 * The error every facade call rejects with: the code and category of the
 * JSON contract's error envelope, plus its machine-readable details (for
 * example retry_after_seconds on ERR_TELEGRAM_RATE_LIMITED).
 */
export interface BackendError {
  code: string
  category: string
  message: string
  details?: Record<string, unknown>
}

/**
 * Everything the screens need from Go. The app uses the generated Wails
 * bindings (wails-backend.ts); tests pass an in-memory backend.
 */
export interface Backend {
  drive: {
    list(path: string): Promise<Entry[]>
    tree(path: string, maxDepth: number): Promise<TreeNode[]>
    mkdir(path: string): Promise<void>
    move(from: string, to: string, opts: { confirm: boolean }): Promise<void>
    delete(path: string, opts: { confirm: boolean }): Promise<DeleteOutcome>
    share(path: string): Promise<ShareLink>
    scan(): Promise<ScanOutcome>
  }
  events: {
    onDirectoryChanged(cb: (e: DirectoryChanged) => void): () => void
    onScanProgress(cb: (e: ScanProgress) => void): () => void
  }
  channels: {
    /** The channels bound in the shared index; one is marked active. */
    list(): Promise<ChannelInfo[]>
    /** The active channel's status; rejects ERR_CHANNEL_NOT_FOUND when none is bound. */
    status(): Promise<ChannelStatus>
    /** The Telegram channels offered for binding, plus the default title for a created one. */
    choices(): Promise<BindChoices>
    /** Binds or creates a channel and makes it the active drive. */
    bind(req: BindRequest): Promise<BindResult>
    /** Switches the active drive to a bound channel. */
    select(channelID: string): Promise<ChannelStatus>
    /** Links a discussion group to the active channel (ADR 0018). */
    linkDiscussion(): Promise<DiscussionLink>
  }
  auth: {
    status(): Promise<AuthStatus>
    setup(apiID: string, apiHash: string, phone: string): Promise<AuthStatus>
    login(phone: string, forceNewCode: boolean): Promise<LoginResult>
    logout(): Promise<void>
    answerPrompt(id: string, value: string): Promise<void>
    cancelPrompt(id: string): Promise<void>
    /** Subscribes to the typed auth.prompt event; returns an unsubscribe. */
    onPrompt(cb: (prompt: AuthPrompt) => void): () => void
  }
  settings: {
    /** Every config key in file order, secrets redacted. */
    listConfig(): Promise<ConfigEntry[]>
    /** One secret's real value; confirmed is the user's reveal click. */
    revealSecret(key: string, confirmed: boolean): Promise<ConfigEntry>
    /** Validate and save one key; resolves to the entry as it now displays. */
    setConfig(key: string, value: string): Promise<ConfigEntry>
    versions(): Promise<Versions>
    omarchy(): Promise<OmarchyState>
    /** Subscribe to omarchy:theme-changed; returns an unsubscribe. */
    onOmarchyTheme(cb: (theme: OmarchyTheme) => void): () => void
  }
}

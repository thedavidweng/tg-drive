import type { ComponentType } from "react"

import type { PreviewDescriptor } from "@/backend"

/** What every preview view receives from the preview surface. */
export interface PreviewViewProps {
  /** The prepared file: display metadata, capabilities, and the media URL. */
  descriptor: PreviewDescriptor
  /**
   * The view cannot show this file after all (a decode or load error, an
   * unsupported codec). The surface replaces the view with the fallback,
   * which keeps Download; message is an optional user-facing reason.
   */
  onError: (message?: string) => void
  /** Starts the same Download the header offers (for a view's own button). */
  onDownload: () => void
}

/**
 * One preview provider: which files it handles and the view that shows
 * them. The registry tries every provider's MIME types before any
 * provider's extensions, so the indexed MIME type outranks a misleading
 * extension.
 *
 * A view that handles Escape itself (closing its own search box, leaving
 * its own fullscreen) calls preventDefault on that keydown, and the surface
 * then leaves the preview open.
 */
export interface PreviewProvider {
  /** Stable identifier, for tests and diagnostics. */
  id: string
  /**
   * MIME types matched against the descriptor's type without parameters:
   * exact ("image/png") or a whole top-level type ("video/*").
   */
  mimeTypes?: readonly string[]
  /** Lower-case file extensions without the dot ("png"). */
  extensions?: readonly string[]
  /** Further eligibility beyond the match (a size or capability check). */
  canPreview?: (descriptor: PreviewDescriptor) => boolean
  View: ComponentType<PreviewViewProps>
}

import { imagePreview } from "@/preview/image"
import type { PreviewProvider } from "@/preview/types"

/**
 * Every preview provider, in priority order. Each provider lives in its own
 * module under src/preview/ and registers with one import and one line here;
 * the fallback is not listed, the registry always ends with it.
 */
export const previewProviders: readonly PreviewProvider[] = [
  imagePreview,
]

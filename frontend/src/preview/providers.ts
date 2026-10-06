import { audioPreview } from "@/preview/audio"
import { docxPreview } from "@/preview/docx"
import { imagePreview } from "@/preview/image"
import { markdownPreview } from "@/preview/markdown"
import { pdfPreview } from "@/preview/pdf"
import { pptxPreview } from "@/preview/pptx"
import { textPreview } from "@/preview/text"
import type { PreviewProvider } from "@/preview/types"
import { videoPreview } from "@/preview/video"
import { xlsxPreview } from "@/preview/xlsx"

/**
 * Every preview provider, in priority order. Each provider lives in its own
 * module under src/preview/ and registers with one import and one line here;
 * the fallback is not listed, the registry always ends with it.
 */
export const previewProviders: readonly PreviewProvider[] = [
  imagePreview,
  videoPreview,
  audioPreview,
  pdfPreview,
  docxPreview,
  xlsxPreview,
  pptxPreview,
  markdownPreview,
  // Last: its text/* catch-all must not shadow a more specific provider.
  textPreview,
]

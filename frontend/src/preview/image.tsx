import type { PreviewProvider, PreviewViewProps } from "@/preview/types"

// SVG goes through <img> too: an SVG image document runs no script and
// loads no external resources, unlike an inline or framed SVG.
function ImagePreview({ descriptor, onError }: PreviewViewProps) {
  return (
    <div className="flex h-full min-h-0 items-center justify-center p-4">
      <img
        src={descriptor.url}
        alt={descriptor.name}
        decoding="async"
        draggable={false}
        onError={() => onError()}
        className="max-h-full max-w-full object-contain"
      />
    </div>
  )
}

export const imagePreview: PreviewProvider = {
  id: "image",
  mimeTypes: ["image/jpeg", "image/png", "image/gif", "image/webp", "image/svg+xml"],
  extensions: ["jpg", "jpeg", "jfif", "png", "gif", "webp", "svg"],
  View: ImagePreview,
}

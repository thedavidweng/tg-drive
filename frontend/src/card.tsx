import type { ReactNode } from "react"

/** The labelled settings-style card the tab screens compose from. */
export function Card({ label, children }: { label: string; children: ReactNode }) {
  return (
    <section aria-label={label}>
      <h2 className="px-1 pb-1.5 text-[11.5px] font-medium tracking-[.04em] text-muted-foreground uppercase">
        {label}
      </h2>
      <ul className="divide-y divide-line-2 overflow-hidden rounded-card border border-line bg-card">
        {children}
      </ul>
    </section>
  )
}

export function Row({ children }: { children: ReactNode }) {
  return <li className="flex min-h-11 items-center gap-3 px-3.5 py-2">{children}</li>
}

/** The iOS-style segmented control for choosing one of a few options. */
export function Segmented<T extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string
  options: readonly { value: T; name: string }[]
  value: T
  onChange: (value: T) => void
}) {
  return (
    <div role="group" aria-label={label} className="flex flex-wrap rounded-seg bg-seg-track p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={value === o.value}
          onClick={() => onChange(o.value)}
          className="h-6 rounded-control px-3 text-[12.5px] text-ctl-fg transition-colors duration-150 ease-quiet hover:text-fg-2 aria-pressed:bg-seg-thumb aria-pressed:text-fg aria-pressed:shadow-seg"
        >
          {o.name}
        </button>
      ))}
    </div>
  )
}

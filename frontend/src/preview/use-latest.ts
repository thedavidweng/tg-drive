import { useEffect, useRef, type RefObject } from "react"

/**
 * A ref that always holds the latest value. The surface passes fresh
 * callbacks on every render; an effect that reads them through this ref
 * does not restart for each one.
 */
export function useLatest<T>(value: T): Readonly<RefObject<T>> {
  const ref = useRef(value)
  useEffect(() => {
    ref.current = value
  })
  return ref
}

import { useEffect, useRef, useState } from 'preact/hooks'
import { toast as toastBus, type ToastEvent } from '../toast'

const DEFAULT_DURATION = 2800

export function Toaster() {
  const [toasts, setToasts] = useState<ToastEvent[]>([])
  const timersRef = useRef<Record<string, number>>({})

  const dismissToast = (id: string) => {
    const timer = timersRef.current[id]
    if (timer) {
      window.clearTimeout(timer)
      delete timersRef.current[id]
    }
    setToasts(prev => prev.filter(t => t.id !== id))
  }

  const scheduleDismiss = (toast: ToastEvent) => {
    const durationMs = toast.durationMs ?? (toast.kind === 'error' ? 5000 : DEFAULT_DURATION)
    timersRef.current[toast.id] = window.setTimeout(() => {
      dismissToast(toast.id)
    }, durationMs)
  }

  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent<ToastEvent>).detail
      setToasts(prev => [...prev, detail])
      scheduleDismiss(detail)
    }
    window.addEventListener(toastBus.EVENT, handler)
    return () => {
      window.removeEventListener(toastBus.EVENT, handler)
      Object.values(timersRef.current).forEach(timer => window.clearTimeout(timer))
      timersRef.current = {}
    }
  }, [])

  if (!toasts.length) return null

  return (
    <div class="toast-container" aria-live="polite" aria-atomic="true">
      {toasts.map(t => (
        <div
          key={t.id}
          class={`toast toast-${t.kind}`}
          role={t.kind === 'error' ? 'alert' : 'status'}
        >
          {t.kind === 'success' && (
            <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="8" cy="8" r="6.5" /><path d="M5.5 8l2 2 3-3.5" />
            </svg>
          )}
          {t.kind === 'error' && (
            <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="8" cy="8" r="6.5" /><line x1="8" y1="5" x2="8" y2="8.5" /><circle cx="8" cy="11" r="0.5" fill="currentColor" />
            </svg>
          )}
          {t.kind === 'info' && (
            <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="8" cy="8" r="6.5" /><line x1="8" y1="7" x2="8" y2="11" /><circle cx="8" cy="5" r="0.5" fill="currentColor" />
            </svg>
          )}
          <span>{t.message}</span>
          <button
            type="button"
            class="toast-dismiss"
            onClick={() => dismissToast(t.id)}
            aria-label={t.dismissLabel ?? 'Dismiss notification'}
            title={t.dismissLabel ?? 'Dismiss'}
          >
            ×
          </button>
        </div>
      ))}
    </div>
  )
}

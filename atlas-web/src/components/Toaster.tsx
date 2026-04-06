import { useState, useEffect } from 'preact/hooks'
import { toast as toastBus, type ToastEvent } from '../toast'

const DURATION = 2800   // ms before a toast auto-dismisses

export function Toaster() {
  const [toasts, setToasts] = useState<ToastEvent[]>([])

  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent<ToastEvent>).detail
      setToasts(prev => [...prev, detail])
      setTimeout(() => {
        setToasts(prev => prev.filter(t => t.id !== detail.id))
      }, DURATION)
    }
    window.addEventListener(toastBus.EVENT, handler)
    return () => window.removeEventListener(toastBus.EVENT, handler)
  }, [])

  if (!toasts.length) return null

  return (
    <div class="toast-container">
      {toasts.map(t => (
        <div key={t.id} class={`toast toast-${t.kind}`}>
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
        </div>
      ))}
    </div>
  )
}

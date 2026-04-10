/**
 * Minimal global toast bus.
 * Any screen can call `toast.show(...)` without prop-drilling.
 * The Toaster component in App.tsx subscribes to these events and renders them.
 */

export type ToastKind = 'success' | 'error' | 'info'

export interface ToastEvent {
  id:         string
  message:    string
  kind:       ToastKind
  durationMs?: number
  actionLabel?: string
  dismissLabel?: string
}

const EVENT = 'atlas:toast'

function uid(): string {
  return Math.random().toString(36).slice(2)
}

type ToastOptions = Omit<ToastEvent, 'id' | 'message' | 'kind'>

function emit(message: string, kind: ToastKind, options: ToastOptions = {}) {
  const detail: ToastEvent = { id: uid(), message, kind, ...options }
  window.dispatchEvent(new CustomEvent<ToastEvent>(EVENT, { detail }))
}

export const toast = {
  success: (message: string, options?: ToastOptions) => emit(message, 'success', options),
  error:   (message: string, options?: ToastOptions) => emit(message, 'error', options),
  info:    (message: string, options?: ToastOptions) => emit(message, 'info', options),
  EVENT,
}

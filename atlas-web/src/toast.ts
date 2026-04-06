/**
 * Minimal global toast bus.
 * Any screen can call `toast.show(...)` without prop-drilling.
 * The Toaster component in App.tsx subscribes to these events and renders them.
 */

export type ToastKind = 'success' | 'error' | 'info'

export interface ToastEvent {
  id:      string
  message: string
  kind:    ToastKind
}

const EVENT = 'atlas:toast'

function uid(): string {
  return Math.random().toString(36).slice(2)
}

function emit(message: string, kind: ToastKind) {
  const detail: ToastEvent = { id: uid(), message, kind }
  window.dispatchEvent(new CustomEvent<ToastEvent>(EVENT, { detail }))
}

export const toast = {
  success: (message: string) => emit(message, 'success'),
  error:   (message: string) => emit(message, 'error'),
  info:    (message: string) => emit(message, 'info'),
  EVENT,
}

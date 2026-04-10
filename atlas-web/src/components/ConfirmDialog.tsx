import { createPortal } from 'preact/compat'
import type { VNode } from 'preact'

interface ConfirmDialogProps {
  title: string
  body?: string
  confirmLabel?: string
  cancelLabel?: string
  danger?: boolean
  onConfirm: () => void
  onCancel: () => void
}

const WarningIcon = () => (
  <svg width="26" height="26" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
    <path d="M12 9v4M12 17h.01" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
    <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
  </svg>
)

export function ConfirmDialog({
  title,
  body,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  danger = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const main = document.querySelector('main')
  if (!main) return null

  return createPortal(
    <div class="confirm-dialog-overlay" onClick={e => { if (e.target === e.currentTarget) onCancel() }}>
      <div class="confirm-dialog-card">
        <div class={`confirm-dialog-glyph${danger ? ' confirm-dialog-glyph-danger' : ''}`}>
          <WarningIcon />
        </div>
        <div class="confirm-dialog-title">{title}</div>
        {body && <div class="confirm-dialog-body">{body}</div>}
        <div class="confirm-dialog-actions">
          <button class="btn" onClick={onCancel}>{cancelLabel}</button>
          <button class={`btn ${danger ? 'btn-danger' : 'btn-primary'}`} onClick={onConfirm}>{confirmLabel}</button>
        </div>
      </div>
    </div>,
    main,
  ) as unknown as VNode
}

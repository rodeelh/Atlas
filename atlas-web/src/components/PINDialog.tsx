import { createPortal } from 'preact/compat'
import { useEffect, useRef, useState } from 'preact/hooks'
import type { VNode } from 'preact'

interface PINDialogProps {
  /** true = changing an existing PIN, false = setting a new one */
  isChange: boolean
  onSave: (pin: string) => Promise<void>
  onCancel: () => void
}

const LockIcon = () => (
  <svg width="26" height="26" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="3" y="11" width="18" height="11" rx="2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
    <path d="M7 11V7a5 5 0 0 1 10 0v4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
  </svg>
)

export function PINDialog({ isChange, onSave, onCancel }: PINDialogProps) {
  const [pin, setPin] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const cardRef = useRef<HTMLDivElement>(null)
  const firstInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    firstInputRef.current?.focus()

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !saving) {
        e.preventDefault()
        onCancel()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onCancel, saving])

  const handleSave = async () => {
    if (pin.length < 6) { setError('PIN must be at least 6 characters'); return }
    if (pin !== confirm) { setError('PINs do not match'); return }
    setError(null)
    setSaving(true)
    try {
      await onSave(pin)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save PIN')
      setSaving(false)
    }
  }

  const main = document.querySelector('main')
  if (!main) return null

  return createPortal(
    <div
      class="confirm-dialog-overlay"
      onClick={e => { if (e.target === e.currentTarget && !saving) onCancel() }}
    >
      <div
        class="confirm-dialog-card"
        ref={cardRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="pin-dialog-title"
      >
        <div class="confirm-dialog-glyph">
          <LockIcon />
        </div>
        <div id="pin-dialog-title" class="confirm-dialog-title">
          {isChange ? 'Change PIN' : 'Set PIN'}
        </div>
        <div class="confirm-dialog-body" style={{ width: '100%' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '10px', marginTop: '4px' }}>
            <input
              ref={firstInputRef}
              class="input"
              type="password"
              placeholder={isChange ? 'New PIN' : 'PIN (min 6 characters)'}
              value={pin}
              onInput={e => { setPin((e.target as HTMLInputElement).value); setError(null) }}
              onKeyDown={e => e.key === 'Enter' && document.getElementById('pin-dialog-confirm-input')?.focus()}
              disabled={saving}
              autocomplete="new-password"
            />
            <input
              id="pin-dialog-confirm-input"
              class="input"
              type="password"
              placeholder="Confirm PIN"
              value={confirm}
              onInput={e => { setConfirm((e.target as HTMLInputElement).value); setError(null) }}
              onKeyDown={e => e.key === 'Enter' && handleSave()}
              disabled={saving}
              autocomplete="new-password"
            />
            {error && (
              <span style={{ fontSize: '12px', color: 'var(--danger, #ff453a)', textAlign: 'left' }}>
                {error}
              </span>
            )}
          </div>
        </div>
        <div class="confirm-dialog-actions">
          <button class="btn" onClick={onCancel} disabled={saving}>Cancel</button>
          <button class="btn btn-primary" onClick={handleSave} disabled={saving}>
            {saving ? 'Saving…' : 'Save PIN'}
          </button>
        </div>
      </div>
    </div>,
    main,
  ) as unknown as VNode
}

import { createPortal } from 'preact/compat'
import { useEffect, useRef, useState } from 'preact/hooks'
import { api, type RuntimeConfig } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { PageSpinner } from '../components/PageSpinner'
import { PINDialog } from '../components/PINDialog'
import { toast } from '../toast'
import { ErrorBanner } from '../components/ErrorBanner'
import type { RuntimeConfigUpdateResponse, StorageStats } from '../api/client'

type RestartPhase = 'confirm' | 'restarting' | 'done'

export function Settings() {
  const [config, setConfig] = useState<RuntimeConfig | null>(null)
  const [draft, setDraft] = useState<RuntimeConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [restartRequired, setRestartRequired] = useState(false)

  const [location, setLocation] = useState<{ city: string; country: string; timezone: string; source: string } | null>(null)
  const [locationEdit, setLocationEdit] = useState('')
  const [locationSaving, setLocationSaving] = useState(false)
  const [locationError, setLocationError] = useState<string | null>(null)
  const [prefs, setPrefs] = useState<{ temperatureUnit: string; currency: string; unitSystem: string } | null>(null)
  const [restartPhase, setRestartPhase] = useState<RestartPhase | null>(null)
  const [restartStatus, setRestartStatus] = useState('Restarting Atlas…')
  const [storageStats, setStorageStats] = useState<StorageStats | null>(null)
  const [storageCleaning, setStorageCleaning] = useState(false)
  const [locking, setLocking] = useState(false)

  const canRestartLocally = typeof window !== 'undefined' &&
    (window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1')

  const lockAtlas = async () => {
    setLocking(true)
    try {
      await api.localAuthLogout()
      window.location.reload()
    } catch {
      toast.error('Failed to lock Atlas.')
      setLocking(false)
    }
  }

  useEffect(() => {
    const init = async () => {
      try {
        const c = await api.config()
        setConfig(c)
        setDraft(c)
        api.location()
          .then((loc) => {
            setLocation(loc)
            setLocationEdit(loc.city ? loc.city + (loc.country ? ', ' + loc.country : '') : '')
          })
          .catch(() => {})
        api.preferences().then(setPrefs).catch(() => {})
        api.getStorageStats().then(setStorageStats).catch(() => {})
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load config.')
      } finally {
        setLoading(false)
      }
    }
    void init()
  }, [])

  const update = <K extends keyof RuntimeConfig>(key: K, value: RuntimeConfig[K]) => {
    setDraft((prev) => (prev ? { ...prev, [key]: value } : prev))
  }

  const save = async () => {
    if (!draft) return
    setSaving(true)
    setError(null)
    try {
      const result: RuntimeConfigUpdateResponse = await api.updateConfig(draft)
      setConfig(result.config)
      setDraft(result.config)
      toast.success('Changes saved.')
      setRestartRequired(result.restartRequired)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save config.')
    } finally {
      setSaving(false)
    }
  }

  const restartAtlas = () => setRestartPhase('confirm')

  const confirmRestart = async () => {
    setRestartPhase('restarting')
    setRestartStatus('Restarting Atlas…')
    setError(null)
    setRestartRequired(false)
    try {
      await api.restartAtlas()
      setRestartStatus('Reconnecting…')
      const recovered = await waitForAtlasRestart()
      if (!recovered) throw new Error('Atlas did not come back online in time.')
      setRestartPhase('done')
      window.setTimeout(() => setRestartPhase(null), 2500)
    } catch (err) {
      setRestartPhase(null)
      setError(err instanceof Error ? err.message : 'Failed to restart Atlas.')
    }
  }

  const isDirty = (() => {
    if (!config || !draft) return false
    const keys = Object.keys(config) as (keyof RuntimeConfig)[]
    return keys.some((k) => config[k] !== draft[k]) || (Object.keys(draft) as (keyof RuntimeConfig)[]).some((k) => !(k in config))
  })()

  if (loading) {
    return (
      <div class="screen general-settings-screen">
        <PageHeader title="General" subtitle="Profile and access preferences" />
        <PageSpinner />
      </div>
    )
  }

  if (!draft) {
    return (
      <div class="screen general-settings-screen">
        <PageHeader title="General" subtitle="Profile and access preferences" />
        <ErrorBanner error={error} />
      </div>
    )
  }

  return (
    <div class="screen general-settings-screen">
      <PageHeader
        title="General"
        subtitle="Profile and access preferences"
        actions={
          <button class="btn btn-primary btn-sm" onClick={save} disabled={saving || !isDirty}>
            {saving ? (
              <>
                <span class="spinner spinner-sm" style={{ borderTopColor: '#000', borderColor: 'rgba(0,0,0,0.2)' }} /> Saving…
              </>
            ) : (
              'Save changes'
            )}
          </button>
        }
      />

      {restartPhase && createPortal(
        <RestartOverlay
          phase={restartPhase}
          status={restartStatus}
          onConfirm={() => void confirmRestart()}
          onCancel={() => setRestartPhase(null)}
        />,
        document.body
      )}
      <ErrorBanner error={error} onDismiss={() => setError(null)} />
      {restartRequired && (
        <div
          class="banner"
          style={{
            background: 'color-mix(in srgb, var(--yellow, #f59e0b) 15%, transparent)',
            borderColor: 'color-mix(in srgb, var(--yellow, #f59e0b) 40%, transparent)',
            color: 'var(--text)',
          }}
        >
          <strong>Restart required</strong> — Port change saved. Restart the Atlas daemon for it to take effect.
        </div>
      )}

      <SettingsGroup title="Profile">
        <SettingsRow label="Your name" sublabel="How Atlas addresses you in conversation">
          <input class="input" placeholder="e.g. Rami" value={draft.userName ?? ''} onInput={(e) => update('userName', (e.target as HTMLInputElement).value)} />
        </SettingsRow>
        <SettingsRow label="Assistant name" sublabel="How Atlas identifies itself in conversation">
          <input class="input" value={draft.personaName} onInput={(e) => update('personaName', (e.target as HTMLInputElement).value)} />
        </SettingsRow>
        <SettingsRow label="Location" sublabel="Leave blank to auto-detect from IP">
          <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
            <div style={{ position: 'relative', width: '240px', maxWidth: '100%' }}>
              <input
                class="input"
                placeholder={locationSaving ? 'Detecting…' : 'City, Country'}
                value={locationEdit}
                disabled={locationSaving}
                style={{ paddingRight: '34px', width: '100%', boxSizing: 'border-box' }}
                onInput={(e) => setLocationEdit((e.target as HTMLInputElement).value)}
                onBlur={async (e) => {
                  const val = e.currentTarget.value.trim()
                  setLocationError(null)
                  setLocationSaving(true)
                  try {
                    if (!val) {
                      const loc = await api.detectLocation()
                      setLocation(loc)
                      setLocationEdit(loc.city ? loc.city + (loc.country ? ', ' + loc.country : '') : '')
                    } else {
                      const parts = val.split(',').map((s: string) => s.trim())
                      const city = parts[0] ?? ''
                      const country = parts.slice(1).join(', ').trim()
                      const loc = await api.setLocation(city, country)
                      setLocation(loc)
                    }
                  } catch (err) {
                    setLocationError(err instanceof Error ? err.message : 'Failed')
                  } finally {
                    setLocationSaving(false)
                  }
                }}
              />
              <button
                class="chat-copy-btn"
                title="Detect my location"
                disabled={locationSaving}
                style={{ position: 'absolute', right: '6px', top: '50%', transform: 'translateY(-50%)', opacity: 1, pointerEvents: 'auto' }}
                onClick={() => {
                  setLocationError(null)
                  setLocationSaving(true)
                  const finish = async (loc: { city: string; country: string; timezone: string; source: string; updatedAt: string }) => {
                    setLocation(loc)
                    setLocationEdit(loc.city ? loc.city + (loc.country ? ', ' + loc.country : '') : '')
                  }
                  if (navigator.geolocation) {
                    navigator.geolocation.getCurrentPosition(
                      async (pos) => {
                        try {
                          const loc = await api.setLocationFromCoords(pos.coords.latitude, pos.coords.longitude)
                          await finish(loc)
                        } catch (err) {
                          setLocationError(err instanceof Error ? err.message : 'Failed')
                        } finally {
                          setLocationSaving(false)
                        }
                      },
                      async (geoErr) => {
                        // Permission denied or unavailable — fall back to IP detection
                        try {
                          const loc = await api.detectLocation()
                          await finish(loc)
                        } catch (err) {
                          setLocationError(geoErr.message || (err instanceof Error ? err.message : 'Failed'))
                        } finally {
                          setLocationSaving(false)
                        }
                      },
                      { enableHighAccuracy: true, timeout: 15000, maximumAge: 0 }
                    )
                  } else {
                    api.detectLocation().then(finish).catch((err) => {
                      setLocationError(err instanceof Error ? err.message : 'Failed')
                    }).finally(() => setLocationSaving(false))
                  }
                }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="12" cy="12" r="3"/>
                  <path d="M12 2v3M12 19v3M2 12h3M19 12h3"/>
                  <path d="M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8z" style={{ display: 'none' }}/>
                </svg>
              </button>
            </div>
            {locationError && <div style={{ fontSize: '12px', color: 'var(--theme-text-danger, #e05252)' }}>{locationError}</div>}
          </div>
        </SettingsRow>
        {prefs && (
          <SettingsRow label="Units" sublabel="Sets measurement system and temperature scale">
            <select
              class="input"
              value={prefs.unitSystem}
              onChange={async (e) => {
                const v = (e.target as HTMLSelectElement).value
                const tempUnit = v === 'imperial' ? 'fahrenheit' : 'celsius'
                setPrefs((p) => (p ? { ...p, unitSystem: v, temperatureUnit: tempUnit } : p))
                await api.setPreferences({ unitSystem: v, temperatureUnit: tempUnit }).catch(() => {})
              }}
            >
              <option value="metric">Metric (km, km/h, °C)</option>
              <option value="imperial">Imperial (mi, mph, °F)</option>
            </select>
          </SettingsRow>
        )}
      </SettingsGroup>

      <SettingsGroup title="Behavior">
        <SettingsRow label="Action safety" sublabel="When Atlas should stop and ask before taking action">
          <select
            class="input"
            value={draft.actionSafetyMode}
            onChange={(e) => update('actionSafetyMode', (e.target as HTMLSelectElement).value)}
          >
            <option value="always_ask_before_actions">Ask every time</option>
            <option value="ask_only_for_risky_actions">Ask for risky actions</option>
            <option value="more_autonomous">Auto-approve actions</option>
          </select>
        </SettingsRow>
        <SettingsRow label="Memory" sublabel="Extract and persist facts from conversations" mobileSplit>
          <ToggleField checked={draft.memoryEnabled} onChange={(v) => update('memoryEnabled', v)} />
        </SettingsRow>
        {draft.memoryEnabled && <SettingsRow label="Memories per turn" sublabel="How many recalled facts are injected as context per request" hint="Higher values give Atlas more long-term context but use more of the model's token budget.">
          <select class="input" value={draft.maxRetrievedMemoriesPerTurn} onChange={(e) => update('maxRetrievedMemoriesPerTurn', Number((e.target as HTMLSelectElement).value))}>
            <option value={0}>0 — disabled</option>
            <option value={2}>2 — minimal</option>
            <option value={4}>4 — default</option>
            <option value={6}>6 — more context</option>
            <option value={10}>10 — maximum</option>
          </select>
        </SettingsRow>}
      </SettingsGroup>

      <SettingsGroup title="Local Access">
        <LocalAccessSection />
      </SettingsGroup>

      <SettingsGroup title="Remote Access">
        <RemoteAccessSection
          enabled={draft.remoteAccessEnabled}
          tailscaleEnabled={draft.tailscaleEnabled}
          onToggle={async (v) => {
            update('remoteAccessEnabled', v)
            try {
              const result = await api.updateConfig({ ...(draft ?? config ?? {}), remoteAccessEnabled: v })
              setConfig(result.config)
              setDraft(result.config)
              setRestartRequired(result.restartRequired)
              toast.success('Changes saved.')
            } catch (err) {
              update('remoteAccessEnabled', !v)
              setError(err instanceof Error ? err.message : 'Failed to update remote access.')
            }
          }}
          onTailscaleToggle={async (v) => {
            update('tailscaleEnabled', v)
            try {
              const result = await api.updateConfig({ ...(draft ?? config ?? {}), tailscaleEnabled: v })
              setConfig(result.config)
              setDraft(result.config)
              setRestartRequired(result.restartRequired)
              toast.success('Changes saved.')
            } catch (err) {
              update('tailscaleEnabled', !v)
              setError(err instanceof Error ? err.message : 'Failed to update Tailscale setting.')
            }
          }}
        />
      </SettingsGroup>

      <SettingsGroup title="Local Storage">
        <SettingsRow label="Files folder" sublabel={storageStats?.dir ? `Location: ${storageStats.dir}` : 'Default location for generated, received, and sent files'}>
          <button
            class="btn btn-sm btn-icon"
            title="Open in Finder"
            onClick={async () => { await api.openStorageFolder().catch(() => {}) }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style={{ display: 'block' }}>
              <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
            </svg>
          </button>
        </SettingsRow>
        <SettingsRow
          label="Storage used"
          sublabel={storageStats
            ? `Files Atlas has generated or received in this session · ${storageStats.fileCount} file${storageStats.fileCount === 1 ? '' : 's'} · ${formatBytes(storageStats.totalSize)}`
            : 'Files Atlas has generated or received in this session'}
        >
          <button
              class="btn btn-sm btn-icon btn-danger"
              title="Clear all"
              disabled={storageCleaning || !storageStats || storageStats.fileCount === 0}
              onClick={async () => {
                if (!storageStats || storageStats.fileCount === 0) return
                setStorageCleaning(true)
                try {
                  await api.clearStorageFiles()
                  const stats = await api.getStorageStats()
                  setStorageStats(stats)
                  toast.success('Storage cleared.')
                } catch {
                  toast.error('Failed to clear storage.')
                } finally {
                  setStorageCleaning(false)
                }
              }}
            >
              {storageCleaning ? (
                <span class="spinner spinner-sm" style={{ borderTopColor: 'currentColor', borderColor: 'rgba(255,255,255,0.2)' }} />
              ) : (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style={{ display: 'block' }}>
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                  <path d="M10 11v6M14 11v6"/>
                  <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>
                </svg>
              )}
            </button>
        </SettingsRow>
      </SettingsGroup>

      <SettingsGroup title="System">
        <SettingsRow label="Runtime port" sublabel="Port the Atlas daemon listens on. Requires restart to take effect." hint={'Default: 1984\nChange this if another service is using port 1984.'}>
          <input
            class="input"
            type="number"
            min="1024"
            max="65535"
            style={{ width: '80px', textAlign: 'center' }}
            value={draft.runtimePort}
            onInput={(e) => update('runtimePort', parseInt((e.target as HTMLInputElement).value, 10) || draft.runtimePort)}
          />
        </SettingsRow>
        <SettingsRow
          label="Lock Atlas"
          sublabel="End your current session and return to the sign-in screen"
          mobileSplit
        >
          <button class="btn btn-sm" style={{ width: '80px' }} onClick={lockAtlas} disabled={locking}>
            {locking ? 'Locking…' : 'Lock'}
          </button>
        </SettingsRow>
        <SettingsRow
          label="Restart Atlas"
          sublabel={canRestartLocally
            ? 'Gracefully restart the Atlas daemon and reconnect this page automatically.'
            : 'Restart is only available from a local Atlas session on this Mac.'}
          mobileSplit
        >
          <button class="btn btn-sm" style={{ width: '80px' }} onClick={restartAtlas} disabled={restartPhase === 'restarting' || !canRestartLocally}>
            {restartPhase === 'restarting' ? 'Restarting…' : 'Restart'}
          </button>
        </SettingsRow>
      </SettingsGroup>
    </div>
  )
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

async function waitForAtlasRestart(): Promise<boolean> {
  const startedAt = Date.now()
  const deadline = startedAt + 60000
  const minSuccessAfter = startedAt + 2500
  let sawDisconnect = false

  while (Date.now() < deadline) {
    try {
      await api.status()
      const now = Date.now()
      if (now >= minSuccessAfter && (sawDisconnect || now-startedAt >= 5000)) {
        return true
      }
    } catch {
      sawDisconnect = true
    }
    await new Promise((resolve) => window.setTimeout(resolve, 1000))
  }

  return false
}

function SettingsGroup({ title, children }: { title: string; children: preact.ComponentChild }) {
  return (
    <div>
      <div class="section-label">{title}</div>
      <div class="card settings-group">{children}</div>
    </div>
  )
}

function SettingsRow({
  label, sublabel, hint, mobileSplit, children,
}: {
  label: string
  sublabel?: string
  hint?: string
  mobileSplit?: boolean
  children: preact.ComponentChild
}) {
  return (
    <div class={`settings-row${mobileSplit ? ' settings-row-mobile-split' : ''}`}>
      <div class="settings-label-col">
        <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
          {label}
          {hint && <InfoTip text={hint} />}
        </div>
        {sublabel && <div class="settings-sublabel">{sublabel}</div>}
      </div>
      <div class="settings-field">{children}</div>
    </div>
  )
}

function InfoTip({ text }: { text: string }) {
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null)
  const btnRef = useRef<HTMLButtonElement>(null)

  const show = () => {
    if (!btnRef.current) return
    const r = btnRef.current.getBoundingClientRect()
    setPos({ top: r.top + r.height / 2, left: r.right + 8 })
  }

  return (
    <span style={{ display: 'inline-flex', alignItems: 'center' }}>
      <button
        ref={btnRef}
        style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width: '15px', height: '15px', borderRadius: '50%', background: 'var(--text-3)', color: 'var(--bg)', fontSize: '9px', fontWeight: 700, border: 'none', cursor: 'pointer', flexShrink: 0, lineHeight: 1 }}
        onMouseEnter={show}
        onMouseLeave={() => setPos(null)}
      >
        ?
      </button>
      {pos && (
        <span style={{ position: 'fixed', top: pos.top, left: pos.left, transform: 'translateY(-50%)', background: 'var(--surface, var(--bg))', border: '1px solid var(--border)', borderRadius: '8px', padding: '8px 11px', fontSize: '12px', color: 'var(--text-2)', width: '260px', zIndex: 9999, lineHeight: 1.5, boxShadow: '0 4px 20px rgba(0,0,0,0.22)', pointerEvents: 'none' }}>
          {text.split('\n').map((line, i) => (
            <span key={i} style={{ display: 'block' }}>
              {line}
            </span>
          ))}
        </span>
      )}
    </span>
  )
}

function ToggleField({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label class="toggle">
      <input type="checkbox" checked={checked} onChange={(e) => onChange((e.target as HTMLInputElement).checked)} />
      <span class="toggle-track" />
    </label>
  )
}

const CopyIcon = () => (
  <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
    <rect x="5" y="5" width="9" height="9" rx="1.5" />
    <path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5" />
  </svg>
)

const CheckIcon = () => (
  <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <path d="M3 8l4 4 6-7" />
  </svg>
)

// ── WebAuthn helpers (registration only) ─────────────────────────────────────

function b64urlDecode(s: string): ArrayBuffer {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/')
  const bin = atob(b64.padEnd(b64.length + (4 - b64.length % 4) % 4, '='))
  const buf = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i)
  return buf.buffer
}

function b64urlEncode(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf)
  let bin = ''
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
}

function decodeCreationOptions(opts: Record<string, unknown>): PublicKeyCredentialCreationOptions {
  const pk = (opts.publicKey ?? opts) as Record<string, unknown>
  return {
    ...(pk as object),
    challenge: b64urlDecode(pk.challenge as string),
    user: { ...(pk.user as object), id: b64urlDecode((pk.user as Record<string, string>).id) },
    excludeCredentials: ((pk.excludeCredentials as unknown[]) ?? []).map((c: unknown) => ({
      ...(c as object), id: b64urlDecode((c as Record<string, string>).id),
    })),
  } as unknown as PublicKeyCredentialCreationOptions
}

function encodeCredential(cred: PublicKeyCredential): Record<string, unknown> {
  const r = cred.response as AuthenticatorAttestationResponse
  return {
    id: cred.id, rawId: b64urlEncode(cred.rawId), type: cred.type,
    response: { clientDataJSON: b64urlEncode(r.clientDataJSON), attestationObject: b64urlEncode(r.attestationObject) },
  }
}

function relativeDate(iso: string): string {
  if (!iso) return 'Never'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return 'Never'
  const days = Math.floor((Date.now() - d.getTime()) / 86400000)
  if (days === 0) return 'Today'
  if (days === 1) return 'Yesterday'
  if (days < 7) return `${days} days ago`
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

// ── Local Access section ──────────────────────────────────────────────────────

type LocalCredential = { id: string; type: string; name: string; createdAt: string; lastUsedAt: string }

function LocalAccessSection() {
  // ── Authenticators state ──
  const [creds, setCreds] = useState<LocalCredential[] | null>(null)
  const [addingKey, setAddingKey] = useState(false)
  const [addErr, setAddErr] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null)

  // ── PIN state ──
  const [hasPIN, setHasPIN] = useState<boolean | null>(null)
  const [pinDialogOpen, setPinDialogOpen] = useState(false)


  const loadCreds = () => {
    api.localAuthCredentials()
      .then(list => {
        setCreds(list)
        setHasPIN(list.some(c => c.type === 'pin'))
      })
      .catch(() => { setCreds([]); setHasPIN(false) })
  }

  useEffect(() => { loadCreds() }, [])

  // ── Authenticator actions ──
  const addKey = async () => {
    setAddErr(null)
    setAddingKey(true)
    try {
      const { options, sessionId } = await api.localAuthWebAuthnRegisterBegin('Security Key')
      const cred = await navigator.credentials.create({ publicKey: decodeCreationOptions(options) })
      if (!cred) throw new Error('No credential returned')
      await api.localAuthWebAuthnRegisterFinish(sessionId, 'Security Key', encodeCredential(cred as PublicKeyCredential))
      loadCreds()
      toast.success('Authenticator added.')
    } catch (e) {
      setAddErr(e instanceof Error ? e.message : 'Registration failed')
    } finally {
      setAddingKey(false)
    }
  }

  // Checks for lockout risk before deleting — shows inline confirmation if needed.
  const requestDeleteKey = (id: string) => {
    const webAuthnCount = (creds ?? []).filter(c => c.type === 'webauthn').length
    if (webAuthnCount === 1 && !hasPIN) {
      setConfirmDeleteId(id)
      return
    }
    void deleteKey(id)
  }

  const deleteKey = async (id: string) => {
    setConfirmDeleteId(null)
    setDeletingId(id)
    try {
      await api.localAuthDeleteCredential(id)
      loadCreds()
      toast.success('Authenticator removed.')
    } catch {
      toast.error('Failed to remove authenticator.')
    } finally {
      setDeletingId(null)
    }
  }

  // ── PIN actions ──
  const savePIN = async (pin: string) => {
    await api.localAuthPINSetup(pin)
    setHasPIN(true)
    setPinDialogOpen(false)
    toast.success(hasPIN ? 'PIN changed.' : 'PIN added.')
    loadCreds()
  }

  const webAuthnCreds = creds?.filter(c => c.type === 'webauthn') ?? []
  const pinCred = creds?.find(c => c.type === 'pin') ?? null

  // ── Loading skeleton ──
  if (creds === null) {
    return (
      <>
        <SettingsRow
          label="Authenticators"
          sublabel="Touch ID, Windows Hello, or hardware security keys registered for local access"
          mobileSplit
        >
          <button class="btn btn-sm" style={{ width: '80px' }} disabled>+ Add</button>
        </SettingsRow>
        <SettingsRow label="Loading…" sublabel="" mobileSplit>
          <span />
        </SettingsRow>
        <SettingsRow label="PIN" sublabel="" mobileSplit>
          <button class="btn btn-sm" style={{ width: '80px' }} disabled>Add PIN</button>
        </SettingsRow>
      </>
    )
  }

  return (
    <>
      {/* ── Authenticators ── */}
      <SettingsRow
        label="Authenticators"
        sublabel="Touch ID, Windows Hello, or hardware security keys registered for local access"
        mobileSplit
      >
        <button class="btn btn-sm" style={{ width: '80px' }} onClick={addKey} disabled={addingKey}>
          {addingKey ? 'Registering…' : '+ Add'}
        </button>
      </SettingsRow>
      {addErr && (
        <SettingsRow label="" sublabel="">
          <span style={{ fontSize: '12px', color: 'var(--danger, #ff453a)' }}>{addErr}</span>
        </SettingsRow>
      )}
      {webAuthnCreds.map(c => (
        confirmDeleteId === c.id ? (
          <SettingsRow
            key={c.id}
            label={`Remove "${c.name}"?`}
            sublabel="This is your only authenticator and you have no PIN. Removing it will lock you out of Atlas."
            mobileSplit
          >
            <div style={{ display: 'flex', gap: '8px' }}>
              <button
                class="btn btn-sm"
                onClick={() => deleteKey(c.id)}
                disabled={!!deletingId}
              >
                Remove anyway
              </button>
              <button class="btn btn-sm" onClick={() => setConfirmDeleteId(null)}>
                Cancel
              </button>
            </div>
          </SettingsRow>
        ) : (
          <SettingsRow key={c.id} label={c.name} sublabel={`Last used: ${relativeDate(c.lastUsedAt)}`} mobileSplit>
            <button
              class="btn btn-sm"
              style={{ width: '80px' }}
              onClick={() => requestDeleteKey(c.id)}
              disabled={deletingId === c.id}
            >
              {deletingId === c.id ? '…' : 'Remove'}
            </button>
          </SettingsRow>
        )
      ))}

      {/* ── PIN — first-class credential with last-used date ── */}
      <SettingsRow
        label="PIN"
        sublabel={pinCred
          ? `Last used: ${relativeDate(pinCred.lastUsedAt)}`
          : 'Add a PIN as a fallback for local access when Touch ID is unavailable'}
        mobileSplit
      >
        <button class="btn btn-sm" style={{ width: '80px' }} onClick={() => setPinDialogOpen(true)}>
          {hasPIN ? 'Change' : 'Add PIN'}
        </button>
      </SettingsRow>
      {pinDialogOpen && (
        <PINDialog
          isChange={!!hasPIN}
          onSave={savePIN}
          onCancel={() => setPinDialogOpen(false)}
        />
      )}

    </>
  )
}

function RemoteAccessSection({
  enabled,
  tailscaleEnabled,
  onToggle,
  onTailscaleToggle,
}: {
  enabled: boolean
  tailscaleEnabled: boolean
  onToggle: (v: boolean) => void
  onTailscaleToggle: (v: boolean) => void
}) {
  const [status, setStatus] = useState<{
    lanIP: string | null
    httpsReady: boolean
    accessURL: string | null
    tailscaleIP: string | null
    tailscaleURL: string | null
    tailscaleConnected: boolean
  } | null>(null)
  const [accessToken, setAccessToken] = useState<string | null>(null)
  const [localCopied, setLocalCopied] = useState(false)
  const [tokenCopied, setTokenCopied] = useState(false)
  const [tailscaleCopied, setTailscaleCopied] = useState(false)
  const [revoking, setRevoking] = useState(false)
  const [revoked, setRevoked] = useState(false)

  useEffect(() => {
    api.remoteAccessStatus().then((s) => setStatus(s)).catch(() => {})
    if (enabled) api.remoteAccessKey().then((r) => setAccessToken(r.key)).catch(() => {})
    if (!enabled && !tailscaleEnabled) return
    const interval = setInterval(() => {
      api.remoteAccessStatus().then((s) => setStatus(s)).catch(() => {})
    }, 4000)
    return () => clearInterval(interval)
  }, [enabled, tailscaleEnabled])

  const revokeAll = async () => {
    setRevoking(true)
    try {
      await api.revokeRemoteSessions()
      const r = await api.remoteAccessKey()
      setAccessToken(r.key)
      setRevoked(true)
      setTimeout(() => setRevoked(false), 3000)
    } finally {
      setRevoking(false)
    }
  }

  const copyLocal = async (url: string) => {
    await navigator.clipboard.writeText(url)
    setLocalCopied(true)
    setTimeout(() => setLocalCopied(false), 1800)
  }

  const copyToken = async () => {
    if (!accessToken) return
    await navigator.clipboard.writeText(accessToken)
    setTokenCopied(true)
    setTimeout(() => setTokenCopied(false), 1800)
  }

  const copyTailscale = async (url: string) => {
    await navigator.clipboard.writeText(url)
    setTailscaleCopied(true)
    setTimeout(() => setTailscaleCopied(false), 1800)
  }

  const codeStyle: preact.JSX.CSSProperties = {
    fontSize: '12px',
    background: 'var(--bg)',
    padding: '6px 10px',
    borderRadius: '8px',
    border: '1px solid var(--border)',
    whiteSpace: 'nowrap',
    width: '100%',
    maxWidth: '240px',
    display: 'inline-block',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    boxSizing: 'border-box',
  }

  return (
    <>
      <SettingsRow label="LAN Access" sublabel="Allow browsers on your local network to connect." mobileSplit>
        <ToggleField checked={enabled} onChange={onToggle} />
      </SettingsRow>
      {enabled && (
        <SettingsRow label="Local address" sublabel="Open this URL on any device on your network">
          {status?.httpsReady && status?.accessURL ? (
            <div style={{ position: 'relative', display: 'inline-flex', alignItems: 'center', width: '240px', maxWidth: '100%' }}>
              <code style={{ ...codeStyle, userSelect: 'all', paddingRight: '28px', maxWidth: '100%' }}>{status.accessURL}</code>
              <button
                class={`chat-copy-btn${localCopied ? ' copied' : ''}`}
                style={{ position: 'absolute', right: '4px', opacity: 1, pointerEvents: 'auto' }}
                onClick={() => copyLocal(status.accessURL!)}
                title="Copy"
                aria-label="Copy local address"
              >
                {localCopied ? <CheckIcon /> : <CopyIcon />}
              </button>
            </div>
          ) : status && !status.httpsReady ? (
            <span style={{ fontSize: '12px', color: 'var(--text-3)', maxWidth: '240px', display: 'inline-block', lineHeight: 1.5 }}>
              HTTPS is not configured yet, so Atlas is hiding the LAN address until secure access is ready.
            </span>
          ) : <span style={{ fontSize: '12px', color: 'var(--text-3)' }}>Detecting…</span>}
        </SettingsRow>
      )}
      {enabled && (
        <SettingsRow label="Access key" sublabel="Enter this key when connecting from another device">
          {accessToken ? (
            <div style={{ position: 'relative', display: 'inline-flex', alignItems: 'center', width: '240px', maxWidth: '100%' }}>
              <code style={{ ...codeStyle, fontFamily: 'ui-monospace, monospace', userSelect: 'all', paddingRight: '28px', maxWidth: '100%' }}>{accessToken}</code>
              <button
                class={`chat-copy-btn${tokenCopied ? ' copied' : ''}`}
                style={{ position: 'absolute', right: '4px', opacity: 1, pointerEvents: 'auto' }}
                onClick={copyToken}
                title="Copy"
                aria-label="Copy access key"
              >
                {tokenCopied ? <CheckIcon /> : <CopyIcon />}
              </button>
            </div>
          ) : (
            <span style={{ fontSize: '12px', color: 'var(--text-3)' }}>Loading…</span>
          )}
        </SettingsRow>
      )}
      {enabled && (
        <SettingsRow label="Revoke sessions" sublabel="Sign out all remote devices immediately" mobileSplit>
          <button class="btn btn-sm" onClick={revokeAll} disabled={revoking}>
            {revoked ? 'Revoked' : revoking ? 'Revoking…' : 'Revoke all'}
          </button>
        </SettingsRow>
      )}
      <SettingsRow
        label="Tailscale Access"
        sublabel="Allow devices on your Tailscale network to connect. No access key required."
        hint="Every device on a Tailscale network is cryptographically enrolled by the account owner — network membership is the authentication. Tailscale must be installed and running on both devices."
        mobileSplit
      >
        <ToggleField checked={tailscaleEnabled} onChange={onTailscaleToggle} />
      </SettingsRow>
      {tailscaleEnabled && (
        <SettingsRow label="Tailscale address" sublabel="Open directly on any device in your Tailscale network">
          {status?.tailscaleConnected && status.tailscaleURL ? (
            <div style={{ position: 'relative', display: 'inline-flex', alignItems: 'center', width: '240px', maxWidth: '100%' }}>
              <code style={{ ...codeStyle, userSelect: 'all', paddingRight: '28px', maxWidth: '100%' }}>{status.tailscaleURL}</code>
              <button
                class={`chat-copy-btn${tailscaleCopied ? ' copied' : ''}`}
                style={{ position: 'absolute', right: '4px', opacity: 1, pointerEvents: 'auto' }}
                onClick={() => copyTailscale(status.tailscaleURL!)}
                title="Copy"
                aria-label="Copy Tailscale address"
              >
                {tailscaleCopied ? <CheckIcon /> : <CopyIcon />}
              </button>
            </div>
          ) : status !== null ? (
            <span style={{ fontSize: '12px', color: 'var(--text-3)' }}>Tailscale not detected</span>
          ) : (
            <span style={{ fontSize: '12px', color: 'var(--text-3)' }}>Detecting…</span>
          )}
        </SettingsRow>
      )}
    </>
  )
}

function RestartOverlay({
  phase,
  status,
  onConfirm,
  onCancel,
}: {
  phase: RestartPhase
  status: string
  onConfirm: () => void
  onCancel: () => void
}) {
  useEffect(() => {
    if (phase !== 'confirm') return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.preventDefault(); onCancel() } }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [phase, onCancel])

  return (
    <div class="restart-overlay">
      <div class="restart-overlay-card">
        <div class={`restart-overlay-glyph${phase === 'restarting' ? ' restart-overlay-glyph-spin' : phase === 'done' ? ' restart-overlay-glyph-done' : ''}`}>
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
            {phase === 'done' ? (
              <path d="M5 13l4 4L19 7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
            ) : (
              <>
                <path d="M21 12a9 9 0 1 1-2.64-6.36" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
                <path d="M21 3v6h-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
              </>
            )}
          </svg>
        </div>

        {phase === 'confirm' && (
          <>
            <div class="restart-overlay-title">Restart Atlas?</div>
            <div class="restart-overlay-body">Active requests will be interrupted. Atlas will reconnect automatically.</div>
            <div class="restart-overlay-actions">
              <button class="btn" onClick={onCancel}>Cancel</button>
              <button class="btn btn-primary" onClick={onConfirm}>Restart</button>
            </div>
          </>
        )}

        {phase === 'restarting' && (
          <>
            <div class="restart-overlay-title">{status}</div>
            <div class="restart-overlay-body">This usually takes a few seconds.</div>
          </>
        )}

        {phase === 'done' && (
          <>
            <div class="restart-overlay-title">Atlas is back</div>
            <div class="restart-overlay-body">Everything is running normally.</div>
          </>
        )}
      </div>
    </div>
  )
}

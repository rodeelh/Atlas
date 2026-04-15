import { useState, useEffect, useRef } from 'preact/hooks'
import { api, APIKeyStatus } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { PageSpinner } from '../components/PageSpinner'
import { ErrorBanner } from '../components/ErrorBanner'

// ── Key name helpers ──────────────────────────────────────────────────────────

// "Serper Search API" → "SERPER_SEARCH_API"
function toKeychainKey(label: string): string {
  return label.trim().toUpperCase().replace(/[^A-Z0-9]+/g, '_').replace(/^_+|_+$/g, '')
}

// "SERPER_SEARCH_API" → "Serper Search Api"
function fromKeychainKey(key: string): string {
  return key.split('_').map(w => w.charAt(0) + w.slice(1).toLowerCase()).join(' ')
}

const KNOWN_PROVIDERS = [
  { id: 'openai',      label: 'OpenAI',              sublabel: 'API key for OpenAI models (GPT-5.4, GPT-5.4 Mini, GPT-4.1 etc.)',     key: 'openAIKeySet'      },
  { id: 'anthropic',   label: 'Anthropic',           sublabel: 'API key for Claude models (Sonnet, Opus, Haiku)',                     key: 'anthropicKeySet'   },
  { id: 'gemini',      label: 'Google Gemini',       sublabel: 'API key for Gemini models (Flash, Pro etc.)',                        key: 'geminiKeySet'      },
  { id: 'openrouter',  label: 'OpenRouter',          sublabel: 'API key for OpenRouter models and routers',                           key: 'openRouterKeySet'  },
  { id: 'lm_studio',   label: 'LM Studio',           sublabel: 'Optional Bearer token for LM Studio v0.4.8+ authentication',         key: 'lmStudioKeySet'    },
  { id: 'telegram',    label: 'Telegram Bot',        sublabel: 'Required for Telegram integration',                                   key: 'telegramTokenSet'  },
  { id: 'discord',     label: 'Discord Bot',         sublabel: 'Connects Atlas through your Discord bot token',                      key: 'discordTokenSet'   },
  { id: 'slackBot',    label: 'Slack Bot Token',     sublabel: 'Use the Bot User OAuth Token (xoxb-) for Slack DMs and @mentions',   key: 'slackBotTokenSet'  },
  { id: 'slackApp',    label: 'Slack App Token',     sublabel: 'Use the App-Level Token (xapp-) for Slack Socket Mode connectivity', key: 'slackAppTokenSet'  },
  { id: 'braveSearch', label: 'Brave Search',        sublabel: 'Enables the Web Search skill (websearch.query)',                      key: 'braveSearchKeySet' },
  { id: 'finnhub',     label: 'Finnhub',             sublabel: 'Enables real-time stock quotes via the Finance skill (optional — falls back to Yahoo Finance)', key: 'finnhubKeySet'     },
] as const
const BADGE_STYLE = { fontSize: '11px', padding: '2px 8px' } as const

type ProviderRow = {
  id: string
  label: string
  sublabel: string
  key: string
}

const KNOWN_PROVIDER_STATUS_KEYS = new Set<string>(KNOWN_PROVIDERS.map(p => p.key))

function humanizeCamel(value: string): string {
  return value
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/^./, c => c.toUpperCase())
}

export function APIKeys() {
  const [keyStatus, setKeyStatus] = useState<APIKeyStatus | null>(null)
  const [loading, setLoading]     = useState(true)
  const [error, setError]         = useState<string | null>(null)
  const [addingNew, setAddingNew] = useState(false)
  const [search, setSearch]       = useState('')
  const [searchOpen, setSearchOpen] = useState(false)
  const loadingRef                = useRef(false)
  const searchInputRef            = useRef<HTMLInputElement>(null)
  const searchContainerRef        = useRef<HTMLDivElement>(null)

  const loadKeys = () => {
    if (loadingRef.current) return
    loadingRef.current = true
    api.apiKeys()
      .then(s => setKeyStatus({ ...s, customKeys: s.customKeys ?? [] }))
      .catch(err => setError(err instanceof Error ? err.message : 'Failed to load API key status.'))
      .finally(() => { setLoading(false); loadingRef.current = false })
  }

  // Initial load
  useEffect(() => { loadKeys() }, [])

  // Re-fetch when the tab regains focus so keys stored via the native macOS settings
  // app are reflected without requiring a page reload.
  useEffect(() => {
    const onFocus = () => loadKeys()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [])

  // Close search on click-outside
  useEffect(() => {
    if (!searchOpen) return
    const handler = (e: MouseEvent) => {
      if (searchContainerRef.current && !searchContainerRef.current.contains(e.target as Node)) {
        setSearchOpen(false)
        setSearch('')
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [searchOpen])

  const handleSaved = (updated: APIKeyStatus) =>
    setKeyStatus({ ...updated, customKeys: updated.customKeys ?? [] })

  if (loading) {
    return (
      <div class="screen">
        <PageHeader title="Credentials" subtitle="Keys, tokens, and provider credentials Atlas uses to operate." />
        <PageSpinner />
      </div>
    )
  }

  const customKeys = keyStatus?.customKeys ?? []
  const providers: ProviderRow[] = (() => {
    const known: ProviderRow[] = [...KNOWN_PROVIDERS]
    if (!keyStatus) return known

    const discovered = Object.keys(keyStatus)
      .filter(k => (k.endsWith('KeySet') || k.endsWith('TokenSet')) && !KNOWN_PROVIDER_STATUS_KEYS.has(k))
      .map((k) => {
        const base = k.replace(/(KeySet|TokenSet)$/, '')
        return {
          id: base, // use the discovered provider ID; backend can map or store as custom fallback
          label: humanizeCamel(base),
          sublabel: 'Auto-discovered system credential.',
          key: k,
        }
      })
      .sort((a, b) => a.label.localeCompare(b.label))
    const merged = [...known, ...discovered]
    const keyStatusMap = keyStatus as unknown as Record<string, unknown>
    return merged.sort((a, b) => {
      const aConfigured = keyStatusMap[a.key] === true
      const bConfigured = keyStatusMap[b.key] === true
      if (aConfigured !== bConfigured) return aConfigured ? -1 : 1
      return a.label.localeCompare(b.label)
    })
  })()

  const keyStatusMap     = keyStatus as Record<string, unknown> | null
  const searchQuery      = search.trim().toLowerCase()
  const configuredRows   = providers.filter(p => keyStatusMap?.[p.key] === true)
  const unconfiguredRows = providers.filter(p => keyStatusMap?.[p.key] !== true)

  const searchResults = searchQuery
    ? providers.filter(p =>
        p.label.toLowerCase().includes(searchQuery) ||
        p.sublabel.toLowerCase().includes(searchQuery)
      )
    : null

  return (
    <div class="screen credentials-screen">
      <PageHeader title="Credentials" subtitle="Keys, tokens, and provider credentials Atlas uses to operate." />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {/* Built-in providers */}
      <div>
        <div class="card settings-group">
          <div class="card-header">
            <span class="card-title">Providers</span>
            <div ref={searchContainerRef} class={`chat-history-search${searchOpen ? ' open' : ''}`}>
              <button
                class="chat-history-search-trigger"
                onClick={() => {
                  if (!searchOpen) {
                    setSearchOpen(true)
                    setTimeout(() => searchInputRef.current?.focus(), 180)
                  }
                }}
                title="Search providers"
                aria-label="Search providers"
              >
                <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="6.5" cy="6.5" r="4.5" /><line x1="10" y1="10" x2="14" y2="14" />
                </svg>
              </button>
              <input
                ref={searchInputRef}
                class="chat-history-search-input"
                type="text"
                placeholder="Search providers…"
                value={search}
                onInput={e => setSearch((e.target as HTMLInputElement).value)}
                onKeyDown={e => {
                  if (e.key === 'Escape') { setSearchOpen(false); setSearch('') }
                }}
                tabIndex={searchOpen ? 0 : -1}
              />
              <button
                class="chat-history-close-btn"
                onClick={() => { setSearchOpen(false); setSearch('') }}
                aria-label="Clear search"
              >
                <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
                  <line x1="1" y1="1" x2="11" y2="11" /><line x1="11" y1="1" x2="1" y2="11" />
                </svg>
              </button>
            </div>
          </div>

          {searchResults ? (
            // Search active — flat list of all matching
            searchResults.map((p, i) => (
              <KeyRow
                key={p.id}
                providerID={p.id}
                label={p.label}
                sublabel={p.sublabel}
                configured={keyStatusMap?.[p.key] === true}
                last={i === searchResults.length - 1}
                onSaved={handleSaved}
              />
            ))
          ) : (
            <>
              {configuredRows.map((p, i) => (
                <KeyRow
                  key={p.id}
                  providerID={p.id}
                  label={p.label}
                  sublabel={p.sublabel}
                  configured
                  last={i === configuredRows.length - 1 && unconfiguredRows.length === 0}
                  onSaved={handleSaved}
                />
              ))}
              {unconfiguredRows.length > 0 && (
                <details class="ai-provider-advanced-panel">
                  <summary>{unconfiguredRows.length} unconfigured</summary>
                  <div class="ai-provider-advanced-panel-body">
                    {unconfiguredRows.map((p, i) => (
                      <KeyRow
                        key={p.id}
                        providerID={p.id}
                        label={p.label}
                        sublabel={p.sublabel}
                        configured={false}
                        last={i === unconfiguredRows.length - 1}
                        onSaved={handleSaved}
                      />
                    ))}
                  </div>
                </details>
              )}
            </>
          )}
        </div>
      </div>

      {/* Custom keys */}
      <div>
        <div class="card settings-group">
          <div class="card-header">
            <span class="card-title">Custom Keys</span>
            {!addingNew && (
              <button
                class="btn btn-sm"
                style={{ minWidth: '96px' }}
                onClick={() => setAddingNew(true)}
              >
                Add key
              </button>
            )}
          </div>
          {customKeys.map((name, i) => (
            <CustomKeyRow
              key={name}
              name={name}
              label={keyStatus?.customKeyLabels?.[name]}
              last={i === customKeys.length - 1 && !addingNew}
              onSaved={handleSaved}
            />
          ))}

          {addingNew && (
            <AddKeyRow
              last
              onSaved={(updated) => { handleSaved(updated); setAddingNew(false) }}
              onCancel={() => setAddingNew(false)}
            />
          )}

          {customKeys.length === 0 && !addingNew && (
            <div style={{ padding: '16px 20px', fontSize: '13px', color: 'var(--text-3)' }}>
              No custom keys yet.
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// ── Built-in provider row ─────────────────────────────────────────────────────

interface KeyRowProps {
  providerID: string
  label: string
  sublabel: string
  configured: boolean
  last: boolean
  onSaved: (updated: APIKeyStatus) => void
}

function KeyRow({ providerID, label, sublabel, configured, last, onSaved }: KeyRowProps) {
  const [editing, setEditing] = useState(false)
  const [value, setValue]     = useState('')
  const [saving, setSaving]   = useState(false)
  const [err, setErr]         = useState<string | null>(null)

  const save = async () => {
    if (!value.trim()) return
    setSaving(true); setErr(null)
    try {
      const updated = await api.setAPIKey(providerID, value.trim())
      onSaved(updated); setValue(''); setEditing(false)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed to save.')
    } finally { setSaving(false) }
  }

  const cancel = () => { setValue(''); setEditing(false); setErr(null) }

  return (
    <div style={{ borderBottom: last && !editing ? 'none' : '1px solid var(--border)' }}>
      <div class="settings-row" style={{ borderBottom: 'none' }}>
        <div class="settings-label-col">
          <div style={{ display: 'inline-flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' }}>
            <div class="settings-label">{label}</div>
            <StatusBadge configured={configured} />
          </div>
          <div class="settings-sublabel">{sublabel}</div>
        </div>
        <div class="settings-field credentials-actions">
          <button class="btn btn-sm" style={{ minWidth: '96px' }} onClick={() => setEditing(v => !v)}>
            {configured ? 'Change' : 'Configure'}
          </button>
        </div>
      </div>
      {editing && <KeyInput value={value} onChange={setValue} onSave={save} onCancel={cancel} saving={saving} err={err} placeholder={`Paste ${label} key…`} />}
    </div>
  )
}

// ── Custom key row ────────────────────────────────────────────────────────────

function CustomKeyRow({ name, label, last, onSaved }: { name: string; label?: string; last: boolean; onSaved: (u: APIKeyStatus) => void }) {
  const [editing, setEditing]   = useState(false)
  const [value, setValue]       = useState('')
  const [saving, setSaving]     = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [copied, setCopied]     = useState(false)
  const [err, setErr]           = useState<string | null>(null)

  const save = async () => {
    if (!value.trim()) return
    setSaving(true); setErr(null)
    try {
      const updated = await api.setAPIKey('custom', value.trim(), name, label)
      onSaved(updated); setValue(''); setEditing(false)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed to save.')
    } finally { setSaving(false) }
  }

  const remove = async () => {
    setDeleting(true)
    try {
      const updated = await api.deleteAPIKey(name)
      onSaved(updated)
    } catch { /* best-effort */ } finally { setDeleting(false) }
  }

  const copyName = () => {
    navigator.clipboard.writeText(name).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <div style={{ borderBottom: last && !editing ? 'none' : '1px solid var(--border)' }}>
      <div class="settings-row" style={{ borderBottom: 'none' }}>
        <div class="settings-label-col">
          <div style={{ display: 'inline-flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' }}>
            <div class="settings-label">{label || fromKeychainKey(name)}</div>
            <StatusBadge configured />
          </div>
          <div class="settings-sublabel credentials-meta-row" style={{ marginTop: '2px' }}>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: '11px', color: 'var(--text-3)' }}>
              {name}
            </span>
            <span style={{ color: 'var(--border)', fontSize: '11px' }}>·</span>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: '11px', color: 'var(--text-3)', opacity: 0.6 }}>
              com.projectatlas.credentials
            </span>
            <button
              onClick={copyName}
              title="Copy keychain key name"
              style={{
                background: 'none', border: 'none', padding: '1px 5px', cursor: 'pointer',
                fontSize: '10px', color: copied ? 'var(--green)' : 'var(--text-3)',
                fontFamily: 'var(--font-mono)', borderRadius: '3px',
                transition: 'color 0.15s',
              }}
            >
              {copied ? '✓ copied' : 'copy'}
            </button>
          </div>
        </div>
        <div class="settings-field credentials-actions">
          <button class="btn btn-sm" style={{ minWidth: '96px' }} onClick={() => setEditing(v => !v)}>Change</button>
          <button class="btn btn-sm btn-danger" style={{ minWidth: '96px' }} disabled={deleting} onClick={remove}>
            {deleting ? <span class="spinner" style={{ width: '10px', height: '10px' }} /> : 'Delete'}
          </button>
        </div>
      </div>
      {editing && <KeyInput value={value} onChange={setValue} onSave={save} onCancel={() => { setValue(''); setEditing(false); setErr(null) }} saving={saving} err={err} placeholder={`New value for ${name}…`} />}
    </div>
  )
}

// ── Add new key row ───────────────────────────────────────────────────────────

function AddKeyRow({ last, onSaved, onCancel }: { last: boolean; onSaved: (u: APIKeyStatus) => void; onCancel: () => void }) {
  const [label, setLabel]   = useState('')
  const [keyName, setKeyName] = useState('')
  const [value, setValue]   = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr]       = useState<string | null>(null)

  const save = async () => {
    if (!keyName.trim() || !value.trim()) { setErr('Both a key name and a value are required.'); return }
    setSaving(true); setErr(null)
    try {
      const updated = await api.setAPIKey('custom', value.trim(), keyName.trim(), label.trim() || undefined)
      onSaved(updated)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed to save.')
    } finally { setSaving(false) }
  }

  return (
    <div style={{ borderBottom: last ? 'none' : '1px solid var(--border)', padding: '14px 20px', display: 'flex', flexDirection: 'column', gap: '8px' }}>
      <div class="credentials-add-grid">
        <input
          class="input"
          type="text"
          placeholder="Display name (e.g. Serper Search)"
          value={label}
          onInput={e => setLabel((e.target as HTMLInputElement).value)}
          autoFocus
        />
        <input
          class="input"
          type="text"
          placeholder="Key name (e.g. SERPER_API_KEY)"
          value={keyName}
          onInput={e => setKeyName((e.target as HTMLInputElement).value)}
          style={{ fontFamily: 'var(--font-mono)', fontSize: '12.5px' }}
        />
        <input
          class="input"
          type="password"
          placeholder="Key value"
          value={value}
          onInput={e => setValue((e.target as HTMLInputElement).value)}
          onKeyDown={e => { if (e.key === 'Enter') save(); if (e.key === 'Escape') onCancel() }}
        />
      </div>
      {err && <div style={{ fontSize: '12px', color: 'var(--red)' }}>{err}</div>}
      <div class="credentials-actions">
        <button class="btn btn-sm btn-primary" style={{ minWidth: '96px' }} onClick={save} disabled={saving || !keyName.trim() || !value.trim()}>
          {saving ? <span class="spinner" style={{ width: '11px', height: '11px', borderTopColor: '#000', borderColor: 'rgba(0,0,0,0.2)' }} /> : 'Save'}
        </button>
        <button class="btn btn-sm" style={{ minWidth: '96px' }} onClick={onCancel} disabled={saving}>Cancel</button>
      </div>
    </div>
  )
}

// ── Shared helpers ────────────────────────────────────────────────────────────

function StatusBadge({ configured }: { configured: boolean }) {
  if (!configured) return null
  return (
    <span class="badge badge-green" style={BADGE_STYLE}>Configured</span>
  )
}

function KeyInput({ value, onChange, onSave, onCancel, saving, err, placeholder }: {
  value: string; onChange: (v: string) => void; onSave: () => void; onCancel: () => void
  saving: boolean; err: string | null; placeholder: string
}) {
  return (
    <div style={{ padding: '0 20px 14px', display: 'flex', flexDirection: 'column', gap: '8px' }}>
      <input
        class="input"
        type="password"
        placeholder={placeholder}
        value={value}
        onInput={e => onChange((e.target as HTMLInputElement).value)}
        onKeyDown={e => { if (e.key === 'Enter') onSave(); if (e.key === 'Escape') onCancel() }}
        autoFocus
      />
      {err && <div style={{ fontSize: '12px', color: 'var(--red)' }}>{err}</div>}
      <div class="credentials-actions">
        <button class="btn btn-sm btn-primary" style={{ minWidth: '96px' }} onClick={onSave} disabled={saving || !value.trim()}>
          {saving ? <span class="spinner" style={{ width: '11px', height: '11px', borderTopColor: '#000', borderColor: 'rgba(0,0,0,0.2)' }} /> : 'Save'}
        </button>
        <button class="btn btn-sm" style={{ minWidth: '96px' }} onClick={onCancel} disabled={saving}>Cancel</button>
      </div>
    </div>
  )
}

import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { api, CommunicationChannel, CommunicationPlatformStatus, CommunicationsSnapshot, RuntimeConfig } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { Portal } from '../components/Portal'
import { ErrorBanner } from '../components/ErrorBanner'
import { EmptyState } from '../components/EmptyState'

type PlatformID = CommunicationPlatformStatus['platform']
type SetupField = {
  id: string
  label: string
  placeholder: string
  inputType?: 'password' | 'text'
  storage: 'apiKey' | 'config'
}

type ConnectResult = {
  ok: boolean
  error?: string
  accountName?: string
  qrCodeDataURL?: string
}

const QUICK_SETUP_FIELDS: Record<PlatformID, SetupField[]> = {
  telegram: [
    { id: 'telegram', label: 'Telegram Bot Token', placeholder: '1234567890:ABC…', inputType: 'password', storage: 'apiKey' },
  ],
  discord: [
    { id: 'discord', label: 'Discord Bot Token', placeholder: 'Paste the Bot Token from the Discord Bot page…', inputType: 'password', storage: 'apiKey' },
    { id: 'discordClientID', label: 'Discord Client ID', placeholder: 'Paste the Application ID / Client ID…', inputType: 'text', storage: 'config' },
  ],
  slack: [
    { id: 'slackBot', label: 'Slack Bot Token', placeholder: 'xoxb-…', inputType: 'password', storage: 'apiKey' },
    { id: 'slackApp', label: 'Slack App Token', placeholder: 'xapp-…', inputType: 'password', storage: 'apiKey' },
  ],
  whatsapp: [],
  companion: [],
}

function platformLabel(platform: PlatformID) {
  switch (platform) {
    case 'telegram': return 'Telegram'
    case 'discord': return 'Discord'
    case 'slack': return 'Slack'
    case 'whatsapp': return 'WhatsApp'
    case 'companion': return 'Companion'
    default: return platform
  }
}

function platformSubtitle(platform: PlatformID) {
  switch (platform) {
    case 'telegram': return 'Connect your Telegram bot to Atlas via BotFather.'
    case 'discord': return 'Bot gateway integration for DMs and @mentions.'
    case 'slack': return 'Socket Mode integration for DMs and @mentions.'
    case 'whatsapp': return 'Scan QR code to connect your WhatsApp account.'
    case 'companion': return 'Reserved for the Atlas companion app.'
    default: return ''
  }
}

function setupBadgeClass(status: CommunicationPlatformStatus) {
  switch (status.setupState) {
    case 'ready': return 'badge badge-green'
    case 'validation_failed': return 'badge badge-red'
    case 'partial_setup': return 'badge badge-yellow'
    case 'missing_credentials': return 'badge badge-gray'
    default: return 'badge badge-gray'
  }
}

const EMPTY_VALUES: Record<string, string> = {
  telegram: '',
  discord: '',
  discordClientID: '',
  slackBot: '',
  slackApp: '',
}

function platformBotLabel(platform: CommunicationPlatformStatus) {
  return `Bot name: ${platform.connectedAccountName ?? 'Not available'}`
}

function platformSetupNotes(platform: PlatformID) {
  switch (platform) {
    case 'telegram':
      return [
        'Create the bot with BotFather and copy the token it gives you.',
        'Send the bot one message so Atlas can discover your chat.',
        'Hit Connect — Atlas will verify the token and start receiving messages.',
      ]
    case 'discord':
      return [
        'Paste the Bot Token and the Application ID / Client ID.',
        'Install the bot into the server where you want to use it.',
        'Enable Message Content intent before validating.',
        'Test one DM and one @mention in a normal channel.',
      ]
    case 'slack':
      return [
        'Use the Bot User OAuth Token and the App-Level token.',
        'Turn on the Messages tab, Socket Mode, and install the app.',
        'Subscribe to bot DMs and app mentions before validating.',
        'Send one DM or @mention after setup so Atlas can discover the channel.',
      ]
    case 'whatsapp':
      return [
        'On your phone open WhatsApp → Linked Devices → Link a Device.',
        'Scan the code and keep Atlas running while pairing completes.',
        'Send a message to Atlas in WhatsApp to create the first session.',
      ]
    default:
      return ['Finish setup to make this platform available in Atlas.']
  }
}

function platformDocsURL(platform: PlatformID) {
  switch (platform) {
    case 'telegram':
      return 'https://core.telegram.org/bots#6-botfather'
    case 'discord':
      return 'https://discord.com/developers/docs/quick-start/getting-started'
    case 'slack':
      return 'https://api.slack.com/start/quickstart'
    case 'whatsapp':
      return 'https://faq.whatsapp.com/1317564962315842'
    default:
      return null
  }
}

function formatLastSeen(timestamp: string): { relative: string; absolute: string } {
  const date = new Date(timestamp)
  const absolute = date.toLocaleString()
  const diffMs = Date.now() - date.getTime()

  if (!Number.isFinite(diffMs) || diffMs < 0) {
    return { relative: 'just now', absolute }
  }

  const diffSeconds = Math.floor(diffMs / 1000)
  if (diffSeconds < 45) return { relative: 'just now', absolute }
  if (diffSeconds < 3600) return { relative: `${Math.floor(diffSeconds / 60)}m ago`, absolute }
  if (diffSeconds < 86400) return { relative: `${Math.floor(diffSeconds / 3600)}h ago`, absolute }
  if (diffSeconds < 604800) return { relative: `${Math.floor(diffSeconds / 86400)}d ago`, absolute }

  return { relative: absolute, absolute }
}

function PlatformLogo({ platform }: { platform: PlatformID }) {
  const assetPath = (() => {
    switch (platform) {
      case 'telegram':
        return '/web/chat-app-logos/telegram.png'
      case 'discord':
        return '/web/chat-app-logos/discord.png'
      case 'slack':
        return '/web/chat-app-logos/slack.png'
      case 'whatsapp':
        return '/web/chat-app-logos/whatsapp.svg'
      default:
        return null
    }
  })()

  return (
    <div class={`communication-platform-logo communication-platform-logo-${platform}`} aria-hidden="true">
      {assetPath ? (
        <img src={assetPath} alt="" class="communication-platform-logo-image" />
      ) : (
        <span>{platformLabel(platform).charAt(0)}</span>
      )}
    </div>
  )
}

export function Communications() {
  const [snapshot, setSnapshot] = useState<CommunicationsSnapshot | null>(null)
  const [config, setConfig] = useState<RuntimeConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [busyPlatform, setBusyPlatform] = useState<string | null>(null)
  const [selectedPlatformID, setSelectedPlatformID] = useState<PlatformID | null>(null)
  const [credentialValues, setCredentialValues] = useState<Record<string, string>>(EMPTY_VALUES)
  const [initialCredentialValues, setInitialCredentialValues] = useState<Record<string, string>>(EMPTY_VALUES)

  const mergePlatformStatus = (updatedPlatform: CommunicationPlatformStatus) => {
    setSnapshot(current => {
      if (!current) return current
      return {
        ...current,
        platforms: current.platforms.map(platform =>
          platform.platform === updatedPlatform.platform ? updatedPlatform : platform
        ),
      }
    })
  }

  const waitForPlatformReady = async (platformID: PlatformID) => {
    for (let attempt = 0; attempt < 8; attempt += 1) {
      const nextSnapshot = await api.communications()
      setSnapshot(nextSnapshot)
      const latest = nextSnapshot.platforms.find(platform => platform.platform === platformID)
      if (latest?.setupState === 'ready') {
        return latest
      }
      await new Promise(resolve => window.setTimeout(resolve, 750))
    }
    return null
  }

  const load = async () => {
    setLoading(true)
    try {
      const [communicationsSnapshot, runtimeConfig] = await Promise.all([
        api.communications(),
        api.config(),
      ])
      setSnapshot(communicationsSnapshot)
      setConfig(runtimeConfig)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load communications.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const platforms = snapshot?.platforms ?? []
  const readyPlatforms = platforms.filter(platform => platform.setupState === 'ready')
  const addablePlatforms = platforms.filter(platform => platform.available && platform.setupState !== 'ready')
  const selectedPlatform = platforms.find(platform => platform.platform === selectedPlatformID) ?? null
  const readyPlatformIDs = new Set(readyPlatforms.map(platform => platform.platform))
  const channels = (snapshot?.channels ?? []).filter(channel => readyPlatformIDs.has(channel.platform))
  const sevenDaysMs = 7 * 24 * 60 * 60 * 1000
  const recentChannels = channels.filter(channel => {
    const updatedAtMs = Date.parse(channel.updatedAt)
    if (!Number.isFinite(updatedAtMs)) return true
    return (Date.now() - updatedAtMs) <= sevenDaysMs
  })

  const choosePlatform = async (platform: PlatformID) => {
    const initialValues = {
      ...EMPTY_VALUES,
      discordClientID: platform === 'discord' ? (config?.discordClientID ?? '') : '',
    }
    setSelectedPlatformID(platform)
    setCredentialValues(initialValues)
    setInitialCredentialValues(initialValues)
    setError(null)

    try {
      const setup = await api.communicationSetupValues(platform)
      const loadedValues = {
        ...EMPTY_VALUES,
        ...setup.values,
        discordClientID: platform === 'discord'
          ? (setup.values.discordClientID ?? config?.discordClientID ?? '')
          : '',
      }
      setCredentialValues(loadedValues)
      setInitialCredentialValues(loadedValues)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load saved channel credentials.')
    }
  }

  const saveAndValidate = async (platform: CommunicationPlatformStatus): Promise<ConnectResult> => {
    const fields = QUICK_SETUP_FIELDS[platform.platform]
    setError(null)

    try {
      const credentials = fields.reduce<Record<string, string>>((result, field) => {
        if (field.storage !== 'apiKey') return result
        const value = credentialValues[field.id]?.trim()
        if (value) result[field.id] = value
        return result
      }, {})

      const configPayload = platform.platform === 'discord'
        ? { discordClientID: credentialValues.discordClientID?.trim() || config?.discordClientID || '' }
        : undefined

      const validationResult = await api.validateCommunicationPlatform(platform.platform, {
        credentials,
        config: configPayload,
      })
      mergePlatformStatus(validationResult)

      // WhatsApp QR code step — validation returns the QR, not a ready state
      if (platform.platform === 'whatsapp' && validationResult.metadata?.qrCodeDataURL) {
        return { ok: false, qrCodeDataURL: validationResult.metadata.qrCodeDataURL }
      }

      if (validationResult.setupState !== 'ready') {
        await load()
        return {
          ok: false,
          error: validationResult.blockingReason ?? 'Validation failed. Check your credentials and try again.',
        }
      }

      for (const field of fields) {
        const value = credentialValues[field.id]?.trim()
        const initialValue = initialCredentialValues[field.id]?.trim() ?? ''
        if (value && field.storage === 'apiKey' && value !== initialValue) {
          await api.setAPIKey(field.id, value)
        }
      }

      if (platform.platform === 'discord' && config) {
        const discordClientID = credentialValues.discordClientID?.trim() ?? ''
        if (discordClientID !== config.discordClientID) {
          const { config: updatedConfig } = await api.updateConfig({ ...config, discordClientID })
          setConfig(updatedConfig)
        }
      }

      const updatedPlatform = await api.updateCommunicationPlatform(platform.platform, true)
      mergePlatformStatus(updatedPlatform)

      // Non-blocking background poll — keep UI responsive
      waitForPlatformReady(platform.platform).then(() => load())

      return { ok: true, accountName: updatedPlatform.connectedAccountName ?? undefined }
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : 'Failed to complete setup.' }
    }
  }

  const disablePlatform = async (platform: PlatformID) => {
    setBusyPlatform(`${platform}:disable`)
    try {
      await api.updateCommunicationPlatform(platform, false)
      if (selectedPlatformID === platform) {
        setSelectedPlatformID(null)
      }
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to disable platform.')
    } finally {
      setBusyPlatform(null)
    }
  }

  return (
    <div class="screen communications-screen">
      <PageHeader
        title="Communications"
        subtitle="Manage connected channels and complete setup for supported chat platforms."
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {/* Empty state — shown above Channels card when nothing is connected */}
      {readyPlatforms.length === 0 && (
        <EmptyState
          icon={<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="0.6" stroke-linecap="round" stroke-linejoin="round"><path d="M6 2.5h5.5A1.5 1.5 0 0 1 13 4v3.5A1.5 1.5 0 0 1 11.5 9H9l-2 2V9H6A1.5 1.5 0 0 1 4.5 7.5V4A1.5 1.5 0 0 1 6 2.5z" /><path d="M4.5 6H3A1.5 1.5 0 0 0 1.5 7.5V11l2-1.5h1" /></svg>}
          title="No channels connected"
          body="Connect a messaging platform to start receiving and sending messages through Atlas."
        />
      )}

      {/* Channels card */}
      <div>
        <div class="card settings-group">
          <div class="card-header"><span class="card-title">Channels</span></div>
          {readyPlatforms.map((platform, index) => (
            <ConnectedPlatformRow
              key={platform.id}
              platform={platform}
              last={index === readyPlatforms.length - 1 && addablePlatforms.length === 0}
              busy={busyPlatform === `${platform.platform}:disable`}
              onDisable={() => disablePlatform(platform.platform)}
            />
          ))}
          {addablePlatforms.length > 0 && (
            readyPlatforms.length === 0 ? (
              // No connected channels — show platforms directly, no collapse header
              addablePlatforms.map(platform => (
                <button
                  key={platform.id}
                  class="communication-picker-row communication-platform-row"
                  onClick={() => { void choosePlatform(platform.platform) }}
                >
                  <div class="communication-platform-summary">
                    <PlatformLogo platform={platform.platform} />
                    <div class="settings-label-col">
                      <div class="settings-label">
                        <span style="display:inline-flex;align-items:center;gap:6px">
                          {platformLabel(platform.platform)}
                          {platform.platform === 'telegram' && <span class="badge badge-blue">Recommended</span>}
                        </span>
                      </div>
                      <div class="settings-sublabel">{platformSubtitle(platform.platform)}</div>
                    </div>
                  </div>
                  <div class="communication-platform-controls communication-platform-controls-bottom">
                    <div class="communication-platform-actions">
                      <span class="btn btn-sm communication-platform-action-btn">Connect</span>
                    </div>
                  </div>
                </button>
              ))
            ) : (
              // Some channels connected — collapse the rest
              <details class="ai-provider-advanced-panel">
                <summary>{addablePlatforms.length} not configured</summary>
                <div class="ai-provider-advanced-panel-body">
                  {addablePlatforms.map(platform => (
                    <button
                      key={platform.id}
                      class="communication-picker-row communication-platform-row"
                      onClick={() => { void choosePlatform(platform.platform) }}
                    >
                      <div class="communication-platform-summary">
                        <PlatformLogo platform={platform.platform} />
                        <div class="settings-label-col">
                          <div class="settings-label">
                            {platformLabel(platform.platform)}
                            {platform.platform === 'telegram' && <span class="badge badge-blue" style="margin-left:6px">Recommended</span>}
                          </div>
                          <div class="settings-sublabel">{platformSubtitle(platform.platform)}</div>
                        </div>
                      </div>
                      <div class="communication-platform-controls communication-platform-controls-bottom">
                        <div class="communication-platform-actions">
                          <span class="btn btn-sm communication-platform-action-btn">Connect</span>
                        </div>
                      </div>
                    </button>
                  ))}
                </div>
              </details>
            )
          )}
        </div>
      </div>

      {/* Recent Sessions */}
      <div>
        <div class="card settings-group">
          <div class="card-header"><span class="card-title">Recent Sessions</span></div>
          {recentChannels.length === 0 && (
            <div class="communication-empty-state">
              No sessions in the last 7 days. Once a connected channel receives a message it will appear here.
            </div>
          )}
          {recentChannels.map((channel, index) => (
            <CommunicationChannelRow key={channel.id} channel={channel} last={index === recentChannels.length - 1} />
          ))}
        </div>
      </div>

      {selectedPlatform && (
        <QuickSetupModal
          platform={selectedPlatform}
          values={credentialValues}
          onChange={(id, value) => setCredentialValues(current => ({ ...current, [id]: value }))}
          onCancel={() => {
            setSelectedPlatformID(null)
            setCredentialValues(EMPTY_VALUES)
            setInitialCredentialValues(EMPTY_VALUES)
            void load()
          }}
          onConnect={() => saveAndValidate(selectedPlatform)}
        />
      )}
    </div>
  )
}

function ConnectedPlatformRow({
  platform,
  last,
  busy,
  onDisable,
}: {
  platform: CommunicationPlatformStatus
  last: boolean
  busy: boolean
  onDisable: () => void
}) {
  return (
    <div class="settings-row communication-platform-row" style={{ borderBottom: last ? 'none' : undefined }}>
      <div class="communication-platform-summary">
        <PlatformLogo platform={platform.platform} />
        <div class="settings-label-col">
          <div class="communication-platform-heading">
            <div class="settings-label">{platformLabel(platform.platform)}</div>
            <span class={setupBadgeClass(platform)}>{platform.statusLabel}</span>
          </div>
          <div class="settings-sublabel communication-bot-label">{platformBotLabel(platform)}</div>
          {platform.blockingReason && <div class="settings-sublabel" style={{ color: 'var(--text-2)', marginTop: '4px' }}>{platform.blockingReason}</div>}
        </div>
      </div>
      <div class="communication-platform-controls">
        <div class="communication-platform-actions">
          <button class="btn btn-sm btn-danger communication-platform-action-btn" onClick={onDisable} disabled={busy}>
            {busy ? 'Working…' : 'Disable'}
          </button>
        </div>
      </div>
    </div>
  )
}

type SetupPhase = 'credentials' | 'connecting' | 'qr' | 'success' | 'error'

function QuickSetupModal({
  platform,
  values,
  onChange,
  onCancel,
  onConnect,
}: {
  platform: CommunicationPlatformStatus
  values: Record<string, string>
  onChange: (id: string, value: string) => void
  onCancel: () => void
  onConnect: () => Promise<ConnectResult>
}) {
  const isWhatsApp = platform.platform === 'whatsapp'
  const isDiscord = platform.platform === 'discord'
  const fields = QUICK_SETUP_FIELDS[platform.platform]
  const notes = platformSetupNotes(platform.platform)
  const docsURL = platformDocsURL(platform.platform)
  const installURL = platform.metadata.installURL

  const [phase, setPhase] = useState<SetupPhase>(isWhatsApp ? 'connecting' : 'credentials')
  const [errorMsg, setErrorMsg] = useState('')
  const [qrDataURL, setQrDataURL] = useState('')
  const [accountName, setAccountName] = useState('')

  const hasPendingInput = fields.some(f => values[f.id]?.trim())
  const connectDisabled = fields.length > 0 && !platform.credentialConfigured && !hasPendingInput

  // Keep a ref to the latest callbacks so effects with stable deps can always
  // call the current version without being re-triggered on every parent render.
  const onConnectRef = useRef(onConnect)
  const onCancelRef  = useRef(onCancel)
  onConnectRef.current = onConnect
  onCancelRef.current  = onCancel

  const connect = useCallback(async () => {
    setPhase('connecting')
    const result = await onConnectRef.current()
    if (result.ok) {
      setAccountName(result.accountName ?? '')
      setPhase('success')
      window.setTimeout(() => onCancelRef.current(), 1600)
    } else if (result.qrCodeDataURL) {
      setQrDataURL(result.qrCodeDataURL)
      setPhase('qr')
    } else {
      setErrorMsg(result.error ?? 'Connection failed. Check your credentials and try again.')
      setPhase('error')
    }
  }, []) // stable — reads latest callbacks via refs

  // WhatsApp: auto-trigger QR generation exactly once on mount.
  // Empty deps intentional: isWhatsApp is constant for the lifetime of this
  // modal, and connect is stable (useCallback with []).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (isWhatsApp) connect()
  }, [])

  // WhatsApp: poll for connection after QR is shown; also detect QR expiry
  useEffect(() => {
    if (phase !== 'qr') return
    const interval = window.setInterval(async () => {
      try {
        const snap = await api.communications()
        const status = snap.platforms.find(p => p.platform === platform.platform)
        if (status?.setupState === 'ready') {
          setAccountName(status.connectedAccountName ?? '')
          setPhase('success')
          window.setTimeout(() => onCancelRef.current(), 1600)
        } else if (status?.setupState === 'validation_failed') {
          // QR timed out or bridge errored — show error with retry option
          setErrorMsg(status.blockingReason ?? status.lastError ?? 'QR code expired. Try again.')
          setPhase('error')
        }
      } catch {
        // silently ignore transient polling errors
      }
    }, 2000)
    return () => window.clearInterval(interval)
  }, [phase, platform.platform])

  return (
    <Portal>
      <div
        class="communication-setup-overlay"
        onClick={e => { if ((e.target as HTMLElement).classList.contains('communication-setup-overlay')) onCancel() }}
      >
        <div class="card settings-group communication-setup-card">

          {/* Card header — logo + name only */}
          <div class="communication-setup-card-header">
            <div class="communication-platform-summary">
              <PlatformLogo platform={platform.platform} />
              <div class="settings-label">{platformLabel(platform.platform)}</div>
            </div>
            <button class="communication-setup-dismiss" onClick={onCancel} aria-label="Cancel">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
                <line x1="3" y1="3" x2="13" y2="13" /><line x1="13" y1="3" x2="3" y2="13" />
              </svg>
            </button>
          </div>

          {/* Credentials phase */}
          {phase === 'credentials' && (
            <div class="communication-setup-card-body">
              {fields.length > 0 && (
                <div class="communication-setup-fields">
                  {fields.map(field => (
                    <label key={field.id} class="communication-secret-field">
                      <span>{field.label}</span>
                      <input
                        class="input"
                        type={field.inputType ?? 'password'}
                        value={values[field.id] ?? ''}
                        placeholder={field.placeholder}
                        onInput={e => onChange(field.id, (e.target as HTMLInputElement).value)}
                      />
                    </label>
                  ))}
                </div>
              )}
              {isDiscord && (
                <div class="communication-setup-inline-action">
                  {installURL ? (
                    <a class="btn btn-sm" href={installURL} target="_blank" rel="noreferrer">
                      Install Bot in Discord
                    </a>
                  ) : (
                    <div class="communication-setup-note">Add the Discord Client ID above to unlock the install link.</div>
                  )}
                </div>
              )}
              <details class="communication-setup-guide">
                <summary>Setup guide</summary>
                <div class="communication-setup-guide-body">
                  {notes.map(note => (
                    <div key={note} class="communication-setup-check">{note}</div>
                  ))}
                  {docsURL && (
                    <a class="communication-setup-docs-link" href={docsURL} target="_blank" rel="noreferrer">
                      Official docs ↗
                    </a>
                  )}
                </div>
              </details>
              <div class="communication-setup-card-footer">
                <button class="btn btn-sm btn-ghost" onClick={onCancel}>Cancel</button>
                <button class="btn btn-primary btn-sm communication-setup-connect-btn" onClick={connect} disabled={connectDisabled}>
                  Connect
                </button>
              </div>
            </div>
          )}

          {/* Connecting phase */}
          {phase === 'connecting' && (
            <div class="communication-setup-card-status">
              <div class="communication-setup-status-spinner" />
              <div class="communication-setup-status-title">Connecting to {platformLabel(platform.platform)}…</div>
            </div>
          )}

          {/* QR phase (WhatsApp) */}
          {phase === 'qr' && (
            <div class="communication-setup-card-status">
              <img src={qrDataURL} alt="WhatsApp QR code" class="communication-whatsapp-qr-image" />
              <div class="communication-setup-status-title">Scan with your phone</div>
              <div class="communication-setup-status-body">WhatsApp → Linked Devices → Link a Device</div>
              <button class="btn btn-sm btn-ghost" onClick={onCancel}>Cancel</button>
            </div>
          )}

          {/* Success phase */}
          {phase === 'success' && (
            <div class="communication-setup-card-status">
              <div class="communication-setup-status-icon communication-setup-status-success">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              </div>
              <div class="communication-setup-status-title">Connected</div>
              {accountName && <div class="communication-setup-status-body">{accountName}</div>}
            </div>
          )}

          {/* Error phase */}
          {phase === 'error' && (
            <div class="communication-setup-card-status">
              <div class="communication-setup-status-icon communication-setup-status-error">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                  <line x1="18" y1="6" x2="6" y2="18" />
                  <line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </div>
              <div class="communication-setup-status-title">Connection failed</div>
              <div class="communication-setup-status-body">{errorMsg}</div>
              <div class="communication-setup-card-footer">
                <button class="btn btn-sm btn-ghost" onClick={onCancel}>Cancel</button>
                <button class="btn btn-primary btn-sm" onClick={() => setPhase('credentials')}>Try Again</button>
              </div>
            </div>
          )}

        </div>
      </div>
    </Portal>
  )
}

function CommunicationChannelRow({ channel, last }: { channel: CommunicationChannel; last: boolean }) {
  const lastSeen = formatLastSeen(channel.updatedAt)

  return (
    <div class="settings-row communication-platform-row" style={{ borderBottom: last ? 'none' : undefined }}>
      <div class="communication-platform-summary">
        <PlatformLogo platform={channel.platform} />
        <div class="settings-label-col">
          <div class="communication-platform-heading">
            <div class="settings-label">{channel.channelName || platformLabel(channel.platform)}</div>
          </div>
          <div class="settings-sublabel" title={lastSeen.absolute}>Last seen {lastSeen.relative}</div>
        </div>
      </div>
    </div>
  )
}

import { useState, useEffect } from 'preact/hooks'
import { api } from '../api/client'

type Mode = 'loading' | 'setup-choose' | 'setup-webauthn' | 'setup-pin' | 'auth-webauthn' | 'auth-pin'

interface Props {
  onAuthenticated: () => void
}

// platformAuthLabel returns the device-specific name for the platform authenticator
// (Touch ID, Windows Hello, Face ID, etc.), or null if unavailable on this device.
async function platformAuthLabel(): Promise<string | null> {
  if (!window.PublicKeyCredential?.isUserVerifyingPlatformAuthenticatorAvailable) return null
  const available = await PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable().catch(() => false)
  if (!available) return null
  const ua = navigator.userAgent
  const plat = (navigator as Navigator & { platform?: string }).platform ?? ''
  if (/iPhone|iPad/.test(ua)) return 'Face ID / Touch ID'
  if (/Mac/.test(plat) || /Macintosh/.test(ua)) return 'Touch ID'
  if (/Win/.test(plat) || /Windows/.test(ua)) return 'Windows Hello'
  if (/Android/.test(ua)) return 'Fingerprint / Face Unlock'
  return null
}

// friendlyAuthError maps raw WebAuthn / server error messages to user-readable text.
function friendlyAuthError(err: unknown): string {
  const msg = err instanceof Error ? err.message : String(err)
  // WebAuthn browser errors
  if (msg.includes('NotAllowedError') || msg.includes('not allowed')) return 'Cancelled — please try again.'
  if (msg.includes('SecurityError')) return 'A secure connection (HTTPS) is required to use security keys.'
  if (msg.includes('InvalidStateError')) return 'This authenticator is already registered.'
  if (msg.includes('NotSupportedError')) return 'This device does not support this authentication method.'
  if (msg.includes('AbortError')) return 'Authentication was aborted — please try again.'
  // Server errors (rate limiting, etc.) are already user-readable — pass through.
  return msg || 'Something went wrong — please try again.'
}

export function LocalAuthGate({ onAuthenticated }: Props) {
  const [mode, setMode] = useState<Mode>('loading')
  const [hasWebAuthn, setHasWebAuthn] = useState(false)
  // webAuthnLabel reflects the actual platform authenticator available on this device.
  // Falls back to 'Security Key' if no platform authenticator is detected.
  const [webAuthnLabel, setWebAuthnLabel] = useState('Touch ID / Security Key')
  const [pin, setPIN] = useState('')
  const [pinConfirm, setPINConfirm] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    platformAuthLabel().then(label => setWebAuthnLabel(label ?? 'Security Key')).catch(() => {})
  }, [])

  useEffect(() => {
    api.localAuthStatus().then((s) => {
      if (s.authenticated) {
        onAuthenticated()
        return
      }
      setHasWebAuthn(s.hasWebAuthn)
      if (!s.configured) {
        setMode('setup-choose')
      } else if (s.hasWebAuthn) {
        setMode('auth-webauthn')
      } else {
        setMode('auth-pin')
      }
    }).catch(() => setMode('setup-choose'))
  }, [])

  // ── WebAuthn registration ────────────────────────────────────────────────

  async function startWebAuthnSetup() {
    setMode('setup-webauthn')
    setError(null)
    setBusy(true)
    try {
      const { options, sessionId } = await api.localAuthWebAuthnRegisterBegin(webAuthnLabel)
      const cred = await navigator.credentials.create({ publicKey: decodeCreationOptions(options) })
      if (!cred) throw new Error('No credential returned from authenticator')
      await api.localAuthWebAuthnRegisterFinish(sessionId, webAuthnLabel, encodeCredential(cred as PublicKeyCredential))
      onAuthenticated()
    } catch (err) {
      setMode('setup-choose')
      setError(friendlyAuthError(err))
    } finally {
      setBusy(false)
    }
  }

  // ── WebAuthn authentication ──────────────────────────────────────────────

  async function authenticateWebAuthn() {
    setError(null)
    setBusy(true)
    try {
      const { options, sessionId } = await api.localAuthWebAuthnAuthBegin()
      const assertion = await navigator.credentials.get({ publicKey: decodeRequestOptions(options) })
      if (!assertion) throw new Error('No assertion returned from authenticator')
      await api.localAuthWebAuthnAuthFinish(sessionId, encodeAssertion(assertion as PublicKeyCredential))
      onAuthenticated()
    } catch (err) {
      setError(friendlyAuthError(err))
    } finally {
      setBusy(false)
    }
  }

  // ── PIN setup ────────────────────────────────────────────────────────────

  async function setupPIN() {
    if (pin.length < 6) { setError('PIN must be at least 6 characters'); return }
    if (pin !== pinConfirm) { setError('PINs do not match'); return }
    setError(null)
    setBusy(true)
    try {
      await api.localAuthPINSetup(pin)
      onAuthenticated()
    } catch (err) {
      setError(friendlyAuthError(err))
    } finally {
      setBusy(false)
    }
  }

  // ── PIN authentication ───────────────────────────────────────────────────

  async function verifyPIN() {
    if (!pin) { setError('Enter your PIN'); return }
    setError(null)
    setBusy(true)
    try {
      await api.localAuthPINVerify(pin)
      onAuthenticated()
    } catch (err) {
      setError(friendlyAuthError(err))
    } finally {
      setBusy(false)
    }
  }

  // ── Render ───────────────────────────────────────────────────────────────

  if (mode === 'loading') {
    return (
      <div class="local-auth-gate">
        <div class="local-auth-card">
          <div class="local-auth-spinner" />
        </div>
      </div>
    )
  }

  return (
    <div class="local-auth-gate">
      <div class="local-auth-card">
        <h2 class="local-auth-title">Atlas</h2>

        {/* ── Setup: choose method ── */}
        {mode === 'setup-choose' && (
          <>
            <p class="local-auth-subtitle">Protect local access to Atlas</p>
            <button class="local-auth-btn primary" onClick={startWebAuthnSetup} disabled={busy}>
              {busy ? 'Setting up…' : `Use ${webAuthnLabel}`}
            </button>
            <button class="local-auth-btn secondary" onClick={() => setMode('setup-pin')} disabled={busy}>
              Use a PIN
            </button>
          </>
        )}

        {/* ── Setup: PIN entry ── */}
        {mode === 'setup-pin' && (
          <>
            <p class="local-auth-subtitle">Create a PIN</p>
            <input
              class="local-auth-input"
              type="password"
              placeholder="PIN (min 6 characters)"
              value={pin}
              onInput={(e) => setPIN((e.target as HTMLInputElement).value)}
              onKeyDown={(e) => e.key === 'Enter' && setupPIN()}
              autoFocus
            />
            <input
              class="local-auth-input"
              type="password"
              placeholder="Confirm PIN"
              value={pinConfirm}
              onInput={(e) => setPINConfirm((e.target as HTMLInputElement).value)}
              onKeyDown={(e) => e.key === 'Enter' && setupPIN()}
            />
            <button class="local-auth-btn primary" onClick={setupPIN} disabled={busy}>
              {busy ? 'Setting up…' : 'Set PIN'}
            </button>
            <button class="local-auth-btn text" onClick={() => setMode('setup-choose')} disabled={busy}>
              Back
            </button>
          </>
        )}

        {/* ── Setup: WebAuthn in-progress ── */}
        {mode === 'setup-webauthn' && (
          <>
            <p class="local-auth-subtitle">Complete the authenticator prompt to continue</p>
            <div class="local-auth-spinner" />
            <button
              class="local-auth-btn text"
              onClick={() => { setBusy(false); setMode('setup-choose') }}
            >
              Cancel
            </button>
          </>
        )}

        {/* ── Auth: WebAuthn ── */}
        {mode === 'auth-webauthn' && (
          <>
            <p class="local-auth-subtitle">Verify your identity to continue</p>
            <button class="local-auth-btn primary" onClick={authenticateWebAuthn} disabled={busy}>
              {busy ? 'Verifying…' : `Use ${webAuthnLabel}`}
            </button>
            <button class="local-auth-btn text" onClick={() => setMode('auth-pin')}>
              Use PIN instead
            </button>
          </>
        )}

        {/* ── Auth: PIN ── */}
        {mode === 'auth-pin' && (
          <>
            <p class="local-auth-subtitle">Enter your PIN to continue</p>
            <input
              class="local-auth-input"
              type="password"
              placeholder="PIN"
              value={pin}
              onInput={(e) => setPIN((e.target as HTMLInputElement).value)}
              onKeyDown={(e) => e.key === 'Enter' && verifyPIN()}
              autoFocus
            />
            <button class="local-auth-btn primary" onClick={verifyPIN} disabled={busy}>
              {busy ? 'Verifying…' : 'Continue'}
            </button>
            {hasWebAuthn && (
              <button class="local-auth-btn text" onClick={() => { setError(null); setMode('auth-webauthn') }}>
                {`Use ${webAuthnLabel} instead`}
              </button>
            )}
          </>
        )}

        {error && <p class="local-auth-error">{error}</p>}
      </div>
    </div>
  )
}

// ── WebAuthn encoding helpers ─────────────────────────────────────────────────

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

// decodeCreationOptions converts the server's base64url-encoded options into
// ArrayBuffers as required by the WebAuthn API.
function decodeCreationOptions(opts: Record<string, unknown>): PublicKeyCredentialCreationOptions {
  const o = opts as Record<string, unknown>
  const pk = (o.publicKey ?? o) as Record<string, unknown>
  return {
    ...(pk as object),
    challenge: b64urlDecode(pk.challenge as string),
    user: {
      ...(pk.user as object),
      id: b64urlDecode((pk.user as Record<string, string>).id),
    },
    excludeCredentials: ((pk.excludeCredentials as unknown[]) ?? []).map((c: unknown) => ({
      ...(c as object),
      id: b64urlDecode((c as Record<string, string>).id),
    })),
  } as unknown as PublicKeyCredentialCreationOptions
}

// decodeRequestOptions converts the server's base64url-encoded options into
// ArrayBuffers as required by the WebAuthn API.
function decodeRequestOptions(opts: Record<string, unknown>): PublicKeyCredentialRequestOptions {
  const pk = (opts.publicKey ?? opts) as Record<string, unknown>
  return {
    ...(pk as object),
    challenge: b64urlDecode(pk.challenge as string),
    allowCredentials: ((pk.allowCredentials as unknown[]) ?? []).map((c: unknown) => ({
      ...(c as object),
      id: b64urlDecode((c as Record<string, string>).id),
    })),
  } as unknown as PublicKeyCredentialRequestOptions
}

// encodeCredential converts a PublicKeyCredential creation response into the
// JSON shape expected by the server.
function encodeCredential(cred: PublicKeyCredential): Record<string, unknown> {
  const response = cred.response as AuthenticatorAttestationResponse
  return {
    id: cred.id,
    rawId: b64urlEncode(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: b64urlEncode(response.clientDataJSON),
      attestationObject: b64urlEncode(response.attestationObject),
    },
  }
}

// encodeAssertion converts a PublicKeyCredential assertion response into the
// JSON shape expected by the server.
function encodeAssertion(cred: PublicKeyCredential): Record<string, unknown> {
  const response = cred.response as AuthenticatorAssertionResponse
  return {
    id: cred.id,
    rawId: b64urlEncode(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: b64urlEncode(response.clientDataJSON),
      authenticatorData: b64urlEncode(response.authenticatorData),
      signature: b64urlEncode(response.signature),
      userHandle: response.userHandle ? b64urlEncode(response.userHandle) : null,
    },
  }
}

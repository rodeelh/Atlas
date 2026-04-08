import { useState, useEffect } from 'preact/hooks'
import { api, type RuntimeConfig } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { ErrorBanner } from '../components/ErrorBanner'

// ── Icons ─────────────────────────────────────────────────────────────────────

const EditIcon = () => (
  <svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
    <path d="M10 2l2 2-7 7H3v-2L10 2z" />
  </svg>
)

const DreamIcon = () => (
  <svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
    <path d="M7 1a6 6 0 0 0 6 6 6 6 0 0 1-6 6 6 6 0 0 1-6-6 6 6 0 0 0 6-6z" />
  </svg>
)

// ── MIND.md parser ────────────────────────────────────────────────────────────

interface MindSection { title: string; body: string; special: string | null }

// Canonical display order — lower = earlier. Unlisted sections get 50 (middle).
const SECTION_ORDER: Record<string, number> = {
  'Identity':                    0,  'Who I Am':                   0,
  "Today's Read":                1,
  'Current Frame':               2,  'What Matters Right Now':     2,
  'Working Style':               3,  'How You Work':               3,
  'You':                         4,  'My Understanding of the User': 4,
  "Patterns I've Noticed":       5,
  'Our Story':                   6,  "What's Active":              6,
  'Commitments':                 7,
  "What I'm Curious About":      8,
  "What I've Learned":          99,  'Active Theories':           99,
}

// Sections intentionally hidden from the Mind screen. THOUGHTS is an internal
// curation surface driven by the nap cycle — Atlas tends it between turns and
// surfaces thoughts naturally through chat (or through approvals / unprompted
// messages once phases 4-6 land). Showing it here would break the "thoughts
// are fleeting" framing and turn a private scratchpad into a public list.
const HIDDEN_SECTIONS = new Set<string>(['THOUGHTS'])

function parseMindSections(content: string): MindSection[] {
  const sections: MindSection[] = []
  const parts = content.split(/\n(?=## )/)
  for (const part of parts) {
    const lines = part.split('\n')
    const first = lines[0]
    if (first.startsWith('# ') || !first.startsWith('## ')) continue
    const title = first.slice(3).trim()
    if (HIDDEN_SECTIONS.has(title)) continue
    const body = lines.slice(1).join('\n').trim()
    let special: string | null = null
    if (title === 'Current Frame')     special = 'current-frame'
    if (title === 'Commitments')       special = 'commitments'
    if (title === "What I've Learned") special = 'learned'
    if (title === 'Active Theories')   special = 'learned' // legacy
    sections.push({ title, body, special })
  }
  return [...sections].sort((a, b) => (SECTION_ORDER[a.title] ?? 50) - (SECTION_ORDER[b.title] ?? 50))
}

// ── Body renderers ────────────────────────────────────────────────────────────

function renderBodyLines(body: string) {
  const paras = body.split(/\n\n+/).map(p => p.trim()).filter(p => p && p !== '---')
  return paras.map((p, i) => {
    const lines = p.split('\n')
    const isBulletList = lines.every(l => l.trim().startsWith('-') || !l.trim())
    if (isBulletList) {
      return lines.filter(l => l.trim()).map((l, j) => (
        <div key={`${i}-${j}`} class="row" style={{ padding: '9px 20px', borderBottom: 'none' }}>
          <span style={{ color: 'var(--text-3)', marginRight: '10px', flexShrink: 0 }}>—</span>
          <span style={{ fontSize: '13.5px', color: 'var(--text)', lineHeight: 1.6 }}>{l.replace(/^-\s*/, '')}</span>
        </div>
      ))
    }
    return (
      <div key={i} style={{ padding: '9px 20px', fontSize: '13.5px', lineHeight: 1.65, color: 'var(--text)' }}>
        {p}
      </div>
    )
  })
}

// What I've Learned / Active Theories — confidence-badged items
function LearnedRows({ body }: { body: string }) {
  const lines = body.split('\n').map(l => l.replace(/^-\s*/, '').trim()).filter(Boolean)
  return (
    <>
      {lines.map((line, i) => {
        const isConfirmed  = /\*\*Confirmed\*\*/i.test(line) || /\(confirmed\)/i.test(line)
        const isHighConf   = /\*\*High confidence\*\*/i.test(line)
        const isWorkingTh  = /\*\*Working theory\*\*/i.test(line) || /\(testing\)/i.test(line)
        const isLikely     = /\(likely\)/i.test(line)
        const isRefuted    = /\(refuted\)/i.test(line)
        const clean = line
          .replace(/\*\*(Confirmed|High confidence|Working theory)\*\*:?\s*/gi, '')
          .replace(/\((confirmed|likely|testing|refuted)\)\s*/gi, '')
          .trim()

        let badge: preact.ComponentChild = null
        if (isConfirmed)                   badge = <span class="theory-badge confirmed">confirmed</span>
        else if (isHighConf)               badge = <span class="theory-badge likely">high confidence</span>
        else if (isWorkingTh)              badge = <span class="theory-badge testing">working theory</span>
        else if (isLikely)                 badge = <span class="theory-badge likely">likely</span>
        else if (isRefuted)                badge = <span class="theory-badge refuted">refuted</span>

        return (
          <div key={i} class="row" style={{ padding: '9px 20px', borderBottom: 'none', alignItems: 'flex-start', justifyContent: 'space-between', gap: '12px', textDecoration: isRefuted ? 'line-through' : 'none', color: isRefuted ? 'var(--text-3)' : 'var(--text)' }}>
            <span style={{ fontSize: '13.5px', lineHeight: 1.55, flex: 1 }}>{clean}</span>
            {badge && <div style={{ paddingTop: '1px', flexShrink: 0 }}>{badge}</div>}
          </div>
        )
      })}
    </>
  )
}

// Commitments — accent-tinted rows
function CommitmentRows({ body }: { body: string }) {
  const lines = body.split('\n').map(l => l.replace(/^-\s*/, '').trim()).filter(Boolean)
  if (!lines.length) return <>{renderBodyLines(body)}</>
  return (
    <>
      {lines.map((line, i) => (
        <div key={i} class="row" style={{ padding: '9px 20px', borderBottom: 'none', alignItems: 'flex-start', gap: '10px' }}>
          <span style={{ color: 'var(--accent)', flexShrink: 0, fontSize: '10px', marginTop: '4px' }}>◆</span>
          <span style={{ fontSize: '13.5px', lineHeight: 1.6, color: 'var(--text)' }}>{line}</span>
        </div>
      ))}
    </>
  )
}

// ── Section cards ─────────────────────────────────────────────────────────────

function MindCard({ section }: { section: MindSection }) {
  // Current Frame — same card style, accent border on the left side
  if (section.special === 'current-frame') {
    return (
      <div class="card" style={{ borderLeft: '2px solid var(--accent)' }}>
        <div class="card-header">
          <span class="card-title">{section.title}</span>
        </div>
        <div style={{ padding: '12px 20px', fontSize: '13.5px', lineHeight: 1.7, color: 'var(--text)' }}>
          {section.body}
        </div>
      </div>
    )
  }

  return (
    <div class="card">
      <div class="card-header">
        <span class="card-title">{section.title}</span>
      </div>
      {section.special === 'learned'
        ? <LearnedRows body={section.body} />
        : section.special === 'commitments'
        ? <CommitmentRows body={section.body} />
        : <div>{renderBodyLines(section.body)}</div>
      }
    </div>
  )
}

// ── Diary card ────────────────────────────────────────────────────────────────

interface DiaryDay { date: string; entries: string[] }

function parseDiary(content: string): DiaryDay[] {
  const days: DiaryDay[] = []
  for (const part of content.split(/\n(?=## \d{4}-\d{2}-\d{2})/)) {
    const lines = part.split('\n')
    if (!lines[0].startsWith('## ')) continue
    const date = lines[0].slice(3).trim()
    const entries = lines.slice(1).map(l => l.replace(/^-\s*/, '').trim()).filter(Boolean)
    if (entries.length) days.push({ date, entries })
  }
  return days
}

const ChevronLeft = () => (
  <svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
    <polyline points="9 2 4 7 9 12" />
  </svg>
)
const ChevronRight = () => (
  <svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
    <polyline points="5 2 10 7 5 12" />
  </svg>
)

function DiaryCard({ diary }: { diary: DiaryDay[] }) {
  // Show most recent day first; navigate backward/forward
  const [idx, setIdx] = useState(diary.length - 1)
  const day = diary[idx]
  if (!day) return null
  const canPrev = idx > 0
  const canNext = idx < diary.length - 1

  return (
    <div class="card">
      <div class="card-header" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <span class="card-title">Diary</span>
        <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
          <button class="btn btn-ghost btn-sm" style={{ padding: '3px 7px' }} disabled={!canPrev} onClick={() => setIdx(i => i - 1)}>
            <ChevronLeft />
          </button>
          <span style={{ fontSize: '12px', color: 'var(--text-2)', minWidth: '88px', textAlign: 'center' }}>{day.date}</span>
          <button class="btn btn-ghost btn-sm" style={{ padding: '3px 7px' }} disabled={!canNext} onClick={() => setIdx(i => i + 1)}>
            <ChevronRight />
          </button>
        </div>
      </div>
      {day.entries.map((entry, i) => (
        <div key={i} class="row" style={{ padding: '9px 20px', borderBottom: 'none', alignItems: 'flex-start', gap: '10px' }}>
          <span style={{ color: 'var(--text-3)', flexShrink: 0, marginTop: '2px' }}>—</span>
          <span style={{ fontSize: '13.5px', lineHeight: 1.6, color: 'var(--text)' }}>{entry}</span>
        </div>
      ))}
    </div>
  )
}

// ── Thoughts settings card ────────────────────────────────────────────────────

function MindToggle({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label class="toggle">
      <input type="checkbox" checked={checked} onChange={(e) => onChange((e.target as HTMLInputElement).checked)} />
      <span class="toggle-track" />
    </label>
  )
}

function ThoughtsSettingsCard({ config, onUpdate }: {
  config: RuntimeConfig
  onUpdate: (patch: Partial<RuntimeConfig>) => void
}) {
  return (
    <div class="card">
      <div class="card-header">
        <span class="card-title">Thoughts</span>
      </div>

      <div class="row" style={{ padding: '12px 20px', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div style={{ flex: 1, paddingRight: '24px' }}>
          <div style={{ fontSize: '13.5px', color: 'var(--text)', fontWeight: 500 }}>Enable mind thoughts</div>
          <div style={{ fontSize: '12.5px', color: 'var(--text-2)', marginTop: '3px', lineHeight: 1.5 }}>
            Atlas maintains an internal list of active thoughts — observations, hypotheses, and follow-ups — that inform how it responds.
          </div>
        </div>
        <MindToggle checked={!!config.thoughtsEnabled} onChange={(v) => onUpdate({ thoughtsEnabled: v })} />
      </div>

      {config.thoughtsEnabled && (
        <div class="row" style={{ padding: '12px 20px', justifyContent: 'space-between', alignItems: 'flex-start', borderTop: '1px solid var(--border)' }}>
          <div style={{ flex: 1, paddingRight: '24px' }}>
            <div style={{ fontSize: '13.5px', color: 'var(--text)', fontWeight: 500 }}>Autonomous naps</div>
            <div style={{ fontSize: '12.5px', color: 'var(--text-2)', marginTop: '3px', lineHeight: 1.5 }}>
              After an idle period, Atlas quietly reviews its thoughts, prunes stale ones, and may act on high-confidence read-only insights without prompting.
            </div>
          </div>
          <MindToggle checked={!!config.napsEnabled} onChange={(v) => onUpdate({ napsEnabled: v })} />
        </div>
      )}
    </div>
  )
}

// ── Main screen ───────────────────────────────────────────────────────────────

export function Mind() {
  const [content, setContent]   = useState('')
  const [loading, setLoading]   = useState(true)
  const [error, setError]       = useState<string | null>(null)
  const [editing, setEditing]   = useState(false)
  const [editText, setEditText] = useState('')
  const [saving, setSaving]     = useState(false)
  const [dreaming, setDreaming] = useState(false)
  const [dreamMsg, setDreamMsg] = useState<string | null>(null)
  const [diary, setDiary]       = useState<DiaryDay[]>([])
  const [cfg, setCfg]           = useState<RuntimeConfig | null>(null)

  async function load() {
    setError(null)
    try {
      const [mindData, diaryData, configData] = await Promise.all([api.mind(), api.diary(), api.config()])
      setContent(mindData.content)
      setDiary(parseDiary(diaryData.content))
      setCfg(configData)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load MIND.md.')
    } finally {
      setLoading(false)
    }
  }

  async function updateThoughtsCfg(patch: Partial<RuntimeConfig>) {
    if (!cfg) return
    const next = { ...cfg, ...patch }
    setCfg(next)
    try {
      const result = await api.updateConfig(next)
      setCfg(result.config)
    } catch (e: unknown) {
      setCfg(cfg) // revert
      setError(e instanceof Error ? e.message : 'Failed to save settings.')
    }
  }

  async function saveEdit() {
    setSaving(true); setError(null)
    try {
      await api.updateMind(editText)
      setContent(editText)
      setEditing(false)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed.')
    } finally { setSaving(false) }
  }

  async function triggerDream() {
    setDreaming(true); setDreamMsg(null)
    try {
      await api.forceDream()
      setDreamMsg('Dream cycle started — check back in ~30s')
      setTimeout(() => setDreamMsg(null), 5000)
    } catch (e: unknown) {
      setDreamMsg(e instanceof Error ? e.message : 'Failed to start dream cycle.')
      setTimeout(() => setDreamMsg(null), 4000)
    } finally { setDreaming(false) }
  }

  useEffect(() => { load() }, [])

  const sections = content ? parseMindSections(content) : []

  return (
    <div class="screen">
      <PageHeader
        title="Mind"
        subtitle="Atlas's living inner world — updated after every conversation"
        actions={!editing && (
          <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
            {dreamMsg && <span style={{ fontSize: '12px', color: 'var(--text-2)' }}>{dreamMsg}</span>}
            <button class="btn btn-ghost btn-sm" onClick={triggerDream} disabled={dreaming} title="Run dream consolidation cycle now">
              <DreamIcon /> {dreaming ? 'Dreaming…' : 'Dream'}
            </button>
            <button class="btn btn-ghost btn-sm" onClick={() => { setEditText(content); setEditing(true) }}>
              <EditIcon /> Edit
            </button>
          </div>
        )}
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {loading && (
        <div style={{ display: 'flex', justifyContent: 'center', padding: '48px' }}>
          <span class="spinner" />
        </div>
      )}

      {!loading && !content && !editing && (
        <div class="empty-state">
          <p>MIND.md is empty — Atlas will seed it on the next daemon start.</p>
        </div>
      )}

      {editing && (
        <>
          <textarea
            class="mind-raw-editor"
            value={editText}
            onInput={(e) => setEditText((e.target as HTMLTextAreaElement).value)}
            style={{ width: '100%', minHeight: '520px' }}
          />
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '10px' }}>
            <button class="btn btn-ghost btn-sm" onClick={() => setEditing(false)} disabled={saving}>Cancel</button>
            <button class="btn btn-primary btn-sm" onClick={saveEdit} disabled={saving}>
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </>
      )}

      {!loading && content && !editing && (
        <>
          <div class="skill-group-header">
            <span>Memory</span>
            <p class="skill-group-sub">Atlas's living model of you, your work, and the world</p>
          </div>
          {sections.map((s, i) => <MindCard key={i} section={s} />)}
        </>
      )}
      {!loading && !editing && diary.length > 0 && (
        <>
          <div class="skill-group-header">
            <span>Diary</span>
            <p class="skill-group-sub">Daily reflections written after each dream cycle</p>
          </div>
          <DiaryCard diary={diary} />
        </>
      )}

      {!loading && !editing && cfg && (
        <>
          <div class="skill-group-header">
            <span>Settings</span>
            <p class="skill-group-sub">Control how Atlas's inner loop behaves</p>
          </div>
          <ThoughtsSettingsCard config={cfg} onUpdate={updateThoughtsCfg} />
        </>
      )}
    </div>
  )
}

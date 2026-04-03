import { useState, useEffect, useRef } from 'preact/hooks'
import { api } from '../api/client'
import { PageHeader } from '../components/PageHeader'

// ── SKILLS.md parsers ────────────────────────────────────────────────────────

function parsePrinciples(content: string): string[] {
  const match = content.match(/##\s+Orchestration Principles\s*\n([\s\S]*?)(?=\n##|\n---|\s*$)/)
  if (!match) return []
  return match[1].split('\n').map(l => l.trim()).filter(l => l.length > 0 && !l.startsWith('_'))
}

function parseDontWork(content: string): string[] {
  const match = content.match(/##\s+Things That Don't Work\s*\n([\s\S]*?)(?=\n##|\n---|\s*$)/)
  if (!match) return []
  return match[1].split('\n').map(l => l.replace(/^-\s*/, '').trim()).filter(l => l.length > 0 && !l.startsWith('_'))
}

interface Routine { name: string; triggers: string[]; steps: string[]; learned: string }

function parseRoutines(content: string): Routine[] {
  const section = content.match(/##\s+Learned Routines\s*\n([\s\S]*?)(?=\n##\s+[^#]|\n---\s*$|\s*$)/)
  if (!section) return []
  return section[1].split(/\n###\s+/).filter(b => b.trim()).map(block => {
    const lines = block.split('\n')
    const name = lines[0].trim()
    const triggers: string[] = []; const steps: string[] = []; let learned = ''
    for (const line of lines.slice(1)) {
      const t = line.match(/\*\*Triggers:\*\*\s*(.+)/)
      if (t) triggers.push(...t[1].split(',').map(x => x.replace(/"/g, '').trim()).filter(Boolean))
      const s = line.match(/^\d+\.\s+(.+)/); if (s) steps.push(s[1].trim())
      const l = line.match(/\*\*Learned:\*\*\s*(.+)/); if (l) learned = l[1].trim()
    }
    return { name, triggers, steps, learned }
  }).filter(r => r.name)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function formatMarkdown(content: string): preact.ComponentChild[] {
  const sections = content.split(/\n(?=## )/)
  return sections.map((section, i) => {
    const lines = section.split('\n')
    const firstLine = lines[0]

    if (firstLine.startsWith('# ')) {
      return (
        <div key={i} class="mind-doc-title">
          <h1>{firstLine.slice(2)}</h1>
          <div class="mind-doc-meta">{lines.slice(1).filter(l => l.trim()).join(' ')}</div>
        </div>
      )
    }

    if (firstLine.startsWith('## ')) {
      const title = firstLine.slice(3)
      const body = lines.slice(1).join('\n').trim()
      const isTodaysRead = title === "Today's Read"
      const isWhoIAm = title === 'Who I Am'
      const isTheories = title === 'Active Theories'

      return (
        <div key={i} class={`mind-section${isTodaysRead ? ' todays-read' : ''}${isWhoIAm ? ' who-i-am' : ''}`}>
          <h2 class="mind-section-title">{title}</h2>
          {isTheories
            ? <TheoriesBlock content={body} />
            : <div class="mind-section-body">{renderBody(body)}</div>
          }
        </div>
      )
    }

    return <div key={i} class="mind-section-body">{renderBody(section)}</div>
  })
}

function renderBody(text: string): preact.ComponentChild {
  if (!text.trim()) return null
  const paragraphs = text.split(/\n\n+/)
  return (
    <>
      {paragraphs.map((para, i) => {
        const trimmed = para.trim()
        if (!trimmed || trimmed === '---') return null
        return <p key={i}>{trimmed}</p>
      })}
    </>
  )
}

function TheoriesBlock({ content }: { content: string }) {
  const lines = content.split('\n').filter(l => l.trim())
  return (
    <div class="theories-list">
      {lines.map((line, i) => {
        const testingMatch = line.match(/\(testing\)/i)
        const likelyMatch  = line.match(/\(likely\)/i)
        const confirmedMatch = line.match(/\(confirmed\)/i)
        const refutedMatch = line.match(/\(refuted\)/i)

        let badge: preact.ComponentChild = null
        if (testingMatch)  badge = <span class="theory-badge testing">testing</span>
        if (likelyMatch)   badge = <span class="theory-badge likely">likely</span>
        if (confirmedMatch) badge = <span class="theory-badge confirmed">confirmed</span>
        if (refutedMatch)  badge = <span class="theory-badge refuted">refuted</span>

        const cleanLine = line
          .replace(/\(testing\)/gi, '')
          .replace(/\(likely\)/gi, '')
          .replace(/\(confirmed\)/gi, '')
          .replace(/\(refuted\)/gi, '')
          .trim()

        return (
          <div key={i} class={`theory-item${refutedMatch ? ' refuted' : ''}`}>
            {badge}
            <span>{cleanLine}</span>
          </div>
        )
      })}
    </div>
  )
}

// ── Icons ────────────────────────────────────────────────────────────────────

const RefreshIcon = () => (
  <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
    <path d="M2.5 8a5.5 5.5 0 0 1 9.5-3.8" />
    <polyline points="13.5,2.5 13.5,6 10,6" />
    <path d="M13.5 8a5.5 5.5 0 0 1-9.5 3.8" />
    <polyline points="2.5,13.5 2.5,10 6,10" />
  </svg>
)

const EditIcon = () => (
  <svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
    <path d="M10 2l2 2-7 7H3v-2L10 2z" />
  </svg>
)

// ── Main screen ──────────────────────────────────────────────────────────────

export function Mind() {
  // MIND.md
  const [content, setContent]   = useState('')
  const [loading, setLoading]   = useState(true)
  const [error, setError]       = useState<string | null>(null)
  const [editing, setEditing]   = useState(false)
  const [editText, setEditText] = useState('')
  const [saving, setSaving]     = useState(false)
  const intervalRef = useRef<number | null>(null)

  // SKILLS.md
  const [skillsMem, setSkillsMem]         = useState<string | null>(null)
  const [skillsEditing, setSkillsEditing] = useState(false)
  const [skillsDraft, setSkillsDraft]     = useState('')
  const [skillsSaving, setSkillsSaving]   = useState(false)
  const [skillsSaveOk, setSkillsSaveOk]   = useState(false)
  const [skillsError, setSkillsError]     = useState<string | null>(null)

  async function load() {
    setError(null)
    try {
      const data = await api.mind()
      setContent(data.content)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load MIND.md.')
    } finally {
      setLoading(false)
    }
  }

  async function loadSkillsMem() {
    try {
      const data = await api.skillsMemory()
      setSkillsMem(data.content)
      setSkillsDraft(data.content)
    } catch { setSkillsMem('') }
  }

  async function saveSkillsMem() {
    setSkillsSaving(true); setSkillsError(null)
    try {
      await api.updateSkillsMemory(skillsDraft)
      setSkillsMem(skillsDraft)
      setSkillsEditing(false)
      setSkillsSaveOk(true); setTimeout(() => setSkillsSaveOk(false), 2000)
    } catch (e: unknown) {
      setSkillsError(e instanceof Error ? e.message : 'Save failed.')
    } finally { setSkillsSaving(false) }
  }

  useEffect(() => {
    load()
    loadSkillsMem()
    intervalRef.current = setInterval(load, 30000) as unknown as number
    return () => { if (intervalRef.current) clearInterval(intervalRef.current) }
  }, [])

  function openEdit() {
    setEditText(content)
    setEditing(true)
  }

  async function saveEdit() {
    setSaving(true)
    setError(null)
    try {
      await api.updateMind(editText)
      setContent(editText)
      setEditing(false)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div class="screen mind-screen">
      <PageHeader
        title="Mind"
        subtitle="Atlas's living inner world — updated after every conversation."
        actions={<>
          <button class="btn btn-ghost btn-sm" onClick={openEdit}><EditIcon /> Edit</button>
          <button class="btn btn-primary btn-sm" onClick={load}><RefreshIcon /> Refresh</button>
        </>}
      />

      {error && <p class="error-banner">{error}</p>}

      {loading && <p class="empty-state">Loading MIND.md…</p>}

      {!loading && !content && (
        <p class="empty-state">MIND.md is empty. Atlas will seed it on the next daemon start.</p>
      )}

      {!loading && content && !editing && (
        <div class="mind-document">
          {formatMarkdown(content)}
        </div>
      )}

      {editing && (
        <div class="mind-editor">
          <textarea
            class="mind-raw-editor"
            value={editText}
            onInput={(e) => setEditText((e.target as HTMLTextAreaElement).value)}
            rows={30}
          />
          <div class="mind-editor-footer">
            <button class="btn btn-ghost btn-sm" onClick={() => setEditing(false)} disabled={saving}>Cancel</button>
            <button class="btn btn-primary btn-sm" onClick={saveEdit} disabled={saving}>
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      )}

      {/* ── SKILLS.md ── */}
      {!loading && (() => {
        const principles = skillsMem ? parsePrinciples(skillsMem) : []
        const routines   = skillsMem ? parseRoutines(skillsMem)   : []
        const dontWork   = skillsMem ? parseDontWork(skillsMem)   : []
        const hasContent = principles.length > 0 || routines.length > 0 || dontWork.length > 0

        return (
          <>
            <div class="section-divider" style={{ marginTop: '32px' }}>
              <div class="section-divider-label">
                <span>Skill Memory</span>
                <p class="section-divider-sub">How Atlas has learned to use its tools for you</p>
              </div>
              <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
                {skillsSaveOk  && <span style={{ color: 'var(--c-green)', fontSize: '12px' }}>Saved</span>}
                {skillsError   && <span style={{ color: 'var(--c-red)',   fontSize: '12px' }}>{skillsError}</span>}
                {skillsEditing ? (
                  <>
                    <button class="btn btn-ghost btn-sm" onClick={() => setSkillsEditing(false)} disabled={skillsSaving}>Cancel</button>
                    <button class="btn btn-primary btn-sm" onClick={saveSkillsMem} disabled={skillsSaving}>
                      {skillsSaving ? 'Saving…' : 'Save'}
                    </button>
                  </>
                ) : (
                  <button class="btn btn-ghost btn-sm" onClick={() => { setSkillsDraft(skillsMem ?? ''); setSkillsEditing(true) }}>
                    <EditIcon /> Edit SKILLS.md
                  </button>
                )}
              </div>
            </div>

            {skillsEditing ? (
              <textarea
                class="mind-raw-editor"
                value={skillsDraft}
                onInput={e => setSkillsDraft((e.target as HTMLTextAreaElement).value)}
                style={{ width: '100%', minHeight: '320px', marginTop: '4px' }}
              />
            ) : !hasContent ? (
              <div style={{ padding: '20px 0', color: 'var(--text-2)', fontSize: '13px' }}>
                Nothing learned yet — Atlas builds this automatically as it uses skills for you.
              </div>
            ) : (
              <div class="card">
                {principles.length > 0 && (
                  <div>
                    <div style={{ padding: '14px 20px 10px', fontSize: '12px', fontWeight: 600, color: 'var(--text-2)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>Orchestration Principles</div>
                    <div style={{ padding: '0 20px 16px' }}>
                      {principles.map((p, i) => (
                        <div key={i} style={{ padding: '6px 0', borderBottom: i < principles.length - 1 ? '1px solid var(--border)' : 'none', fontSize: '13px', color: 'var(--text)' }}>{p}</div>
                      ))}
                    </div>
                  </div>
                )}
                {routines.length > 0 && (
                  <div style={{ borderTop: principles.length > 0 ? '1px solid var(--border)' : 'none' }}>
                    <div style={{ padding: '14px 20px 10px', fontSize: '12px', fontWeight: 600, color: 'var(--text-2)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>Learned Routines</div>
                    <div style={{ padding: '0 20px 16px' }}>
                      {routines.map((r, i) => (
                        <div key={i} style={{ padding: '8px 0', borderBottom: i < routines.length - 1 ? '1px solid var(--border)' : 'none' }}>
                          <div style={{ fontWeight: 500, fontSize: '13px', marginBottom: '4px' }}>{r.name}</div>
                          {r.triggers.length > 0 && (
                            <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap', marginBottom: '4px' }}>
                              {r.triggers.map((t, j) => <span key={j} class="badge badge-gray">"{t}"</span>)}
                            </div>
                          )}
                          {r.steps.length > 0 && (
                            <ol style={{ margin: '4px 0 0 16px', fontSize: '12.5px', color: 'var(--text-2)' }}>
                              {r.steps.map((s, j) => <li key={j}>{s}</li>)}
                            </ol>
                          )}
                          {r.learned && <div style={{ marginTop: '4px', fontSize: '12px', color: 'var(--text-2)', fontStyle: 'italic' }}>{r.learned}</div>}
                        </div>
                      ))}
                    </div>
                  </div>
                )}
                {dontWork.length > 0 && (
                  <div style={{ borderTop: '1px solid var(--border)' }}>
                    <div style={{ padding: '14px 20px 10px', fontSize: '12px', fontWeight: 600, color: 'var(--text-2)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>Things That Don't Work</div>
                    <div style={{ padding: '0 20px 16px' }}>
                      {dontWork.map((d, i) => (
                        <div key={i} style={{ padding: '6px 0', borderBottom: i < dontWork.length - 1 ? '1px solid var(--border)' : 'none', fontSize: '13px', color: 'var(--text)' }}>{d}</div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )}
          </>
        )
      })()}
    </div>
  )
}

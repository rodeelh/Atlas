import { useState, useEffect, useRef } from 'preact/hooks'
import { api, SkillRecord, FsRoot, CapabilityRecord } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { ErrorBanner } from '../components/ErrorBanner'
import { EmptyState } from '../components/EmptyState'
import { PageSpinner } from '../components/PageSpinner'
import { toast } from '../toast'

/* ── Badge helpers ──────────────────────────────────────── */

function riskBadge(level: string) {
  switch (level.toLowerCase()) {
    case 'low':    return <span class="badge badge-green">{level}</span>
    case 'medium': return <span class="badge badge-yellow">{level}</span>
    case 'high':   return <span class="badge badge-red">{level}</span>
    default:       return <span class="badge badge-gray">{level}</span>
  }
}

function permissionBadge(level: string) {
  const normalized = level.toLowerCase().trim()
  const canonical = (normalized === 'readonly') ? 'read' : normalized
  switch (canonical) {
    case 'read':    return <span class="badge badge-green">read</span>
    case 'draft':   return <span class="badge badge-yellow">draft</span>
    case 'execute': return <span class="badge badge-red">execute</span>
    default:        return <span class="badge badge-green">read</span>
  }
}

function sourceBadge(source?: string) {
  switch ((source ?? '').toLowerCase()) {
    case 'custom': return <span class="badge badge-blue">Custom</span>
    case 'forge':  return <span class="badge badge-blue">Generated</span>
    default:       return null
  }
}

function validationBadge(skill: SkillRecord) {
  if (!skill.validation) return null
  const ok = skill.validation.status === 'passed' || skill.validation.status === 'warning'
  return <span class={`badge ${ok ? 'badge-green' : 'badge-red'}`}>{skill.validation.status}</span>
}

/* ── Icons ──────────────────────────────────────────────── */

const RefreshIcon = () => (
  <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
    <path d="M2.5 8a5.5 5.5 0 0 1 9.5-3.8" />
    <polyline points="13.5,2.5 13.5,6 10,6" />
    <path d="M13.5 8a5.5 5.5 0 0 1-9.5 3.8" />
    <polyline points="2.5,13.5 2.5,10 6,10" />
  </svg>
)
const ChevronDown = () => (
  <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
    <polyline points="2,4 6,8 10,4" />
  </svg>
)
const ChevronUp = () => (
  <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
    <polyline points="2,8 6,4 10,8" />
  </svg>
)

/* ── Policy labels ──────────────────────────────────────── */

const POLICY_LABELS: Record<string, string> = {
  auto_approve: 'Auto-approve',
  ask_once:     'Ask once',
  always_ask:   'Always ask',
}

/* ── Skill grouping ─────────────────────────────────────── */

type SkillGroupKey = 'agent' | 'capabilities' | 'system' | 'custom'

const SKILL_GROUPS: Array<{ key: SkillGroupKey; label: string; sub: string }> = [
  { key: 'agent',        label: 'Agent Control',     sub: 'Automation, workflow, and agent controls exposed to Atlas' },
  { key: 'capabilities', label: 'Capabilities',      sub: 'Information, research, and creative tools' },
  { key: 'system',       label: 'System Access',     sub: 'Local files, apps, browser, and device controls' },
  { key: 'custom',       label: 'Custom Extensions', sub: 'User-installed and generated skill extensions' },
]

function classifySkill(skill: SkillRecord): SkillGroupKey | 'hidden' {
  const { id, isUserVisible, category, source } = skill.manifest
  if (!isUserVisible || id === 'websearch-api') return 'hidden'
  if (source === 'custom' || source === 'forge') return 'custom'
  if (id === 'gremlin-management') return 'hidden'
  if (id === 'automation-control' || id === 'workflow-control' || id === 'team-control' ||
      id === 'dashboards' || id === 'communication-bridge' || id === 'memory' || id === 'forge') return 'agent'
  if (id === 'atlas.info') return 'hidden'
  if (id === 'voice') return 'capabilities'
  if (category === 'system' || category === 'automation') return 'system'
  return 'capabilities'
}

function skillMatchesSearch(skill: SkillRecord, q: string): boolean {
  if (!q) return true
  const lower = q.toLowerCase()
  if (skill.manifest.name.toLowerCase().includes(lower)) return true
  if (skill.manifest.description?.toLowerCase().includes(lower)) return true
  if (skill.manifest.tags?.some(t => t.toLowerCase().includes(lower))) return true
  if (skill.actions.some(a => a.name.toLowerCase().includes(lower) || a.id.toLowerCase().includes(lower))) return true
  return false
}

const RISK_ORDER: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3 }
function sortByRisk(a: SkillRecord, b: SkillRecord) {
  return (RISK_ORDER[a.manifest.riskLevel] ?? 99) - (RISK_ORDER[b.manifest.riskLevel] ?? 99)
}

/* ── Main component ─────────────────────────────────────── */

export function Skills() {
  const [skills, setSkills]             = useState<SkillRecord[]>([])
  const [capabilities, setCapabilities] = useState<Record<string, CapabilityRecord>>({})
  const [loading, setLoading]           = useState(true)
  const [error, setError]               = useState<string | null>(null)
  const [acting, setActing]             = useState<Set<string>>(new Set())
  const [expanded, setExpanded]         = useState<Set<string>>(new Set())
  const [policies, setPolicies]         = useState<Record<string, string>>({})
  const [bulkActing, setBulkActing]     = useState<Set<string>>(new Set())
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(() => {
    try { const s = localStorage.getItem('atlas_skills_collapsed'); return s ? new Set(JSON.parse(s)) : new Set() }
    catch { return new Set() }
  })
  const [search, setSearch]             = useState('')
  const [searchOpen, setSearchOpen]     = useState(false)
  const searchInputRef                  = useRef<HTMLInputElement>(null)
  const searchContainerRef              = useRef<HTMLDivElement>(null)

  // Custom skill install state
  const [customInstalling, setCustomInstalling] = useState(false)
  const [customRemoving, setCustomRemoving]     = useState<Set<string>>(new Set())

  const installCustomSkill = async () => {
    setCustomInstalling(true)
    try {
      const result = await api.pickFsFolder()
      if (!result?.path) { setCustomInstalling(false); return }
      const res = await api.installCustomSkill(result.path)
      toast.success(res.message ?? 'Skill installed. Restart Atlas to activate it.')
      await loadSkills()
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Install failed.')
    } finally {
      setCustomInstalling(false)
    }
  }

  const removeCustomSkill = async (id: string) => {
    setCustomRemoving(prev => new Set(prev).add(id))
    try {
      await api.removeCustomSkill(id)
      await loadSkills()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to remove skill.')
    } finally {
      setCustomRemoving(prev => { const s = new Set(prev); s.delete(id); return s })
    }
  }

  // File system roots state
  const [fsRoots, setFsRoots]         = useState<FsRoot[]>([])
  const [fsRootAdding, setFsRootAdding] = useState(false)
  const [fsRootError, setFsRootError]   = useState<string | null>(null)

  const loadFsRoots = async () => {
    try { setFsRoots(await api.fsRoots()) }
    catch (e: unknown) { setFsRootError(e instanceof Error ? e.message : 'Failed to load approved folders.') }
  }

  const browseFsFolder = async () => {
    setFsRootAdding(true); setFsRootError(null)
    try {
      const result = await api.pickFsFolder()
      if (result?.path) { await api.addFsRoot(result.path); await loadFsRoots() }
    } catch { /* user cancelled — 204, ignore */ } finally { setFsRootAdding(false) }
  }

  const removeFsRoot = async (id: string) => {
    try { await api.removeFsRoot(id); await loadFsRoots() }
    catch (e: unknown) { setFsRootError(e instanceof Error ? e.message : 'Failed to remove folder.') }
  }

  const loadSkills = async () => {
    try {
      const [skillsResult, policiesResult, capabilitiesResult] = await Promise.allSettled([api.skills(), api.actionPolicies(), api.capabilities()])
      if (skillsResult.status === 'fulfilled') { setSkills(skillsResult.value); setError(null) }
      else throw skillsResult.reason
      if (policiesResult.status === 'fulfilled') setPolicies(policiesResult.value)
      if (capabilitiesResult.status === 'fulfilled') {
        setCapabilities(Object.fromEntries(capabilitiesResult.value.map(c => [c.id, c])))
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load skills.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadSkills()
    loadFsRoots()
  }, [])

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

  const toggleExpand = (id: string) => {
    setExpanded(prev => { const s = new Set(prev); s.has(id) ? s.delete(id) : s.add(id); return s })
  }

  const toggleGroup = (key: string) => {
    setCollapsedGroups(prev => {
      const next = new Set(prev)
      next.has(key) ? next.delete(key) : next.add(key)
      try { localStorage.setItem('atlas_skills_collapsed', JSON.stringify([...next])) } catch {}
      return next
    })
  }

  const toggleEnable = async (skill: SkillRecord) => {
    const id = skill.manifest.id
    setActing(prev => new Set(prev).add(id))
    try {
      skill.manifest.lifecycleState === 'enabled' ? await api.disableSkill(id) : await api.enableSkill(id)
      await loadSkills()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to toggle skill.')
    } finally {
      setActing(prev => { const s = new Set(prev); s.delete(id); return s })
    }
  }

  const changePolicy = async (actionID: string, policy: string) => {
    setPolicies(prev => ({ ...prev, [actionID]: policy }))
    try {
      const updated = await api.setActionPolicy(actionID, policy)
      setPolicies(updated)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update policy.')
      await loadSkills()
    }
  }

  const bulkChangePolicy = async (scopeKey: string, actionIDs: string[], policy: string) => {
    if (!actionIDs.length) return
    setBulkActing(prev => new Set(prev).add(scopeKey))
    try {
      await Promise.all(actionIDs.map(id => api.setActionPolicy(id, policy)))
      const updated = await api.actionPolicies()
      setPolicies(updated)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update policies.')
      await loadSkills()
    } finally {
      setBulkActing(prev => { const s = new Set(prev); s.delete(scopeKey); return s })
    }
  }

  const validate = async (id: string) => {
    setActing(prev => new Set(prev).add(`v:${id}`))
    try {
      await api.validateSkill(id); await loadSkills()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to validate skill.')
    } finally {
      setActing(prev => { const s = new Set(prev); s.delete(`v:${id}`); return s })
    }
  }

  if (loading) {
    return (
      <div class="screen">
        <PageHeader title="Skills" subtitle="Capabilities available to Atlas" />
        <PageSpinner />
      </div>
    )
  }

  const searchWidget = (
    <div ref={searchContainerRef} class={`chat-history-search${searchOpen ? ' open' : ''}`}>
      <button
        class="chat-history-search-trigger"
        onClick={() => { if (!searchOpen) { setSearchOpen(true); setTimeout(() => searchInputRef.current?.focus(), 180) } }}
        title="Search skills"
        aria-label="Search skills"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="6.5" cy="6.5" r="4.5" /><line x1="10" y1="10" x2="14" y2="14" />
        </svg>
      </button>
      <input
        ref={searchInputRef}
        class="chat-history-search-input"
        type="text"
        placeholder="Search skills…"
        value={search}
        onInput={e => setSearch((e.target as HTMLInputElement).value)}
        onKeyDown={e => { if (e.key === 'Escape') { setSearchOpen(false); setSearch('') } }}
        tabIndex={searchOpen ? 0 : -1}
      />
      <button
        class="chat-history-close-btn"
        onClick={() => { setSearchOpen(false); setSearch('') }}
        tabIndex={searchOpen ? 0 : -1}
        aria-label="Clear search"
      >
        <svg width="9" height="9" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
          <line x1="1" y1="1" x2="9" y2="9" /><line x1="9" y1="1" x2="1" y2="9" />
        </svg>
      </button>
    </div>
  )

  const renderSkillRow = (skill: SkillRecord, i: number, total: number) => {
    const id = skill.manifest.id
    const isEnabled = skill.manifest.lifecycleState === 'enabled'
    const isExpanded = expanded.has(id)
    const capability = capabilities[id]
    const artifactTypes = capability?.artifactTypes ?? []
    const requiredRoots = capability?.requiredRoots ?? []
    const requiredCapabilities = capability?.requiredCapabilities ?? []
    const targetLabel = capability ? `${capability.target.type} · ${capability.target.ref}` : null
    return (
      <div key={id} class={`skill-row-shell ${isExpanded ? 'skill-row-shell-expanded' : ''} ${i >= total - 1 ? 'skill-row-shell-last' : ''}`}>
        <div class="skill-row">
          <div class="skill-row-copy">
            <div class="skill-title-line">
              <span class="skill-name">{skill.manifest.name}</span>
              {riskBadge(skill.manifest.riskLevel)}
              {sourceBadge(skill.manifest.source)}
              {validationBadge(skill)}
            </div>
            <div class="skill-meta">
              <span>v{skill.manifest.version}</span>
              <span>{skill.actions.length} action{skill.actions.length !== 1 ? 's' : ''}</span>
              {skill.manifest.description && <span>{skill.manifest.description}</span>}
            </div>
          </div>
          <div class="skill-row-controls">
            <button class="btn btn-sm btn-icon" disabled={acting.has(`v:${id}`)} onClick={() => validate(id)} title="Re-validate">
              {acting.has(`v:${id}`) ? <span class="spinner" style={{ width: '11px', height: '11px' }} /> : <RefreshIcon />}
            </button>
            {skill.manifest.source === 'custom' && (
              <button
                class="btn btn-sm btn-ghost skill-remove-btn"
                disabled={customRemoving.has(id)}
                onClick={() => removeCustomSkill(id)}
                title="Remove this custom skill"
              >
                {customRemoving.has(id) ? <span class="spinner" style={{ width: '11px', height: '11px' }} /> : 'Remove'}
              </button>
            )}
            {skill.actions.length > 0 && (
              <button class="btn btn-sm btn-icon" onClick={() => toggleExpand(id)} title="Show actions">
                {isExpanded ? <ChevronUp /> : <ChevronDown />}
              </button>
            )}
            <label class="toggle" title={isEnabled ? 'Disable skill' : 'Enable skill'}>
              <input type="checkbox" checked={isEnabled} disabled={acting.has(id)} onChange={() => toggleEnable(skill)} />
              <span class="toggle-track" />
            </label>
          </div>
        </div>
        {isExpanded && skill.actions.length > 0 && (
          <div class="skill-actions-list">
            <div class="skill-actions-toolbar">
              <span>Actions</span>
              <div class="skill-bulk-controls">
                <span class="skill-bulk-label">{skill.actions.length} available</span>
                <div class="skill-bulk-picker">
                  <span class="skill-bulk-set-label">Set all:</span>
                  <select
                    class="policy-select"
                    disabled={bulkActing.has(id)}
                    value=""
                    onChange={e => {
                      const val = (e.target as HTMLSelectElement).value
                      if (!val) return
                      ;(e.target as HTMLSelectElement).value = ''
                      bulkChangePolicy(id, skill.actions.map(a => a.id), val)
                    }}
                  >
                    <option value="" disabled>Choose…</option>
                    {Object.entries(POLICY_LABELS).map(([val, label]) => <option key={val} value={val}>{label}</option>)}
                  </select>
                  {bulkActing.has(id) && <span class="spinner" style={{ width: '11px', height: '11px' }} />}
                </div>
              </div>
            </div>
            {skill.actions.map(action => (
              <div class="skill-action-row" key={action.id}>
                <div class="skill-action-copy">
                  <div class="skill-action-heading">
                    <span class="skill-action-name">{action.name}</span>
                    {permissionBadge(action.permissionLevel)}
                  </div>
                  <div class="skill-action-id">{action.publicID ?? action.id}</div>
                  <div class="skill-action-desc">{action.description ?? 'No description provided.'}</div>
                </div>
                <div class="skill-action-policy">
                  <select class="policy-select" value={policies[action.id] ?? action.approvalPolicy}
                    onChange={e => changePolicy(action.id, (e.target as HTMLSelectElement).value)}>
                    {Object.entries(POLICY_LABELS).map(([val, label]) => <option key={val} value={val}>{label}</option>)}
                  </select>
                </div>
              </div>
            ))}
            {(targetLabel || artifactTypes.length > 0 || requiredRoots.length > 0 || requiredCapabilities.length > 0) && (
              <div class="skill-action-row">
                <div class="skill-action-copy">
                  <div class="skill-action-heading">
                    <span class="skill-action-name">Capability Contract</span>
                  </div>
                  {targetLabel && <div class="skill-action-desc"><strong>Target:</strong> {targetLabel}</div>}
                  {artifactTypes.length > 0 && <div class="skill-action-desc"><strong>Artifacts:</strong> {artifactTypes.join(', ')}</div>}
                  {requiredCapabilities.length > 0 && <div class="skill-action-desc"><strong>Depends on:</strong> {requiredCapabilities.join(', ')}</div>}
                  {requiredRoots.length > 0 && <div class="skill-action-desc"><strong>Prerequisites:</strong> {requiredRoots.join(', ')}</div>}
                </div>
              </div>
            )}
            {id === 'file-system' && (
              <div class="skill-fs-roots">
                <div class="skill-actions-toolbar"><span>Approved Folders</span></div>
                {fsRoots.length === 0
                  ? <div class="skill-fs-empty">No folders approved yet. Atlas cannot read or write files until at least one folder is added.</div>
                  : <div class="skill-fs-list">
                      {fsRoots.map(root => (
                        <div key={root.id} class="skill-fs-row">
                          <span>{root.path}</span>
                          <button class="btn btn-sm btn-ghost skill-remove-btn" onClick={() => removeFsRoot(root.id)}>Remove</button>
                        </div>
                      ))}
                    </div>
                }
                {fsRootError && <div class="skill-inline-error">{fsRootError}</div>}
                <div class="skill-fs-footer">
                  <button class="btn btn-primary btn-sm" disabled={fsRootAdding} onClick={browseFsFolder}>
                    {fsRootAdding ? <span class="spinner" style={{ width: '11px', height: '11px' }} /> : 'Add Folder'}
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    )
  }

  const grouped = skills.reduce<Record<string, SkillRecord[]>>((acc, skill) => {
    const key = classifySkill(skill)
    if (key === 'hidden') return acc
    ;(acc[key] ??= []).push(skill)
    return acc
  }, {})
  Object.values(grouped).forEach(g => g.sort(sortByRisk))

  const searchQuery = search.trim().toLowerCase()

  return (
    <div class="screen">
      <PageHeader
        title="Skills"
        subtitle="Capabilities available to Atlas"
        actions={searchWidget}
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {skills.length === 0 && !error ? (
        <EmptyState
          icon={<svg viewBox="0 0 36 36" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><polygon points="18,3 22,13 33,13 24,20 27,31 18,24 9,31 12,20 3,13 14,13" /></svg>}
          title="No skills registered"
          body="Skills will appear here once the daemon bootstraps"
        />
      ) : (
        <>
          {SKILL_GROUPS.map(group => {
            const allSkills   = grouped[group.key] ?? []
            const groupSkills = searchQuery ? allSkills.filter(s => skillMatchesSearch(s, searchQuery)) : allSkills
            const isCustom    = group.key === 'custom'
            const isCollapsed = collapsedGroups.has(group.key)

            if (searchQuery && groupSkills.length === 0 && !isCustom) return null
            if (!groupSkills.length && !isCustom) return null

            return (
              <div key={group.key} class="card settings-group">
                <div class="card-header" style={{ cursor: 'pointer', userSelect: 'none' }} onClick={() => toggleGroup(group.key)}>
                  <div>
                    <span class="card-title">{group.label}</span>
                    {group.sub && <div class="card-subtitle" style={{ fontSize: '12px', color: 'var(--text-3)', marginTop: '2px' }}>{group.sub}</div>}
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                      <svg
                      width="12" height="12" viewBox="0 0 12 12" fill="none"
                      stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"
                      style={{ transform: isCollapsed ? 'rotate(-90deg)' : 'rotate(0deg)', transition: 'transform 0.15s', color: 'var(--text-3)', flexShrink: 0 }}
                    >
                      <polyline points="2,4 6,8 10,4" />
                    </svg>
                  </div>
                </div>

                {!isCollapsed && (
                  isCustom && groupSkills.length === 0 ? (
                    <div style={{ padding: '32px 20px', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '12px', textAlign: 'center' }}>
                      <div class="skill-empty-copy">
                        Install a folder that contains a <code>skill.json</code> manifest and executable <code>run</code> entrypoint.
                        Generated extensions also appear here once installed.
                      </div>
                      <button class="btn btn-primary btn-sm" disabled={customInstalling} onClick={installCustomSkill}>
                        {customInstalling ? <span class="spinner" style={{ width: '11px', height: '11px' }} /> : 'Install from Folder'}
                      </button>
                    </div>
                  ) : (
                    groupSkills.map((skill, i) => renderSkillRow(skill, i, groupSkills.length))
                  )
                )}
              </div>
            )
          })}
        </>
      )}
    </div>
  )
}

import { useState, useEffect, useRef } from 'preact/hooks'
import { api, MemoryItem } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { ErrorBanner } from '../components/ErrorBanner'
import { EmptyState } from '../components/EmptyState'
import { PageSpinner } from '../components/PageSpinner'

type Category = 'all' | 'profile' | 'preference' | 'project' | 'workflow' | 'episodic'

const CATEGORIES: Category[] = ['all', 'profile', 'preference', 'project', 'workflow', 'episodic']

function categoryBadge(cat: string) {
  switch (cat.toLowerCase()) {
    case 'profile':    return <span class="badge badge-blue">{cat}</span>
    case 'preference': return <span class="badge badge-green">{cat}</span>
    case 'project':    return <span class="badge badge-yellow">{cat}</span>
    case 'workflow':   return <span class="badge badge-gray">{cat}</span>
    case 'episodic':   return <span class="badge badge-gray">{cat}</span>
    default:           return <span class="badge badge-gray">{cat}</span>
  }
}

const SearchIcon = () => (
  <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round">
    <circle cx="6" cy="6" r="4.5" />
    <line x1="9.5" y1="9.5" x2="13" y2="13" />
  </svg>
)

const MEMORY_PAGE_SIZE = 10

function memorySortTime(memory: MemoryItem): number {
  const primary = Date.parse(memory.updatedAt || memory.createdAt || '')
  if (Number.isFinite(primary)) return primary
  const fallback = Date.parse(memory.createdAt || '')
  return Number.isFinite(fallback) ? fallback : 0
}

function sortMemoriesChronologically(items: MemoryItem[]): MemoryItem[] {
  return [...items].sort((a, b) => memorySortTime(b) - memorySortTime(a))
}

function formatMemoryTimestamp(updatedAt?: string, createdAt?: string): { label: string; absolute: string } | null {
  const iso = updatedAt || createdAt
  if (!iso) return null
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return null

  const absolute = date.toLocaleString()
  const diffSeconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000))
  let relative = ''
  if (diffSeconds < 45) relative = 'just now'
  else if (diffSeconds < 3600) relative = `${Math.floor(diffSeconds / 60)}m ago`
  else if (diffSeconds < 86400) relative = `${Math.floor(diffSeconds / 3600)}h ago`
  else if (diffSeconds < 604800) relative = `${Math.floor(diffSeconds / 86400)}d ago`
  else relative = date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })

  return {
    label: updatedAt ? `Updated ${relative}` : `Saved ${relative}`,
    absolute,
  }
}

export function Memory() {
  const [memories, setMemories] = useState<MemoryItem[]>([])
  const [filtered, setFiltered] = useState<MemoryItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [category, setCategory] = useState<Category>('all')
  const [query, setQuery] = useState('')
  const [searching, setSearching] = useState(false)
  const [deleting, setDeleting] = useState<Set<string>>(new Set())
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)
  const [visibleCount, setVisibleCount] = useState(MEMORY_PAGE_SIZE)
  const searchTimeout = useRef<ReturnType<typeof setTimeout> | null>(null)

  const load = async () => {
    try {
      const data = await api.memories()
      setMemories(sortMemoriesChronologically(data))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load memories.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  useEffect(() => {
    if (query.trim()) return
    const cat = category === 'all' ? null : category
    const items = cat ? memories.filter(m => m.category.toLowerCase() === cat) : memories
    setFiltered(sortMemoriesChronologically(items))
  }, [memories, category, query])

  useEffect(() => {
    if (!query.trim()) { setSearching(false); return }
    if (searchTimeout.current) clearTimeout(searchTimeout.current)
    searchTimeout.current = setTimeout(async () => {
      setSearching(true)
      setError(null)
      try {
        // api.searchMemories does not accept a category parameter — category
        // filtering is applied client-side below as a safety net.
        const results = await api.searchMemories(query.trim())
        const cat = category === 'all' ? null : category
        const items = cat ? results.filter(m => m.category.toLowerCase() === cat) : results
        setFiltered(sortMemoriesChronologically(items))
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Search failed.')
      } finally {
        setSearching(false)
      }
    }, 350)
  }, [query, category])

  useEffect(() => {
    setVisibleCount(MEMORY_PAGE_SIZE)
  }, [query, category, memories.length])

  const visibleMemories = filtered.slice(0, visibleCount)
  const hasMoreMemories = filtered.length > visibleCount

  const deleteMemory = async (id: string) => {
    setDeleting(prev => new Set(prev).add(id))
    try {
      await api.deleteMemory(id)
      setMemories(prev => prev.filter(m => m.id !== id))
      setFiltered(prev => prev.filter(m => m.id !== id))
      setConfirmDelete(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete memory.')
    } finally {
      setDeleting(prev => { const s = new Set(prev); s.delete(id); return s })
    }
  }

  if (loading) {
    return (
      <div class="screen">
        <PageHeader title="Memory" subtitle="Facts Atlas has learned from your conversations" />
        <PageSpinner />
      </div>
    )
  }

  return (
    <div class="screen">
      <PageHeader
        title="Memory"
        subtitle="Facts Atlas has learned from your conversations"
        actions={
          <span class="surface-chip">
            {visibleMemories.length}/{filtered.length} {filtered.length === 1 ? 'item' : 'items'}
            {category !== 'all' && ` · ${category}`}
          </span>
        }
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      <div class="card memory-toolbar-card">
        <div style={{ position: 'relative' }}>
          <span style={{ position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)', color: 'var(--text-3)', pointerEvents: 'none' }}>
            <SearchIcon />
          </span>
          <input
            class="input"
            type="search"
            placeholder="Search memories…"
            value={query}
            onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
            style={{ paddingLeft: '32px' }}
          />
        </div>

        <div class="filter-bar">
          {CATEGORIES.map(cat => (
            <button
              key={cat}
              class={`tab-btn${category === cat ? ' active' : ''}`}
              onClick={() => setCategory(cat)}
            >
              {cat.charAt(0).toUpperCase() + cat.slice(1)}
            </button>
          ))}
        </div>
      </div>

      {searching && (
        <div class="memory-searching">
          <span class="spinner" />
          Searching…
        </div>
      )}

      {!searching && filtered.length === 0 && (
        <EmptyState
          class="memory-empty-card"
          icon={<svg viewBox="0 0 36 36" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="18" cy="9" rx="11" ry="4" /><path d="M7 9v6c0 2.2 4.9 4 11 4s11-1.8 11-4V9" /><path d="M7 15v6c0 2.2 4.9 4 11 4s11-1.8 11-4v-6" /></svg>}
          title="No memories found"
          body={query ? `No results for "${query}"` : 'Atlas will save facts here as you chat.'}
        />
      )}

      {!searching && filtered.length > 0 && (
        <div class="card memory-list-card">
          {visibleMemories.map((m, i) => (
            <div key={m.id} style={{ borderBottom: i < visibleMemories.length - 1 ? '1px solid var(--border)' : 'none' }}>
              <div class="row memory-row" style={{ borderBottom: 'none', alignItems: 'flex-start' }}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div class="memory-title">{m.title}</div>
                  <div class="memory-content">{m.content}</div>
                  {(() => {
                    const stamp = formatMemoryTimestamp(m.updatedAt, m.createdAt)
                    if (!stamp) return null
                    return (
                      <div class="memory-meta" title={stamp.absolute}>
                        {stamp.label}
                      </div>
                    )
                  })()}
                  <div class="memory-footer">
                    {categoryBadge(m.category)}
                    {m.isUserConfirmed
                      ? <span class="badge badge-green">confirmed</span>
                      : <span class="badge badge-gray">inferred</span>}
                    {m.isSensitive && <span class="badge badge-red">sensitive</span>}
                    {m.tags.map(t => (
                      <span key={t} class="badge badge-gray">{t}</span>
                    ))}
                  </div>
                </div>
                <div style={{ flexShrink: 0, paddingTop: '2px' }}>
                  {confirmDelete === m.id ? (
                    <div style={{ display: 'flex', gap: '6px' }}>
                      <button
                        class="btn btn-sm btn-danger"
                        disabled={deleting.has(m.id)}
                        onClick={() => deleteMemory(m.id)}
                      >
                        {deleting.has(m.id)
                          ? <span class="spinner" style={{ width: '11px', height: '11px' }} />
                          : 'Confirm'}
                      </button>
                      <button class="btn btn-sm" onClick={() => setConfirmDelete(null)}>
                        Cancel
                      </button>
                    </div>
                  ) : (
                    <button
                      class="btn btn-sm btn-danger"
                      onClick={() => setConfirmDelete(m.id)}
                    >
                      Delete
                    </button>
                  )}
                </div>
              </div>
            </div>
          ))}
          {(hasMoreMemories || visibleCount > MEMORY_PAGE_SIZE) && (
            <div class="memory-pagination">
              <div class="memory-pagination-side">
                {hasMoreMemories && (
                  <button class="btn btn-sm" onClick={() => setVisibleCount(count => count + MEMORY_PAGE_SIZE)}>
                    Show next 10
                  </button>
                )}
              </div>
              <div class="memory-pagination-side memory-pagination-side-right">
                {hasMoreMemories && (
                  <button class="btn btn-sm" onClick={() => setVisibleCount(filtered.length)}>
                    Show all
                  </button>
                )}
                {visibleCount > MEMORY_PAGE_SIZE && (
                  <button class="btn btn-sm" onClick={() => setVisibleCount(MEMORY_PAGE_SIZE)}>
                    Show less
                  </button>
                )}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

import { useState, useEffect, useRef, useCallback, useMemo } from 'preact/hooks'
import type { JSX } from 'preact/jsx-runtime'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/common'
import { api, MessageAttachment, LinkPreview, ConversationSummary, ConversationDetail, CloudModelHealth, type ChatStreamEvent } from '../api/client'
import { pickPresencePhrase } from '../presence_phrases'
import { toast } from '../toast'
import { PageHeader } from '../components/PageHeader'
import { ErrorBanner } from '../components/ErrorBanner'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { formatProviderModelName } from '../modelName'
import { voiceSpeechSupported, startVoiceSpeech, type VoiceSpeechSession } from '../lib/voiceSpeech'
import { createVoicePlayer, warmupAudioContext, type VoicePlayer } from '../lib/voicePlayback'
import { extractStreamError } from './chatStream'

// Configure marked once — GFM tables, auto line-breaks, external links
marked.use({
  gfm: true,
  breaks: true,
  renderer: {
    image({ href, text }: { href: string; text?: string | null }) {
      const safeHref = encodeURI(href ?? '')
      const altAttr  = text ? ` alt="${text.replace(/"/g, '&quot;')}"` : ''
      const downloadIcon = `<svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M8 2v8M5 7l3 3 3-3M3 13h10"/></svg>`
      return `<div class="generated-image-wrap"><img src="${safeHref}"${altAttr} /><a href="${safeHref}" download class="img-download-btn chat-copy-btn" title="Download image" aria-label="Download image">${downloadIcon}</a></div>`
    },
    link({ href, title, text }: { href: string; title?: string | null; text: string }) {
      const safeHref = encodeURI(href ?? '')
      const titleAttr = title ? ` title="${title.replace(/"/g, '&quot;')}"` : ''
      return `<a href="${safeHref}"${titleAttr} target="_blank" rel="noopener noreferrer" class="chat-link">${text}</a>`
    },
    code({ text, lang }: { text: string; lang?: string }) {
      const rawLang   = lang?.trim() || ''
      const label     = (rawLang || 'code').toUpperCase()
      const copyIcon  = `<svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/></svg>`
      const terminalLangs = new Set(['bash', 'sh', 'shell', 'zsh', 'fish', 'cmd', 'bat', 'powershell', 'ps1'])
      const isTerminal = terminalLangs.has(rawLang.toLowerCase())
      const runIcon   = `<svg width="11" height="11" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M3 2.5l10 5.5-10 5.5V2.5z"/></svg>`
      const runBtn    = isTerminal ? `<button class="code-run-btn" type="button" title="Run in terminal" aria-label="Run in terminal" data-run-code="${encodeURIComponent(text)}">${runIcon} Run</button>` : ''
      let highlighted: string
      try {
        highlighted = rawLang && hljs.getLanguage(rawLang)
          ? hljs.highlight(text, { language: rawLang }).value
          : hljs.highlightAuto(text).value
      } catch {
        highlighted = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      }
      return `<div class="code-block"><div class="code-block-header"><span class="code-block-lang">${label}</span><div class="code-block-actions">${runBtn}<button class="code-copy-btn" type="button" title="Copy code" aria-label="Copy code">${copyIcon}</button></div></div><pre>${highlighted}</pre></div>`
    }
  }
})

// ── Types ─────────────────────────────────────────────────────────────────────

interface FileAttachment {
  filename: string
  mimeType: string
  fileSize: number
  fileToken: string
}

interface SearchResultCardData {
  rank?: number
  title: string
  url: string
  domain?: string
  snippet?: string
  provider?: string
  confidence?: string
  publishedAt?: string
  sourceType?: string
  contentPreview?: string
}

interface SourceListGroup {
  query?: string
  provider?: string
  fetchedAt?: string
  results: SearchResultCardData[]
}

type MessageBlock =
  | {
      type: 'source-list'
      title: string
      query?: string
      provider?: string
      fetchedAt?: string
      results: SearchResultCardData[]
    }
  | {
      type: 'multi-source-list'
      title: string
      groups: SourceListGroup[]
    }
  | {
      type: 'source-summary'
      title?: string
      url: string
      domain?: string
      description?: string
      summary?: string
      headings?: string[]
      publishedAt?: string
      canonicalURL?: string
    }
  | {
      type: 'map'
      card: MapCardData
    }
  | {
      type: 'file'
      file: FileAttachment
    }

interface Message {
  id: string
  role: 'user' | 'assistant'
  content: string
  isTyping?: boolean
  createdAt?: number
  /** URL → preview map so each card can be anchored to its source URL. */
  linkPreviews?: Record<string, LinkPreview>
  /** Files produced by tools during this assistant turn. */
  fileAttachments?: FileAttachment[]
  /** Structured map data from maps.* tool calls — rendered as inline map cards. */
  mapCards?: MapCardData[]
  /** Rich structured blocks rendered below the message body. */
  blocks?: MessageBlock[]
}

type ConversationMessage = ConversationDetail['messages'][number]

interface MapCardData {
  type: 'point' | 'directions' | 'places'
  latitude?: number
  longitude?: number
  label?: string
  origin?: string
  destination?: string
  mode?: string
  distance?: string
  duration?: string
  query?: string
  places?: Array<{ name: string; address: string; latitude: number; longitude: number }>
}

type ChatProvider = 'openai' | 'anthropic' | 'gemini' | 'openrouter' | 'lm_studio' | 'ollama' | 'atlas_engine' | 'atlas_mlx'
const CLOUD_CHAT_PROVIDERS: ChatProvider[] = ['openai', 'anthropic', 'gemini', 'openrouter']

const PROVIDER_LABELS: Record<ChatProvider, string> = {
  openai:       'OpenAI',
  anthropic:    'Anthropic',
  gemini:       'Gemini',
  openrouter:   'OpenRouter',
  lm_studio:    'LM Studio',
  ollama:       'Ollama',
  atlas_engine: 'Local LM',
  atlas_mlx:    'Local LM',
}

// LOCAL_LM_PROVIDERS — when either local Atlas engine is active, the
// composer shows a single "Local LM" option and this set is used for checks.
const LOCAL_LM_PROVIDERS = new Set<ChatProvider>(['atlas_engine', 'atlas_mlx'])

const STORAGE_ID_KEY  = 'atlasConversationID'
const STORAGE_MSG_KEY = 'atlasChatMessages'

function selectedModelForProvider(config: {
  selectedOpenAIPrimaryModel?: string
  selectedAnthropicModel?: string
  selectedGeminiModel?: string
  selectedOpenRouterModel?: string
  selectedLMStudioModel?: string
  selectedOllamaModel?: string
  selectedAtlasEngineModel?: string
  selectedAtlasMLXModel?: string
}, provider: string): string | null {
  switch (provider) {
    case 'openai':
      return config.selectedOpenAIPrimaryModel?.trim() || null
    case 'anthropic':
      return config.selectedAnthropicModel?.trim() || null
    case 'gemini':
      return config.selectedGeminiModel?.trim() || null
    case 'openrouter':
      return config.selectedOpenRouterModel?.trim() || null
    case 'lm_studio':
      return config.selectedLMStudioModel?.trim() || null
    case 'ollama':
      return config.selectedOllamaModel?.trim() || null
    case 'atlas_engine':
      return config.selectedAtlasEngineModel?.trim() || null
    case 'atlas_mlx':
      return config.selectedAtlasMLXModel?.trim() || null
    default:
      return null
  }
}

// ── Utilities ─────────────────────────────────────────────────────────────────

/** UUID v4 generator that works in both secure (HTTPS) and non-secure (HTTP) contexts.
 *  `uuid()` is only available in secure contexts (HTTPS / localhost).
 *  On plain HTTP (LAN access), we fall back to `crypto.getRandomValues()` which
 *  is available everywhere, including HTTP on Safari and Android browsers. */
function uuid(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  // RFC 4122 v4 UUID via getRandomValues — works on HTTP
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  bytes[6] = (bytes[6] & 0x0f) | 0x40 // version 4
  bytes[8] = (bytes[8] & 0x3f) | 0x80 // variant 10
  const hex = Array.from(bytes).map(b => b.toString(16).padStart(2, '0'))
  return `${hex.slice(0,4).join('')}-${hex.slice(4,6).join('')}-${hex.slice(6,8).join('')}-${hex.slice(8,10).join('')}-${hex.slice(10,16).join('')}`
}

function getConversationID(): string {
  let id = localStorage.getItem(STORAGE_ID_KEY)
  if (!id) { id = uuid(); localStorage.setItem(STORAGE_ID_KEY, id) }
  return id
}

function joinTranscriptParts(...parts: string[]): string {
  return parts
    .map(part => part.trim())
    .filter(Boolean)
    .join(' ')
    .replace(/\s+/g, ' ')
    .trim()
}

function mergeTranscriptIntoInput(base: string, dictated: string): string {
  const transcript = joinTranscriptParts(dictated)
  if (!transcript) return base
  if (!base.trim()) return transcript
  return /[\s\n]$/.test(base) ? `${base}${transcript}` : `${base} ${transcript}`
}

function loadMessages(): Message[] {
  try {
    const raw = localStorage.getItem(STORAGE_MSG_KEY)
    if (!raw) return []
    return (JSON.parse(raw) as Message[]).map(m => ({
      ...m,
      isTyping: false,
      createdAt: typeof m.createdAt === 'number' ? m.createdAt : Number(m.createdAt) || Date.now(),
    }))
  } catch { return [] }
}

function saveMessages(msgs: Message[]) {
  try {
    const toSave = msgs
      .filter(m => (m.content.length > 0 || (m.blocks?.length ?? 0) > 0 || (m.fileAttachments?.length ?? 0) > 0) && !m.isTyping)
      .map(({ id, role, content, createdAt, fileAttachments, mapCards, blocks }) => ({
        id,
        role,
        content,
        createdAt,
        fileAttachments,
        mapCards,
        blocks,
      }))
    localStorage.setItem(STORAGE_MSG_KEY, JSON.stringify(toSave))
  } catch {
    // QuotaExceededError — storage full; skip silently
  }
}

/**
 * Maps a tool name to a calm, human-readable status phrase.
 * The backend already humanizes most names via AgentOrchestrator.humanReadableName;
 * this is a frontend safety net for any raw IDs that slip through.
 */
function humanizeToolName(raw: string): string {
  if (!raw) return 'Working on it…'
  // Already humanized (contains spaces or ends with ellipsis) — pass through
  if (raw.includes(' ') || raw.endsWith('…')) return raw
  if (raw.startsWith('browser.'))                     return 'Browsing…'
  if (raw.startsWith('weather.'))                     return 'Checking the weather…'
  if (raw.startsWith('websearch.'))                   return 'Searching the web…'
  if (raw.startsWith('web.search'))                   return 'Searching the web…'
  if (raw.startsWith('web.'))                         return 'Looking this up…'
  if (raw.startsWith('fs.'))                          return 'Reading files…'
  if (raw.startsWith('file.'))                        return 'Reading files…'
  if (raw.startsWith('terminal.'))                    return 'Running a command…'
  if (raw.startsWith('finance.'))                     return 'Checking the markets…'
  if (raw.startsWith('vault.'))                       return 'Checking credentials…'
  if (raw.startsWith('diary.'))                       return 'Writing to memory…'
  if (raw.startsWith('forge.orchestration.propose'))  return 'Drafting a new skill…'
  if (raw.startsWith('forge.orchestration.plan'))     return 'Planning this out…'
  if (raw.startsWith('forge.orchestration.review'))   return 'Reviewing the plan…'
  if (raw.startsWith('forge.orchestration.validate')) return 'Verifying the details…'
  if (raw.startsWith('forge.'))                       return 'Building that for you…'
  if (raw.startsWith('system.'))                      return 'Running that now…'
  if (raw.startsWith('applescript.'))                 return 'Working in your apps…'
  if (raw.startsWith('gremlin.'))                     return 'Managing automations…'
  if (raw.startsWith('gremlins.'))                    return 'Managing automations…'
  if (raw.startsWith('image.'))                       return 'Generating an image…'
  if (raw.startsWith('vision.'))                      return 'Analyzing the image…'
  if (raw.startsWith('atlas.'))                       return 'Checking Atlas…'
  if (raw.startsWith('info.'))                        return 'Checking that…'
  return 'Working on it…'
}

// ── Timestamp helpers ─────────────────────────────────────────────────────────

function formatTime(ts: number): string {
  return new Date(ts).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })
}

function formatDateLabel(ts: number): string {
  const d   = new Date(ts)
  const now = new Date()
  if (d.toDateString() === now.toDateString()) return 'Today'
  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  if (d.toDateString() === yesterday.toDateString()) return 'Yesterday'
  return d.toLocaleDateString(undefined, {
    month: 'short', day: 'numeric',
    ...(d.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {})
  })
}

function hydrateConversationMessages(messages: ConversationMessage[]): Message[] {
  return messages
    .filter(m => m.role === 'user' || m.role === 'assistant')
    .map(m => ({
      id: m.id,
      role: m.role as 'user' | 'assistant',
      content: m.content,
      createdAt: new Date(m.timestamp).getTime(),
      blocks: Array.isArray(m.blocks) ? m.blocks as MessageBlock[] : undefined,
    }))
}

function mergeHydratedMessages(messages: Message[], existing: Message[]): Message[] {
  const existingByID = new Map(existing.map(msg => [msg.id, msg]))
  return messages.map(msg => {
    const local = existingByID.get(msg.id)
    if (!local) return msg
    return {
      ...msg,
      linkPreviews: local.linkPreviews ?? msg.linkPreviews,
      fileAttachments: local.fileAttachments ?? msg.fileAttachments,
      mapCards: local.mapCards ?? msg.mapCards,
      blocks: local.blocks ?? msg.blocks,
    }
  })
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null
}

function asString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function asNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function asStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return value
    .map(item => asString(item))
    .filter((item): item is string => Boolean(item))
}

function domainFromURL(rawURL: string): string {
  try {
    return new URL(rawURL).hostname.replace(/^www\./, '')
  } catch {
    return rawURL
  }
}

function normalizeSearchResult(value: unknown): SearchResultCardData | null {
  const data = asRecord(value)
  if (!data) return null
  const url = asString(data.url)
  if (!url) return null
  const title = asString(data.resolved_title) ?? asString(data.title) ?? domainFromURL(url)
  return {
    rank: asNumber(data.rank),
    title,
    url,
    domain: asString(data.domain) ?? domainFromURL(url),
    snippet: asString(data.snippet),
    provider: asString(data.provider),
    confidence: asString(data.confidence),
    publishedAt: asString(data.published_at),
    sourceType: asString(data.source_type),
    contentPreview: asString(data.content_preview),
  }
}

function sourceBlockTitle(toolName?: string): string {
  switch (toolName) {
    case 'web.news':
      return 'Recent coverage'
    case 'web.search_latest':
      return 'Latest sources'
    case 'web.search_docs':
      return 'Documentation sources'
    case 'web.search_entities':
      return 'Entity sources'
    case 'web.research':
      return 'Research sources'
    case 'web.multi_search':
      return 'Searches'
    default:
      return 'Sources'
  }
}

function parseMapCard(value: Record<string, unknown>): MapCardData | null {
  const mapType = asString(value.map_type)
  if (!mapType) return null
  return {
    type: mapType as MapCardData['type'],
    latitude: asNumber(value.latitude),
    longitude: asNumber(value.longitude),
    label: asString(value.label),
    origin: asString(value.origin),
    destination: asString(value.destination),
    mode: asString(value.mode),
    distance: asString(value.distance),
    duration: asString(value.duration),
    query: asString(value.query),
    places: Array.isArray(value.places) ? value.places as MapCardData['places'] : undefined,
  }
}

function parseToolBlocks(toolName: string | undefined, rawResult: string | undefined): MessageBlock[] {
  if (!rawResult) return []
  let artifacts: unknown
  try {
    artifacts = JSON.parse(rawResult)
  } catch {
    return []
  }
  const data = asRecord(artifacts)
  if (!data) return []

  const mapCard = parseMapCard(data)
  if (mapCard) {
    return [{ type: 'map', card: mapCard }]
  }

  const queries = Array.isArray(data.queries) ? data.queries : null
  if (queries) {
    const groups = queries
      .map(item => {
        const group = asRecord(item)
        if (!group) return null
        const results = Array.isArray(group.results)
          ? group.results.map(normalizeSearchResult).filter((entry): entry is SearchResultCardData => Boolean(entry))
          : []
        if (results.length === 0) return null
        const normalizedGroup: SourceListGroup = {
          query: asString(group.query),
          provider: asString(group.provider),
          fetchedAt: asString(group.fetched_at),
          results,
        }
        return normalizedGroup
      })
      .filter((entry): entry is SourceListGroup => Boolean(entry))
    return groups.length > 0
      ? [{ type: 'multi-source-list', title: sourceBlockTitle(toolName), groups }]
      : []
  }

  const results = Array.isArray(data.results)
    ? data.results.map(normalizeSearchResult).filter((entry): entry is SearchResultCardData => Boolean(entry))
    : []
  if (results.length > 0) {
    return [{
      type: 'source-list',
      title: sourceBlockTitle(toolName),
      query: asString(data.query),
      provider: asString(data.provider),
      fetchedAt: asString(data.fetched_at),
      results,
    }]
  }

  const source = asRecord(data.source)
  const sourceURL = asString(data.url) ?? asString(source?.url) ?? asString(data.canonical_url)
  const sourceSummary = asString(data.summary) ?? asString(data.preview)
  const sourceTitle = asString(data.title) ?? asString(source?.title)
  const headings = asStringArray(data.headings)
  if (sourceURL && (sourceTitle || sourceSummary || headings.length > 0)) {
    return [{
      type: 'source-summary',
      title: sourceTitle,
      url: sourceURL,
      domain: asString(source?.domain) ?? domainFromURL(sourceURL),
      description: asString(data.description) ?? asString(source?.snippet),
      summary: sourceSummary,
      headings,
      publishedAt: asString(data.published_at) ?? asString(source?.published_at),
      canonicalURL: asString(data.canonical_url) ?? asString(source?.canonical_url),
    }]
  }

  return []
}

function renderBlockList(blocks: MessageBlock[]): JSX.Element | null {
  if (blocks.length === 0) return null
  return (
    <div class="chat-rich-blocks">
      {blocks.map((block, index) => {
        switch (block.type) {
          case 'source-list':
            return <SourceListBlock key={`block-${index}`} block={block} />
          case 'multi-source-list':
            return <MultiSourceListBlock key={`block-${index}`} block={block} />
          case 'source-summary':
            return <SourceSummaryBlock key={`block-${index}`} block={block} />
          case 'map':
            return <MapCard key={`block-${index}`} card={block.card} />
          case 'file':
            return <FileAttachmentCard key={`block-${index}`} file={block.file} />
          default:
            return null
        }
      })}
    </div>
  )
}

function messageRenderableBlocks(message: Message): MessageBlock[] {
  if (message.blocks && message.blocks.length > 0) return message.blocks
  const fallbackBlocks: MessageBlock[] = []
  if (message.mapCards) {
    fallbackBlocks.push(...message.mapCards.map(card => ({ type: 'map', card } satisfies MessageBlock)))
  }
  if (message.fileAttachments) {
    fallbackBlocks.push(...message.fileAttachments.map(file => ({ type: 'file', file } satisfies MessageBlock)))
  }
  return fallbackBlocks
}

// ── URL detection & link previews ──────────────────────────────────────────────

const URL_RE = /https?:\/\/[^\s<>"'()[\]{}]+[^\s<>"'()[\]{}.,!?;:]/g

/**
 * Extracts unique http/https URLs from text (max 3).
 */
function extractURLs(text: string): string[] {
  return Array.from(new Set(text.match(URL_RE) ?? [])).slice(0, 3)
}

/**
 * Renders assistant message content as markdown.
 * - Normalizes mixed HTML (e.g. <br> tags from local models) before parsing
 * - Parses with marked (GFM: tables, autolinks, fenced code)
 * - Sanitizes with DOMPurify before injection
 * - Appends LinkPreviewCards for any URLs that have resolved previews
 */
// stripThoughtTags removes the canonical "[T-NN]" engagement marker from
// displayed text. The marker is load-bearing for the engagement classifier
// (tells the backend which thought the agent surfaced) but must never be
// shown to the user.
//
// Scope is intentionally narrow: only the bracketed marker is stripped.
// Reconstructing grammar after an id has been written mid-sentence is a
// job for the prompt, not a regex — broad post-hoc cleanup does more
// damage than it prevents. The backend prompt teaches the model to put
// the marker at the *end* of the sentence and never name the id in prose.
// The raw msg.content keeps the marker intact for telemetry; only the
// rendered view runs through this helper.
function stripThoughtTags(content: string): string {
  return content
    // The canonical marker, with any leading whitespace so we don't leave
    // an orphaned space behind when it sits at the end of a sentence.
    .replace(/\s*\[T-\d+\]/g, '')
    // Collapse any double spaces the strip left behind.
    .replace(/ {2,}/g, ' ')
    // Collapse " ." / " ," that can appear after removing a trailing tag.
    .replace(/\s+([,.;:?!])/g, '$1')
    .trim()
}

function renderMessageContent(
  content: string,
  linkPreviews: Record<string, LinkPreview> | undefined
): JSX.Element {
  const normalized = stripThoughtTags(content).replace(/<br\s*\/?>/gi, '\n')
  const rawHtml = marked.parse(normalized) as string
  const safeHtml = DOMPurify.sanitize(rawHtml, {
    ADD_ATTR: ['target', 'rel', 'class', 'type', 'title', 'aria-label', 'aria-hidden',
               'width', 'height', 'viewBox', 'fill', 'stroke', 'stroke-width',
               'stroke-linecap', 'stroke-linejoin', 'd', 'x', 'y', 'rx', 'ry',
               'src', 'alt', 'download'],
    FORCE_BODY: false,
    ALLOWED_TAGS: [
      'p', 'br', 'strong', 'b', 'em', 'i', 'code', 'pre', 'a',
      'ul', 'ol', 'li', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
      'table', 'thead', 'tbody', 'tr', 'th', 'td',
      'blockquote', 'hr', 's', 'del', 'span', 'div', 'button',
      'svg', 'path', 'rect', 'circle', 'line', 'polyline', 'polygon',
      'img'
    ]
  })

  const previews = linkPreviews ?? {}
  const previewCards = Object.entries(previews).map(([url, preview]) => (
    <div key={`pv${url}`} class="link-preview-anchor">
      <LinkPreviewCard preview={preview} />
    </div>
  ))

  return (
    <>
      <div class="message-markdown" dangerouslySetInnerHTML={{ __html: safeHtml }} />
      {previewCards}
    </>
  )
}

/**
 * Compact, clickable link preview card anchored below its source URL.
 */
const LinkPreviewCard = ({ preview }: { preview: LinkPreview }) => {
  const domain = preview.domain
    ?? (() => { try { return new URL(preview.url).hostname.replace(/^www\./, '') } catch { return preview.url } })()

  return (
    <a
      href={preview.url}
      target="_blank"
      rel="noopener noreferrer"
      class="link-preview-card"
      onClick={(e) => e.stopPropagation()}
    >
      {preview.imageURL && (
        <img
          src={preview.imageURL}
          class="link-preview-img"
          alt=""
          loading="lazy"
          onError={(e) => { (e.target as HTMLImageElement).style.display = 'none' }}
        />
      )}
      <div class="link-preview-body">
        <span class="link-preview-domain">{domain}</span>
        {preview.title && <span class="link-preview-title">{preview.title}</span>}
        {preview.description && <span class="link-preview-desc">{preview.description}</span>}
      </div>
    </a>
  )
}

function formatPublishedLabel(value?: string): string | null {
  if (!value) return null
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}

const SourceResultCard = ({ result }: { result: SearchResultCardData }) => {
  const publishedLabel = formatPublishedLabel(result.publishedAt)
  return (
    <a
      href={result.url}
      target="_blank"
      rel="noopener noreferrer"
      class="source-card"
      onClick={(e) => e.stopPropagation()}
    >
      <div class="source-card-topline">
        {result.rank != null && <span class="source-card-rank">{result.rank}</span>}
        <span class="source-card-domain">{result.domain ?? domainFromURL(result.url)}</span>
        {publishedLabel && <span class="source-card-meta">{publishedLabel}</span>}
        {result.provider && <span class="source-card-meta">{result.provider}</span>}
      </div>
      <span class="source-card-title">{result.title}</span>
      {result.snippet && <span class="source-card-snippet">{result.snippet}</span>}
      {!result.snippet && result.contentPreview && <span class="source-card-snippet">{result.contentPreview}</span>}
      <div class="source-card-footer">
        {result.sourceType && <span class="source-card-chip">{result.sourceType}</span>}
        {result.confidence && <span class="source-card-chip">{result.confidence} confidence</span>}
      </div>
    </a>
  )
}

const SourceListBlock = ({ block }: { block: Extract<MessageBlock, { type: 'source-list' }> }) => (
  <div class="chat-rich-card source-list-block">
    <div class="chat-rich-card-header">
      <div>
        <span class="chat-rich-card-kicker">{block.title}</span>
        {block.query && <h4 class="chat-rich-card-title">{block.query}</h4>}
      </div>
      {(block.provider || block.fetchedAt) && (
        <span class="chat-rich-card-meta">
          {[block.provider, formatPublishedLabel(block.fetchedAt)].filter(Boolean).join(' · ')}
        </span>
      )}
    </div>
    <div class="source-card-list">
      {block.results.map(result => (
        <SourceResultCard key={`${result.url}-${result.rank ?? 0}`} result={result} />
      ))}
    </div>
  </div>
)

const MultiSourceListBlock = ({ block }: { block: Extract<MessageBlock, { type: 'multi-source-list' }> }) => (
  <div class="chat-rich-card source-list-block">
    <div class="chat-rich-card-header">
      <div>
        <span class="chat-rich-card-kicker">{block.title}</span>
      </div>
    </div>
    <div class="source-group-list">
      {block.groups.map(group => (
        <section key={`${group.query ?? 'group'}-${group.provider ?? ''}`} class="source-group">
          <div class="source-group-header">
            <h4 class="source-group-title">{group.query ?? 'Search'}</h4>
            {(group.provider || group.fetchedAt) && (
              <span class="chat-rich-card-meta">
                {[group.provider, formatPublishedLabel(group.fetchedAt)].filter(Boolean).join(' · ')}
              </span>
            )}
          </div>
          <div class="source-card-list">
            {group.results.map(result => (
              <SourceResultCard key={`${group.query ?? 'group'}-${result.url}-${result.rank ?? 0}`} result={result} />
            ))}
          </div>
        </section>
      ))}
    </div>
  </div>
)

const SourceSummaryBlock = ({ block }: { block: Extract<MessageBlock, { type: 'source-summary' }> }) => (
  <a
    href={block.canonicalURL ?? block.url}
    target="_blank"
    rel="noopener noreferrer"
    class="chat-rich-card source-summary-card"
    onClick={(e) => e.stopPropagation()}
  >
    <div class="chat-rich-card-header">
      <div>
        <span class="chat-rich-card-kicker">Source summary</span>
        <h4 class="chat-rich-card-title">{block.title ?? block.domain ?? block.url}</h4>
      </div>
      <span class="chat-rich-card-meta">
        {[block.domain, formatPublishedLabel(block.publishedAt)].filter(Boolean).join(' · ')}
      </span>
    </div>
    {block.description && <p class="source-summary-description">{block.description}</p>}
    {block.summary && <p class="source-summary-body">{block.summary}</p>}
    {block.headings && block.headings.length > 0 && (
      <div class="source-summary-headings">
        {block.headings.slice(0, 4).map(heading => (
          <span key={heading} class="source-card-chip">{heading}</span>
        ))}
      </div>
    )}
  </a>
)

// ── File attachment card ───────────────────────────────────────────────────────

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function fileIcon(mimeType: string): JSX.Element {
  const isImage = mimeType.startsWith('image/')
  const isPDF   = mimeType === 'application/pdf'
  if (isImage) return (
    <svg width="18" height="18" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="1.5" y="1.5" width="13" height="13" rx="2"/>
      <circle cx="5.5" cy="5.5" r="1.3"/>
      <path d="M1.5 11l3.5-3.5 2.5 2.5 2-2 4.5 4.5"/>
    </svg>
  )
  if (isPDF) return (
    <svg width="18" height="18" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M9.5 1.5H4a1.5 1.5 0 0 0-1.5 1.5v10A1.5 1.5 0 0 0 4 14.5h8a1.5 1.5 0 0 0 1.5-1.5V5.5L9.5 1.5z"/>
      <path d="M9.5 1.5V5.5H13.5"/>
      <path d="M5 9.5h6M5 12h4"/>
    </svg>
  )
  return (
    <svg width="18" height="18" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M9.5 1.5H4a1.5 1.5 0 0 0-1.5 1.5v10A1.5 1.5 0 0 0 4 14.5h8a1.5 1.5 0 0 0 1.5-1.5V5.5L9.5 1.5z"/>
      <path d="M9.5 1.5V5.5H13.5"/>
    </svg>
  )
}

const FileAttachmentCard = ({ file }: { file: FileAttachment }) => {
  const downloadUrl = `/artifacts/${file.fileToken}`
  const isImage = file.mimeType.startsWith('image/')

  if (isImage) {
    return (
      <div class="file-attachment-image" onClick={(e) => e.stopPropagation()}>
        <img
          src={downloadUrl}
          class="file-attachment-image-img"
          alt="Generated image"
          loading="lazy"
          onError={(e) => { (e.target as HTMLImageElement).style.display = 'none' }}
        />
        <a
          href={downloadUrl}
          download={file.filename}
          class="file-attachment-image-dl"
          aria-label="Download image"
          onClick={(e) => e.stopPropagation()}
        >
          <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M8 3v8M5 8l3 3 3-3"/>
            <path d="M3 13h10"/>
          </svg>
        </a>
      </div>
    )
  }

  return (
    <a
      href={downloadUrl}
      download={file.filename}
      target="_blank"
      rel="noopener noreferrer"
      class="file-attachment-card"
      onClick={(e) => e.stopPropagation()}
    >
      <span class="file-attachment-icon">{fileIcon(file.mimeType)}</span>
      <div class="file-attachment-meta">
        <span class="file-attachment-name">{file.filename}</span>
        <span class="file-attachment-size">{formatFileSize(file.fileSize)}</span>
      </div>
      <span class="file-attachment-dl" aria-label="Download">
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M8 3v8M5 8l3 3 3-3"/>
          <path d="M3 13h10"/>
        </svg>
      </span>
    </a>
  )
}

// ── Map card ──────────────────────────────────────────────────────────────────

const TRAVEL_MODE_ICONS: Record<string, JSX.Element> = {
  driving: (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <rect x="1" y="3" width="15" height="13" rx="2"/><path d="M16 8h4l3 6v3h-7V8z"/><circle cx="5.5" cy="18.5" r="2.5"/><circle cx="18.5" cy="18.5" r="2.5"/>
    </svg>
  ),
  walking: (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="12" cy="5" r="2"/><path d="M5 22l2-8 3 2 2-7h4l-2 4 3 1-3 8"/>
    </svg>
  ),
  bicycling: (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="5.5" cy="17.5" r="3.5"/><circle cx="18.5" cy="17.5" r="3.5"/><path d="M15 6h-3l-2 6 3 1 2-7z"/><path d="M5.5 17.5l6-6 7 6"/>
    </svg>
  ),
  transit: (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <rect x="4" y="3" width="16" height="16" rx="2"/><path d="M4 11h16"/><circle cx="8.5" cy="17" r="1.5"/><circle cx="15.5" cy="17" r="1.5"/>
    </svg>
  ),
}

const MapCard = ({ card }: { card: MapCardData }) => {
  if (card.type === 'point' && card.latitude != null && card.longitude != null) {
    const delta = 0.008
    const bbox = `${card.longitude - delta},${card.latitude - delta},${card.longitude + delta},${card.latitude + delta}`
    const embedUrl = `https://www.openstreetmap.org/export/embed.html?bbox=${bbox}&layer=mapnik&marker=${card.latitude},${card.longitude}`
    const openUrl = `https://www.openstreetmap.org/?mlat=${card.latitude}&mlon=${card.longitude}#map=15/${card.latitude}/${card.longitude}`
    const shortLabel = card.label ? card.label.split(',').slice(0, 2).join(',') : `${card.latitude.toFixed(4)}, ${card.longitude.toFixed(4)}`
    return (
      <div class="map-card">
        <div class="map-card-embed">
          <iframe
            src={embedUrl}
            title="Map"
            loading="lazy"
            referrerpolicy="no-referrer"
          />
        </div>
        <div class="map-card-footer">
          <span class="map-card-label" title={card.label}>{shortLabel}</span>
          <a href={openUrl} target="_blank" rel="noopener noreferrer" class="map-card-open">
            Open in Maps
            <svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M7 3H3a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V9"/><path d="M10 2h4v4"/><path d="M14 2L8 8"/>
            </svg>
          </a>
        </div>
      </div>
    )
  }

  if (card.type === 'places' && card.places && card.places.length > 0) {
    const first = card.places[0]
    const delta = 0.012
    const bbox = `${first.longitude - delta},${first.latitude - delta},${first.longitude + delta},${first.latitude + delta}`
    const embedUrl = `https://www.openstreetmap.org/export/embed.html?bbox=${bbox}&layer=mapnik&marker=${first.latitude},${first.longitude}`
    const searchUrl = `https://www.openstreetmap.org/search?query=${encodeURIComponent(card.query ?? card.places[0].name)}`
    return (
      <div class="map-card">
        <div class="map-card-embed">
          <iframe src={embedUrl} title="Map" loading="lazy" referrerpolicy="no-referrer" />
        </div>
        <div class="map-card-footer">
          <span class="map-card-label">{card.places.length} place{card.places.length !== 1 ? 's' : ''} found</span>
          <a href={searchUrl} target="_blank" rel="noopener noreferrer" class="map-card-open">
            Open in Maps
            <svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M7 3H3a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V9"/><path d="M10 2h4v4"/><path d="M14 2L8 8"/>
            </svg>
          </a>
        </div>
      </div>
    )
  }

  if (card.type === 'directions' && card.origin && card.destination) {
    const modeIcon = TRAVEL_MODE_ICONS[card.mode ?? 'driving'] ?? TRAVEL_MODE_ICONS.driving
    const gmUrl = `https://www.google.com/maps/dir/?api=1&origin=${encodeURIComponent(card.origin)}&destination=${encodeURIComponent(card.destination)}&travelmode=${card.mode ?? 'driving'}`
    return (
      <div class="map-card map-card-directions">
        <div class="map-card-directions-body">
          <div class="map-card-route">
            <span class="map-card-origin">{card.origin}</span>
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 8h10M9 4l4 4-4 4"/>
            </svg>
            <span class="map-card-dest">{card.destination}</span>
          </div>
          {(card.distance || card.duration) && (
            <div class="map-card-meta">
              <span class="map-card-mode">{modeIcon}</span>
              {card.distance && <span>{card.distance}</span>}
              {card.duration && <span class="map-card-dot">·</span>}
              {card.duration && <span>{card.duration}</span>}
            </div>
          )}
        </div>
        <a href={gmUrl} target="_blank" rel="noopener noreferrer" class="map-card-open-btn">
          Open in Google Maps
          <svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M7 3H3a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V9"/><path d="M10 2h4v4"/><path d="M14 2L8 8"/>
          </svg>
        </a>
      </div>
    )
  }

  return null
}

// ── Icon components ────────────────────────────────────────────────────────────

const SendIcon = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.3" stroke-linecap="round" stroke-linejoin="round">
    <path d="M8 13V3" />
    <path d="M4 7l4-4 4 4" />
  </svg>
)

const StopIcon = () => (
  <svg width="13" height="13" viewBox="0 0 14 14" fill="currentColor">
    <rect x="2" y="2" width="10" height="10" rx="2" />
  </svg>
)

const MicIcon = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
    <rect x="5.2" y="1.6" width="5.6" height="8.2" rx="2.8" />
    <path d="M3.2 7.9a4.8 4.8 0 0 0 9.6 0" />
    <line x1="8" y1="12.9" x2="8" y2="14.6" />
    <line x1="5.8" y1="14.6" x2="10.2" y2="14.6" />
  </svg>
)

const AttachIcon = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
    <path d="M8 2.5v11" />
    <path d="M2.5 8h11" />
  </svg>
)

const AttachmentChipIcon = ({ mimeType }: { mimeType: string }) => {
  if (mimeType.startsWith('image/')) {
    return (
      <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <rect x="2" y="2" width="12" height="12" rx="2.5" />
        <circle cx="6" cy="6" r="1.1" />
        <path d="M3.5 11l3-3 2.3 2.3 1.7-1.7 2 2.4" />
      </svg>
    )
  }
  if (mimeType === 'application/pdf') {
    return (
      <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M9.5 1.5H4A1.5 1.5 0 0 0 2.5 3v10A1.5 1.5 0 0 0 4 14.5h8A1.5 1.5 0 0 0 13.5 13V5.5L9.5 1.5z" />
        <path d="M9.5 1.5V5.5H13.5" />
        <path d="M4.8 11.5h5.7" />
      </svg>
    )
  }
  return (
    <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M9.5 1.5H4A1.5 1.5 0 0 0 2.5 3v10A1.5 1.5 0 0 0 4 14.5h8A1.5 1.5 0 0 0 13.5 13V5.5L9.5 1.5z" />
      <path d="M9.5 1.5V5.5H13.5" />
    </svg>
  )
}

const CopyIcon = () => (
  <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <rect x="5" y="5" width="9" height="9" rx="1.5" />
    <path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5" />
  </svg>
)

const CheckIcon = () => (
  <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M3 8l4 4 6-7" />
  </svg>
)

/* Waveform equalizer — 4 bars of varying height */
const SpeakerIcon = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
    <line x1="3"    y1="10"   x2="3"    y2="8.5" />
    <line x1="6.5"  y1="12.5" x2="6.5"  y2="3.5" />
    <line x1="10"   y1="11"   x2="10"   y2="5.5" />
    <line x1="13.5" y1="10.5" x2="13.5" y2="7"   />
  </svg>
)

/* Pause bars — shown when audio is actively playing */
const SpeakerStopIcon = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.3" stroke-linecap="round" aria-hidden="true">
    <line x1="5.5"  y1="5" x2="5.5"  y2="11" />
    <line x1="10.5" y1="5" x2="10.5" y2="11" />
  </svg>
)

/* Waveform with diagonal slash — TTS disabled */
const SpeakerMutedIcon = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
    <line x1="3"    y1="10"   x2="3"    y2="8.5" />
    <line x1="6.5"  y1="12.5" x2="6.5"  y2="3.5" />
    <line x1="10"   y1="11"   x2="10"   y2="5.5" />
    <line x1="13.5" y1="10.5" x2="13.5" y2="7"   />
    <line x1="2"    y1="14"   x2="14"   y2="2"   />
  </svg>
)

const ThinkingIcon = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <circle cx="8" cy="7" r="4.5" />
    <path d="M5.8 11.5 5 14" />
    <path d="M10.2 11.5 11 14" />
    <path d="M6 14h4" />
    <path d="M6.5 5.5c.5-.8 1.5-1 2.5-.5" />
  </svg>
)

const AvatarGlyph = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
    <circle cx="8" cy="5.5" r="3" />
    <path d="M2.5 15c0-3 2.5-5.5 5.5-5.5S13.5 12 13.5 15" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" fill="none" />
  </svg>
)

const TypingDots = () => (
  <span class="typing-dots">
    <span /><span /><span />
  </span>
)

// ── InlineApprovalCard ─────────────────────────────────────────────────────────

function InlineApprovalCard({ toolName, args, loading, onApprove, onDeny }: {
  toolName: string
  args: string
  loading: boolean
  onApprove: () => void
  onDeny: () => void
}) {
  const [expanded, setExpanded] = useState(false)

  // Pretty-print the args JSON for display
  let argsDisplay = ''
  try {
    const parsed = JSON.parse(args)
    const keys = Object.keys(parsed)
    if (keys.length === 0) {
      argsDisplay = '(no arguments)'
    } else if (!expanded && keys.length > 0) {
      // Collapsed: show first 2 key=value pairs on one line
      argsDisplay = keys.slice(0, 2).map(k => {
        const v = parsed[k]
        const str = typeof v === 'string' ? v : JSON.stringify(v)
        return `${k}: ${str.length > 60 ? str.slice(0, 60) + '…' : str}`
      }).join('  ·  ') + (keys.length > 2 ? `  +${keys.length - 2} more` : '')
    } else {
      argsDisplay = JSON.stringify(parsed, null, 2)
    }
  } catch {
    argsDisplay = args
  }

  const hasArgs = args && args !== '{}'

  return (
    <div class="chat-approval-card">
      <div class="chat-approval-card-row">
        <svg class="chat-approval-card-icon" width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="8" cy="8" r="6.5" />
          <path d="M8 5v3.5l2 1.5" />
        </svg>
        <div class="chat-approval-card-meta">
          <span class="chat-approval-card-title">Approval required</span>
          <span class="chat-approval-card-tool">{humanizeToolName(toolName)}</span>
        </div>
        <div class="chat-approval-card-controls">
          {hasArgs && (
            <button class="btn btn-sm chat-approval-details-btn" onClick={() => setExpanded(e => !e)}>
              {expanded ? 'Hide' : 'Details'}
            </button>
          )}
          <button class="btn btn-sm chat-approval-deny-btn" onClick={onDeny} disabled={loading}>Deny</button>
          <button class="btn btn-sm chat-approval-approve-btn" onClick={onApprove} disabled={loading}>
            {loading ? 'Waiting…' : 'Approve'}
          </button>
        </div>
      </div>
      {hasArgs && expanded && (
        <pre class="chat-approval-card-args">{argsDisplay}</pre>
      )}
    </div>
  )
}

// ── Chat component ─────────────────────────────────────────────────────────────

export function Chat({ isActive = true, onUnreadReply }: {
  isActive?: boolean
  onUnreadReply?: () => void
} = {}) {
  const [messages, setMessages]               = useState<Message[]>(loadMessages)
  const [input, setInput]                     = useState('')
  const [sending, setSending]                 = useState(false)
  const [pendingApproval, setPendingApproval] = useState<{ toolCallID: string; toolName: string; args: string } | null>(null)
  const [approvingAction, setApprovingAction] = useState(false)
  const [error, setError]                     = useState<string | null>(null)
  const [attachments, setAttachments]         = useState<MessageAttachment[]>([])
  const [agentName, setAgentName]             = useState('Atlas')
  const [userName, setUserName]               = useState('')
  const [speechAvailable]                     = useState(() => voiceSpeechSupported())
  const [speechListening, setSpeechListening] = useState(false)
  const [activeAudioProvider, setActiveAudioProvider] = useState<string>('local')
  const [ttsEnabled, setTtsEnabled]           = useState<boolean>(() => {
    try { return localStorage.getItem('atlas.ttsEnabled') === '1' } catch { return false }
  })
  const [speakingMsgId, setSpeakingMsgId]     = useState<string | null>(null)
  const [activeProvider, setActiveProvider]   = useState<ChatProvider>('openai')
  // Tracks which local engine is configured (atlas_engine or atlas_mlx) so the
  // "Local LM" dropdown option can resolve to the right backend.
  const [selectedLocalEngine, setSelectedLocalEngine] = useState<ChatProvider>('atlas_engine')
  const [modelByProvider, setModelByProvider] = useState<Record<ChatProvider, string>>({
    openai:    '',
    anthropic: '',
    gemini:    '',
    openrouter: '',
    lm_studio:    '',
    ollama:       '',
    atlas_engine: '',
    atlas_mlx:    '',
  })
  const [cloudModelHealth, setCloudModelHealth] = useState<CloudModelHealth | null>(null)
  const [checkingCloudModelHealth, setCheckingCloudModelHealth] = useState(false)
  // MLX thinking toggle — only shown when the active MLX model supports thinking
  const [mlxHasThinking, setMlxHasThinking]   = useState(false)
  const [thinkingEnabled, setThinkingEnabled]  = useState(false)
  const [showScrollBottom, setShowScrollBottom] = useState(false)

  // History search state
  const [historyOpen, setHistoryOpen]           = useState(false)
  const [historyDropdownVisible, setHistoryDropdownVisible] = useState(false)
  const [historyQuery, setHistoryQuery]         = useState('')
  const [historySummaries, setHistorySummaries] = useState<ConversationSummary[]>([])
  const [historyLoading, setHistoryLoading]     = useState(false)
  const [pendingClearHistory, setPendingClearHistory] = useState(false)
  const historySearchRef                        = useRef<HTMLInputElement>(null)
  const historyDebounceRef                      = useRef<ReturnType<typeof setTimeout> | null>(null)
  const historyContainerRef                     = useRef<HTMLDivElement>(null)
  const [copyFeedback, setCopyFeedback]         = useState<{ id: string; status: 'copied' | 'failed' } | null>(null)
  const copyFeedbackTimer                       = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [revealedCopyId, setRevealedCopyId]     = useState<string | null>(null)
  const [promptIndex, setPromptIndex]           = useState(0)
  // Drag-and-drop
  const [dragOver, setDragOver]                 = useState(false)
  const dragCounterRef                          = useRef(0)
  // Proactive message composing indicator (background SSE turn in progress)
  const [proactiveComposing, setProactiveComposing] = useState(false)
  const hasActiveAssistantOutput = useMemo(
    () => proactiveComposing || sending || messages.some((msg) => msg.role === 'assistant' && msg.isTyping),
    [messages, proactiveComposing, sending],
  )

  const PROMPTS = [
    'Help me draft an email',
    'Summarize a document',
    'Write some code',
    'Search the web for me',
    'Set a reminder for me',
    'What\'s the weather like?',
  ]

  const appendBlocksToMessage = useCallback((messageID: string, blocks: MessageBlock[]) => {
    if (blocks.length === 0) return
    setMessages(prev => prev.map(message =>
      message.id === messageID
        ? { ...message, blocks: [...(message.blocks ?? []), ...blocks] }
        : message
    ))
  }, [])

  const appendFileToMessage = useCallback((messageID: string, attachment: FileAttachment) => {
    setMessages(prev => prev.map(message =>
      message.id === messageID
        ? {
            ...message,
            fileAttachments: [...(message.fileAttachments ?? []), attachment],
            blocks: [...(message.blocks ?? []), { type: 'file', file: attachment }],
          }
        : message
    ))
  }, [])

  useEffect(() => {
    if (messages.length > 0) return
    const t = setInterval(() => setPromptIndex(i => i + 1), 3500)
    return () => clearInterval(t)
  }, [messages.length])


  // activeMsgId: tracks which assistant bubble is the active one this turn.
  // Used to keep typing dots visible even after assistant_done fires (tool-only turns
  // produce no text, so assistant_done fires before tools run, yet the turn continues).
  const activeMsgId        = useRef<string | null>(null)
  const activeTurnIdRef    = useRef<string | null>(null)
  // bgActiveMsgIdRef tracks the typing bubble created by the background SSE so
  // it can be cleaned up independently of the foreground bubble.
  const bgActiveMsgIdRef   = useRef<string | null>(null)
  // foregroundActiveRef is set true the moment the user initiates a send,
  // closing the race window before activeTurnIdRef is populated from the stream.
  const foregroundActiveRef = useRef(false)

  // Code block copy — event-delegated so it works on DOMPurify-rendered HTML
  const handleCodeCopy = useCallback((e: MouseEvent) => {
    const btn = (e.target as HTMLElement).closest('.code-copy-btn') as HTMLButtonElement | null
    if (!btn) return
    e.stopPropagation()
    const code = btn.closest('.code-block')?.querySelector('code')?.textContent ?? ''
    const origHTML = btn.innerHTML
    navigator.clipboard.writeText(code).then(() => {
      btn.innerHTML = `<svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 8l4 4 6-7"/></svg>`
      setTimeout(() => { if (btn) btn.innerHTML = origHTML }, 2000)
    }).catch(() => {})
  }, [])

  // Code block "Run in terminal" — fills composer with the command pre-filled.
  // The user reviews and sends; we never auto-fire shell commands.
  const handleRunCode = useCallback((e: MouseEvent) => {
    const btn = (e.target as HTMLElement).closest('.code-run-btn') as HTMLButtonElement | null
    if (!btn) return
    e.stopPropagation()
    const encoded = btn.getAttribute('data-run-code') ?? ''
    try {
      const code = decodeURIComponent(encoded)
      setInput(`Run this command in the terminal:\n\`\`\`bash\n${code}\n\`\`\``)
      setTimeout(() => {
        textareaRef.current?.focus()
        resizeTextarea()
      }, 0)
    } catch { /* ignore */ }
  }, [])

  const bottomRef      = useRef<HTMLDivElement>(null)
  const messagesRef    = useRef<HTMLDivElement>(null)
  const esRef          = useRef<EventSource | null>(null)
  const textareaRef    = useRef<HTMLTextAreaElement>(null)
  const fileInputRef   = useRef<HTMLInputElement>(null)
  const conversationID = useRef<string>(getConversationID())
  const isInitialMount = useRef(true)
  const speechSessionRef = useRef<VoiceSpeechSession | null>(null)
  const speechBaseInputRef = useRef('')
  const speechCommittedRef = useRef('')
  const voicePlayerRef = useRef<VoicePlayer | null>(null)
  const voiceStreamAbortRef = useRef<(() => void) | null>(null)
  // Streaming-speaker state — used by auto-play during a chat turn.
  // streamingPlayerRef is a SHARED player that all sentences fire into so
  // playback is gapless across sentence boundaries. streamingBufferRef
  // accumulates raw markdown deltas; sentences are popped from it as soon
  // as a sentence terminator (.!?) appears.
  const streamingPlayerRef     = useRef<VoicePlayer | null>(null)
  const streamingBufferRef     = useRef<string>('')
  const streamingPendingRef    = useRef<number>(0) // in-flight synth requests
  const streamingFinishedRef   = useRef<boolean>(false) // model done emitting deltas
  const streamingMsgIdRef      = useRef<string | null>(null)
  const streamingAbortsRef     = useRef<Array<() => void>>([])
  // Sentence ordering — ensures chunks from parallel cloud TTS requests are
  // enqueued into the ring buffer in sentence order, not network-arrival order.
  const streamingOrderRef      = useRef<number>(0)
  const streamingNextEnqRef    = useRef<number>(0)
  const streamingBufsRef       = useRef<Map<number, { chunks: Array<{ b64: string; idx: number; sr: number }>; done: boolean }>>(new Map())

  // Unread-reply tracking for the sidebar notification dot.
  // isActiveRef — always current, used in stale-closure contexts (SSE handler).
  // onUnreadReplyRef — always-current prop ref so async SSE closures never go stale.
  // lastSeenMsgIdRef — ID of the last message visible when user left chat.
  // prevIsActiveRef — detects false→true / true→false transitions.
  const isActiveRef        = useRef(isActive)
  isActiveRef.current      = isActive
  const onUnreadReplyRef   = useRef(onUnreadReply)
  onUnreadReplyRef.current = onUnreadReply
  const lastSeenMsgIdRef  = useRef<string | null>(null)
  const prevIsActiveRef   = useRef(isActive)
  // Always-current snapshot of messages — used in the isActive leave/return
  // effect so we can read state without adding messages as a dependency.
  const messagesLiveRef   = useRef(messages)
  messagesLiveRef.current = messages

  const updateScrollBottomVisibility = useCallback(() => {
    const el = messagesRef.current
    if (!el) return
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    setShowScrollBottom(distance > 140)
  }, [])

  const scrollToBottom = (smooth: boolean) => {
    requestAnimationFrame(() => {
      const el = messagesRef.current
      if (!el) return
      if (smooth) {
        el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
        window.setTimeout(updateScrollBottomVisibility, 220)
      } else {
        el.scrollTop = el.scrollHeight
        updateScrollBottomVisibility()
      }
    })
  }

  useEffect(() => {
    saveMessages(messages)
    scrollToBottom(!isInitialMount.current)
    isInitialMount.current = false
  }, [messages])

  useEffect(() => {
    const el = messagesRef.current
    if (!el) return
    const onScroll = () => updateScrollBottomVisibility()
    el.addEventListener('scroll', onScroll, { passive: true })
    onScroll()
    return () => el.removeEventListener('scroll', onScroll)
  }, [updateScrollBottomVisibility])

  // Mind-thoughts presence state — tracks how many active thoughts Atlas
  // has on its mind so we can render the subdued "Atlas was thinking…"
  // line at the end of the chat thread. Refreshed on chat-open and after
  // the greeting fires (which might produce new thoughts via a nap).
  const [thoughtCount, setThoughtCount] = useState(0)

  // presencePhrase picks one line from the phrase library, seeded by
  // (day-of-year, thoughtCount). Stable within a session — reloading
  // the same day with the same count shows the same line — drifts
  // across days and when the count changes. See presence_phrases.ts.
  const presencePhrase = useMemo(
    () => pickPresencePhrase(thoughtCount, Date.now()),
    [thoughtCount],
  )

  // Mind-thoughts greeting (phase 5/6). On every chat-open, check whether
  // the pending-greetings queue has anything waiting. If so, fire the
  // greeting endpoint — it drains the queue, runs a one-shot agent turn,
  // persists the reply as an assistant message on the active conversation,
  // and streams it via SSE so the normal message handler picks it up.
  // The sidebar dot clears automatically on the next 5-second poll tick.
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const pending = await api.pendingGreetings()
        if (!cancelled && pending.count > 0) {
          await api.triggerGreeting(conversationID.current)
          // The streamed greeting arrives via the existing SSE connection.
        }
        // Always refresh the thought count — even if no greeting fired
        // there may be active thoughts we should surface as a presence line.
        const thoughts = await api.mindThoughts().catch(() => ({ count: 0 }))
        if (!cancelled) setThoughtCount(thoughts.count ?? 0)
      } catch {
        /* daemon may not be running; silent fail is fine */
      }
    })()
    return () => { cancelled = true }
  }, [])

  // Persistent background SSE — stays open between turns so automation and
  // workflow results injected by the backend (platform="webchat") stream in
  // live without a user-initiated message. Events arriving while a regular
  // turn is in progress are silently dropped (esRef.current is occupied).
  const pushEsRef = useRef<EventSource | null>(null)
  useEffect(() => {
    const convID = conversationID.current
    if (!convID) return

    const open = () => {
      const es = new EventSource(`/message/stream?conversationID=${encodeURIComponent(convID)}`)
      pushEsRef.current = es

      es.onmessage = (evt) => {
        try {
          const data = JSON.parse(evt.data) as ChatStreamEvent
          // Skip events that belong to an active foreground turn.
          // foregroundActiveRef is set before the SSE opens, closing the race window
          // where activeTurnIdRef has not yet been populated from the first event.
          if (foregroundActiveRef.current) {
            const fgTurnID = activeTurnIdRef.current
            if (!fgTurnID || data.turnID === fgTurnID) return
          }
          if (data.type === 'assistant_started') {
            setProactiveComposing(false)
            const msg: Message = { id: uuid(), role: 'assistant', content: '', isTyping: true, createdAt: Date.now() }
            bgActiveMsgIdRef.current = msg.id
            activeMsgId.current = msg.id
            setMessages(prev => [...prev, msg])
          } else if (data.type === 'assistant_delta') {
            const delta = data.content ?? ''
            setMessages(prev => prev.map(m =>
              m.id === bgActiveMsgIdRef.current
                ? { ...m, content: m.content + delta, isTyping: true }
                : m
            ))
          } else if (data.type === 'assistant_done') {
            setMessages(prev => prev.map(m =>
              m.id === bgActiveMsgIdRef.current ? { ...m, isTyping: false } : m
            ))
            if (activeMsgId.current === bgActiveMsgIdRef.current) activeMsgId.current = null
            bgActiveMsgIdRef.current = null
            setProactiveComposing(false)
          } else if (data.type === 'done') {
            // Clean up an empty typing bubble left by a tool-only or silent turn.
            const bgID = bgActiveMsgIdRef.current
            if (bgID) {
              setMessages(prev => prev.map(m =>
                m.id === bgID ? { ...m, isTyping: false } : m
              ).filter(m => !(m.id === bgID && !m.content && !(m.blocks?.length ?? 0))))
              if (activeMsgId.current === bgID) activeMsgId.current = null
              bgActiveMsgIdRef.current = null
            }
          } else if (data.type === 'tool_finished') {
            const blocks = parseToolBlocks(data.toolName, data.result)
            if (blocks.length > 0) {
              setMessages(prev => {
                const last = [...prev].reverse().find((m: Message) => m.role === 'assistant')
                if (!last) return prev
                return prev.map(m =>
                  m.id === last.id
                    ? { ...m, blocks: [...(m.blocks ?? []), ...blocks] }
                    : m
                )
              })
            }
          } else if (data.type === 'file_generated' && data.fileToken && data.filename) {
            const attachment: FileAttachment = {
              filename:  data.filename,
              mimeType:  data.mimeType ?? 'application/octet-stream',
              fileSize:  data.fileSize ?? 0,
              fileToken: data.fileToken,
            }
            setMessages(prev => {
              const last = [...prev].reverse().find((m: Message) => m.role === 'assistant')
              if (!last) return prev
              return prev.map(m =>
                m.id === last.id
                  ? {
                      ...m,
                      fileAttachments: [...(m.fileAttachments ?? []), attachment],
                      blocks: [...(m.blocks ?? []), { type: 'file', file: attachment }],
                    }
                  : m
              )
            })
          }
        } catch { /* malformed event */ }
      }

      es.onerror = () => {
        // Clean up any typing bubble this connection was actively streaming when
        // the server closed it (Finish() fires after every turn).
        const bgID = bgActiveMsgIdRef.current
        if (bgID) {
          setMessages(prev => prev.map(m =>
            m.id === bgID ? { ...m, isTyping: false } : m
          ).filter(m => !(m.id === bgID && !m.content && !(m.blocks?.length ?? 0))))
          if (activeMsgId.current === bgID) activeMsgId.current = null
          bgActiveMsgIdRef.current = null
        }
        es.close()
        pushEsRef.current = null
        // Reopen after 1 s to survive daemon restarts and post-turn close.
        window.setTimeout(open, 1000)
      }
    }

    open()
    return () => {
      pushEsRef.current?.close()
      pushEsRef.current = null
    }
  }, [])

  // On mount, sync messages from the server. If the agent completed a turn
  // while the page was refreshed (client was disconnected from SSE), the
  // response is in SQLite but not in localStorage. Fetching here ensures the
  // user sees the completed reply when they return.
  useEffect(() => {
    let cancelled = false
    const convID = conversationID.current
    if (!convID) return
    ;(async () => {
      try {
        const detail = await api.conversationDetail(convID)
        if (cancelled) return
        const serverMsgs = hydrateConversationMessages(detail.messages)
        // Only update if the server has more messages than what's cached locally.
        // This avoids clobbering an active in-progress turn or a fresh session.
        setMessages(prev => {
          if (serverMsgs.length > prev.filter(m => !m.isTyping).length) {
            return mergeHydratedMessages(serverMsgs, prev)
          }
          return prev
        })
      } catch {
        // Server unreachable — localStorage is the fallback
      }
    })()
    return () => { cancelled = true }
  }, [])

  // Track isActive transitions:
  //   true→false: snapshot the last visible message ID as "seen"
  //   false→true: scroll to the first unread message (SSE keeps state live while
  //               away — no server re-sync needed here; mount-time sync covers refresh)
  //               and fire any pending greeting (fire-and-forget; SSE delivers it).
  useEffect(() => {
    const wasActive = prevIsActiveRef.current
    prevIsActiveRef.current = isActive

    if (wasActive && !isActive) {
      // User left chat — snapshot the last visible message as "seen"
      const visible = messagesLiveRef.current.filter(m => !m.isTyping)
      if (visible.length > 0) lastSeenMsgIdRef.current = visible[visible.length - 1].id
      return
    }

    if (!wasActive && isActive) {
      // User returned to chat.
      const seenId = lastSeenMsgIdRef.current
      lastSeenMsgIdRef.current = null

      // Scroll to first unread message if new messages arrived via SSE while away.
      // Chat is always mounted, so SSE continuously updates messages state — by the
      // time this effect fires the new messages are already in messagesLiveRef.
      requestAnimationFrame(() => {
        if (seenId) {
          const msgs = messagesLiveRef.current.filter(m => !m.isTyping)
          const seenIdx = msgs.findIndex(m => m.id === seenId)
          if (seenIdx >= 0 && seenIdx < msgs.length - 1) {
            const firstNewId = msgs[seenIdx + 1].id
            const el = messagesRef.current?.querySelector(`[data-msg-id="${firstNewId}"]`)
            if (el) {
              el.scrollIntoView({ behavior: 'smooth', block: 'start' })
              return
            }
          }
        }
        scrollToBottom(true)
      })

      // Fire any pending proactive greeting (fire-and-forget; SSE delivers the reply).
      const convID = conversationID.current
      if (convID) {
        api.pendingGreetings()
          .then(p => { if (p.count > 0) api.triggerGreeting(convID).catch(() => {}) })
          .catch(() => {})
      }
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isActive])

  // Scroll to bottom on mount (page load or tab switch back)
  useEffect(() => {
    scrollToBottom(false)
    return () => {
      esRef.current?.close()
      activeTurnIdRef.current = null
      speechSessionRef.current?.stop()
      if (voiceStreamAbortRef.current) {
        try { voiceStreamAbortRef.current() } catch { /* ignore */ }
      }
      if (voicePlayerRef.current) {
        try { voicePlayerRef.current.stop() } catch { /* ignore */ }
      }
      if (copyFeedbackTimer.current) clearTimeout(copyFeedbackTimer.current)
    }
  }, [])

  useEffect(() => {
    const handlePointerDown = (e: MouseEvent | TouchEvent) => {
      const target = e.target as HTMLElement | null
      if (target?.closest('.chat-bubble-wrap, .chat-message-meta')) return
      setRevealedCopyId(null)
    }
    document.addEventListener('mousedown', handlePointerDown)
    document.addEventListener('touchstart', handlePointerDown)
    return () => {
      document.removeEventListener('mousedown', handlePointerDown)
      document.removeEventListener('touchstart', handlePointerDown)
    }
  }, [])

  // ⌘K / Ctrl+K — focus the chat input from anywhere
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        textareaRef.current?.focus()
      }
    }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [])

  const resolveModelLabel = useCallback(async (provider: ChatProvider, selectedModel?: string | null) => {
    const explicitModel = selectedModel?.trim()
    if (explicitModel) {
      setModelByProvider((current) => ({ ...current, [provider]: explicitModel }))
      return
    }

    try {
      const info = await api.modelsForProvider(provider)
      const resolvedPrimary = info.primaryModel?.trim()
      if (resolvedPrimary) {
        setModelByProvider((current) => ({ ...current, [provider]: resolvedPrimary }))
      }
    } catch {
      // Leave the current value alone if the provider cannot be queried right now.
    }
  }, [])

  useEffect(() => {
    api.config().then(async (s) => {
      if (s.personaName) setAgentName(s.personaName)
      if (s.userName) setUserName(s.userName)
      if (s.activeAIProvider) setActiveProvider(s.activeAIProvider as ChatProvider)
      if (s.selectedLocalEngine) setSelectedLocalEngine(s.selectedLocalEngine as ChatProvider)
      if (s.activeAudioProvider) setActiveAudioProvider(s.activeAudioProvider)
      setModelByProvider({
        openai:    s.selectedOpenAIPrimaryModel?.trim() || '',
        anthropic: s.selectedAnthropicModel?.trim() || '',
        gemini:    s.selectedGeminiModel?.trim() || '',
        openrouter: s.selectedOpenRouterModel?.trim() || '',
        lm_studio:    s.selectedLMStudioModel?.trim() || '',
        ollama:       s.selectedOllamaModel?.trim() || '',
        atlas_engine: s.selectedAtlasEngineModel?.trim() || '',
        atlas_mlx:    s.selectedAtlasMLXModel?.trim() || '',
      })
      setThinkingEnabled(!!s.atlasMLXThinkingEnabled)
      const provider = (s.activeAIProvider || 'openai') as ChatProvider
      await resolveModelLabel(provider, selectedModelForProvider(s, provider))
    }).catch(() => {})
  }, [resolveModelLabel])

  // Detect whether the currently-selected MLX model supports thinking.
  // Re-runs when the provider or selected MLX model changes.
  useEffect(() => {
    const isMLX = activeProvider === 'atlas_mlx'
      || (activeProvider === 'local_lm' as ChatProvider && selectedLocalEngine === 'atlas_mlx')
    if (!isMLX) { setMlxHasThinking(false); return }
    const modelName = modelByProvider['atlas_mlx']
    if (!modelName) { setMlxHasThinking(false); return }
    api.mlxModels().then((models) => {
      const info = models.find(m => m.name === modelName)
      setMlxHasThinking(!!info?.capabilities?.hasThinking)
    }).catch(() => setMlxHasThinking(false))
  }, [activeProvider, selectedLocalEngine, modelByProvider])

  const activeCloudModel = CLOUD_CHAT_PROVIDERS.includes(activeProvider)
    ? (modelByProvider[activeProvider]?.trim() || (activeProvider === 'openrouter' ? 'openrouter/auto:free' : ''))
    : ''

  useEffect(() => {
    if (!CLOUD_CHAT_PROVIDERS.includes(activeProvider) || !activeCloudModel) {
      setCloudModelHealth(null)
      setCheckingCloudModelHealth(false)
      return
    }
    let cancelled = false
    setCheckingCloudModelHealth(true)
    api.cloudModelHealth(activeProvider, activeCloudModel)
      .then((health) => { if (!cancelled) setCloudModelHealth(health) })
      .catch(() => {
        if (!cancelled) {
          setCloudModelHealth({
            status: 'unavailable',
            message: 'Could not check model availability.',
            checkedAt: new Date().toISOString(),
          })
        }
      })
      .finally(() => { if (!cancelled) setCheckingCloudModelHealth(false) })
    return () => { cancelled = true }
  }, [activeProvider, activeCloudModel])

  // Click-outside handler for search dropdown
  useEffect(() => {
    if (!historyOpen) return
    const handler = (e: MouseEvent) => {
      if (historyContainerRef.current && !historyContainerRef.current.contains(e.target as Node)) {
        setHistoryOpen(false)
        setHistoryDropdownVisible(false)
        setHistoryQuery('')
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [historyOpen])

  // Load conversation list whenever search opens
  useEffect(() => {
    if (!historyOpen) return
    setHistoryQuery('')
    setHistoryLoading(true)
    api.conversations(50, 0)
      .then(setHistorySummaries)
      .catch(() => setHistorySummaries([]))
      .finally(() => setHistoryLoading(false))
  }, [historyOpen])

  // Debounced search
  useEffect(() => {
    if (!historyOpen) return
    if (historyDebounceRef.current) clearTimeout(historyDebounceRef.current)
    if (!historyQuery.trim()) {
      setHistoryLoading(true)
      api.conversations(50, 0)
        .then(setHistorySummaries)
        .catch(() => setHistorySummaries([]))
        .finally(() => setHistoryLoading(false))
      return
    }
    historyDebounceRef.current = setTimeout(() => {
      setHistoryLoading(true)
      api.searchConversations(historyQuery.trim())
        .then(setHistorySummaries)
        .catch(() => setHistorySummaries([]))
        .finally(() => setHistoryLoading(false))
    }, 280)
    return () => { if (historyDebounceRef.current) clearTimeout(historyDebounceRef.current) }
  }, [historyQuery, historyOpen])

  const resumeConversation = async (id: string) => {
    localStorage.setItem(STORAGE_ID_KEY, id)
    localStorage.removeItem(STORAGE_MSG_KEY)
    conversationID.current = id
    setError(null)
    setPendingApproval(null)
    setHistoryDropdownVisible(false)
    setAttachments([])
    activeMsgId.current = null
    activeTurnIdRef.current = null
    setHistoryOpen(false)
    setHistoryQuery('')
    try {
      const detail: ConversationDetail = await api.conversationDetail(id)
      const loaded = mergeHydratedMessages(hydrateConversationMessages(detail.messages), loadMessages())
      setMessages(loaded)
    } catch (err) {
      setMessages([])
      setPendingApproval(null)
      setError(err instanceof Error ? err.message : 'Failed to load conversation.')
    }
  }

  const reconcileConversationState = useCallback(async (convID: string) => {
    try {
      const detail = await api.conversationDetail(convID)
      const loaded = hydrateConversationMessages(detail.messages)
      let merged = loaded
      setMessages(prev => {
        merged = mergeHydratedMessages(loaded, prev)
        return merged
      })
      return merged
    } catch {
      return null
    }
  }, [])

  const handleProviderChange = async (rawProvider: string) => {
    // "local_lm" is the virtual dropdown value — resolve to the configured local engine.
    const provider = (rawProvider === 'local_lm' ? selectedLocalEngine : rawProvider) as ChatProvider
    const previousProvider = activeProvider
    setActiveProvider(provider)
    await resolveModelLabel(provider, modelByProvider[provider])
    try {
      await api.updateConfig({ activeAIProvider: provider })
    } catch {
      setActiveProvider(previousProvider)
    }
  }

  const syncSpeechInput = useCallback((interimText = '') => {
    const dictated = joinTranscriptParts(speechCommittedRef.current, interimText)
    setInput(mergeTranscriptIntoInput(speechBaseInputRef.current, dictated))
    requestAnimationFrame(resizeTextarea)
  }, [])

  const stopSpeechInput = useCallback(() => {
    speechSessionRef.current?.stop()
  }, [])

  const stopSpeaking = useCallback(() => {
    // Single-shot path (per-message Speak button)
    if (voiceStreamAbortRef.current) {
      try { voiceStreamAbortRef.current() } catch { /* ignore */ }
      voiceStreamAbortRef.current = null
    }
    if (voicePlayerRef.current) {
      try { voicePlayerRef.current.stop() } catch { /* ignore */ }
      voicePlayerRef.current = null
    }
    // Streaming-speaker path (auto-play during a turn)
    for (const abort of streamingAbortsRef.current) {
      try { abort() } catch { /* ignore */ }
    }
    streamingAbortsRef.current = []
    if (streamingPlayerRef.current) {
      try { streamingPlayerRef.current.stop() } catch { /* ignore */ }
      streamingPlayerRef.current = null
    }
    streamingBufferRef.current = ''
    streamingPendingRef.current = 0
    streamingFinishedRef.current = false
    streamingMsgIdRef.current = null
    streamingOrderRef.current = 0
    streamingNextEnqRef.current = 0
    streamingBufsRef.current.clear()
    setSpeakingMsgId(null)
  }, [])

  // ── Streaming TTS helpers ──────────────────────────────────────────────────
  // Strip markdown so the synthesizer doesn't try to pronounce backticks/asterisks.
  const cleanForSpeech = (text: string): string =>
    text
      .replace(/```[\s\S]*?```/g, '')
      .replace(/`([^`]+)`/g, '$1')
      .replace(/!\[[^\]]*]\([^)]*\)/g, '')
      .replace(/\[([^\]]+)]\([^)]*\)/g, '$1')
      .replace(/[#*_>]+/g, '')
      .replace(/\s+/g, ' ')
      .trim()

  // Pop the longest sentence-terminated prefix off the buffer. Returns ''
  // if no sentence boundary is present yet. Sentence boundaries are .!?
  // followed by whitespace or end-of-buffer. Code fences and inline code
  // pause sentence splitting so we don't break mid-snippet.
  const popReadySentences = (buffer: string): { ready: string; rest: string } => {
    // Don't split inside an unclosed code fence — wait for the closing ```.
    const fenceCount = (buffer.match(/```/g) || []).length
    if (fenceCount % 2 === 1) {
      return { ready: '', rest: buffer }
    }
    // Find the last sentence terminator followed by whitespace.
    // Walk backwards so we capture as much as possible per flush.
    let lastBoundary = -1
    for (let i = buffer.length - 1; i >= 0; i--) {
      const c = buffer[i]
      if (c === '.' || c === '!' || c === '?') {
        // Boundary if next char is whitespace or end of string.
        if (i === buffer.length - 1 || /\s/.test(buffer[i + 1])) {
          lastBoundary = i
          break
        }
      }
    }
    if (lastBoundary < 0) return { ready: '', rest: buffer }
    return {
      ready: buffer.slice(0, lastBoundary + 1).trim(),
      rest:  buffer.slice(lastBoundary + 1).trimStart(),
    }
  }

  // Lazily create the shared streaming player on first use.
  const ensureStreamingPlayer = (messageId: string): VoicePlayer | null => {
    if (streamingPlayerRef.current) return streamingPlayerRef.current
    let player: VoicePlayer
    try { player = createVoicePlayer() }
    catch (err) {
      setError(err instanceof Error ? err.message : 'Audio playback unavailable.')
      return null
    }
    streamingPlayerRef.current = player
    streamingMsgIdRef.current = messageId
    setSpeakingMsgId(messageId)
    player.onFinished = () => {
      if (streamingPlayerRef.current === player) {
        streamingPlayerRef.current = null
        streamingBufferRef.current = ''
        streamingPendingRef.current = 0
        streamingFinishedRef.current = false
        streamingMsgIdRef.current = null
        streamingAbortsRef.current = []
        streamingOrderRef.current = 0
        streamingNextEnqRef.current = 0
        streamingBufsRef.current.clear()
        setSpeakingMsgId(null)
      }
    }
    player.onError = (msg) => {
      setError(msg)
      if (streamingPlayerRef.current === player) {
        try { player.stop() } catch { /* ignore */ }
        streamingPlayerRef.current = null
        streamingBufferRef.current = ''
        streamingPendingRef.current = 0
        streamingFinishedRef.current = false
        streamingMsgIdRef.current = null
        streamingAbortsRef.current = []
        streamingOrderRef.current = 0
        streamingNextEnqRef.current = 0
        streamingBufsRef.current.clear()
        setSpeakingMsgId(null)
      }
    }
    return player
  }

  // Fire one sentence into the shared player. Chunks are buffered locally
  // and flushed to the ring buffer only after all earlier sentences have been
  // fully enqueued — this prevents cloud-provider network jitter from
  // interleaving chunks from different sentences in the ring buffer.
  const speakSentence = (sentence: string, player: VoicePlayer) => {
    const text = cleanForSpeech(sentence)
    if (!text) return
    streamingPendingRef.current += 1
    const myOrder = streamingOrderRef.current++
    streamingBufsRef.current.set(myOrder, { chunks: [], done: false })

    const flushOrdered = () => {
      while (true) {
        const next = streamingNextEnqRef.current
        const entry = streamingBufsRef.current.get(next)
        if (!entry?.done) break
        if (streamingPlayerRef.current === player) {
          for (const { b64, idx, sr } of entry.chunks) {
            player.enqueueChunk(b64, idx, sr)
          }
        }
        streamingBufsRef.current.delete(next)
        streamingNextEnqRef.current++
      }
    }

    const stream = api.voiceSynthesize(text, {
      onChunk: (b64, index, sampleRate) => {
        const entry = streamingBufsRef.current.get(myOrder)
        if (entry) entry.chunks.push({ b64, idx: index, sr: sampleRate })
      },
      onEnd: () => {
        const entry = streamingBufsRef.current.get(myOrder)
        if (entry) entry.done = true
        flushOrdered()
        streamingPendingRef.current -= 1
        if (streamingFinishedRef.current && streamingPendingRef.current === 0) {
          if (streamingPlayerRef.current === player) player.finish()
        }
      },
      onError: (msg) => {
        const entry = streamingBufsRef.current.get(myOrder)
        if (entry) { entry.chunks = []; entry.done = true }
        flushOrdered()
        streamingPendingRef.current -= 1
        setError(msg)
      },
    })
    streamingAbortsRef.current.push(stream.abort)
  }

  // Append a delta from assistant_delta. Pops any completed sentences off
  // the buffer and fires them at the player. Called per-delta, so the
  // first sentence usually starts speaking ~300 ms after the model emits
  // the first period — not after the whole turn finishes.
  const streamingAppendDelta = (delta: string, messageId: string) => {
    if (!ttsEnabled) return
    const player = ensureStreamingPlayer(messageId)
    if (!player) return
    streamingBufferRef.current += delta
    while (true) {
      const { ready, rest } = popReadySentences(streamingBufferRef.current)
      if (!ready) break
      streamingBufferRef.current = rest
      speakSentence(ready, player)
    }
  }

  // Called from the SSE 'done' handler. Flushes any tail content as a
  // final sentence and signals the player that no more chunks will arrive.
  const streamingFinish = () => {
    const player = streamingPlayerRef.current
    if (!player) return
    const tail = streamingBufferRef.current.trim()
    streamingBufferRef.current = ''
    if (tail) speakSentence(tail, player)
    streamingFinishedRef.current = true
    if (streamingPendingRef.current === 0) {
      try { player.finish() } catch { /* ignore */ }
    }
  }

  const speakText = useCallback((text: string, messageId: string) => {
    // Strip markdown for a cleaner read-aloud: keep words, drop fences.
    const clean = text
      .replace(/```[\s\S]*?```/g, '')
      .replace(/`([^`]+)`/g, '$1')
      .replace(/!\[[^\]]*]\([^)]*\)/g, '')
      .replace(/\[([^\]]+)]\([^)]*\)/g, '$1')
      .replace(/[#*_>]+/g, '')
      .replace(/\s+/g, ' ')
      .trim()
    if (!clean) return

    // Stop any in-flight playback first.
    stopSpeaking()

    let player: VoicePlayer
    try {
      player = createVoicePlayer()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Audio playback unavailable.')
      return
    }
    voicePlayerRef.current = player
    setSpeakingMsgId(messageId)

    player.onFinished = () => {
      if (voicePlayerRef.current === player) {
        voicePlayerRef.current = null
        voiceStreamAbortRef.current = null
        setSpeakingMsgId(null)
      }
    }
    player.onError = (msg) => {
      setError(msg)
      if (voicePlayerRef.current === player) {
        voicePlayerRef.current = null
        voiceStreamAbortRef.current = null
        setSpeakingMsgId(null)
      }
    }

    const stream = api.voiceSynthesize(clean, {
      onChunk: (b64, index, sampleRate) => {
        if (voicePlayerRef.current === player) {
          player.enqueueChunk(b64, index, sampleRate)
        }
      },
      onEnd: () => {
        if (voicePlayerRef.current === player) {
          player.finish()
        }
      },
      onError: (msg) => {
        setError(msg)
        if (voicePlayerRef.current === player) {
          try { player.stop() } catch { /* ignore */ }
          voicePlayerRef.current = null
          voiceStreamAbortRef.current = null
          setSpeakingMsgId(null)
        }
      },
    })
    voiceStreamAbortRef.current = stream.abort
  }, [stopSpeaking])

  const toggleTTS = useCallback(() => {
    setTtsEnabled((prev) => {
      const next = !prev
      try { localStorage.setItem('atlas.ttsEnabled', next ? '1' : '0') } catch { /* ignore */ }
      if (!next) stopSpeaking()
      if (next) {
        // Unlock the shared AudioContext on the user gesture that turns TTS on
        // so later auto-play triggered from the SSE done event can play without
        // hitting the browser's autoplay policy.
        warmupAudioContext()
        // Pre-warm the Kokoro subprocess so the first sentence of the next
        // response doesn't pay the ~600 ms model-load cost. Best-effort:
        // failures here just mean the first synth call will pay the cost
        // itself, which is the previous behavior.
        api.voiceKokoroWarmup().catch(() => { /* ignore */ })
      }
      return next
    })
  }, [stopSpeaking])

  const toggleSpeechInput = useCallback(() => {
    if (speechListening) {
      stopSpeechInput()
      return
    }

    if (!speechAvailable) {
      toast.info('Voice input is not available in this browser.')
      return
    }

    speechBaseInputRef.current = input
    speechCommittedRef.current = ''

    try {
      speechSessionRef.current = startVoiceSpeech({
        lang: navigator.language || 'en-US',
        skipWavConversion: activeAudioProvider !== 'local',
        transcribe: (blob, language) => api.voiceTranscribe(blob, language),
        onStart: () => {
          setSpeechListening(true)
          toast.info('Recording — tap the mic again to stop and transcribe.')
        },
        onResult: ({ finalText }) => {
          if (finalText.trim()) {
            speechCommittedRef.current = joinTranscriptParts(speechCommittedRef.current, finalText)
          }
          syncSpeechInput()
        },
        onError: (message) => {
          setError(message)
        },
        onEnd: () => {
          speechSessionRef.current = null
          setSpeechListening(false)
          syncSpeechInput()
          textareaRef.current?.focus()
        },
      })
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Voice input failed.'
      setError(message)
      setSpeechListening(false)
    }
  }, [input, speechAvailable, speechListening, stopSpeechInput, syncSpeechInput])


  // ── Link preview fetching ──────────────────────────────────────────────────────

  /**
   * Scans a finalized message for URLs, fetches previews in parallel,
   * and attaches any successful results back onto the message record.
   * Runs in the background — never blocks or throws.
   */
  const fetchAndAttachPreviews = async (msgId: string, content: string) => {
    const urls = extractURLs(content)
    if (!urls.length) return
    const results = await Promise.all(
      urls.map(url => api.fetchLinkPreview(url).catch(() => null))
    )
    // Build a URL → preview map so cards can be anchored to their source URL.
    // Only include results that have at least a title (domain-only isn't useful).
    const previewMap: Record<string, LinkPreview> = {}
    results.forEach((p, i) => {
      if (p && p.title) previewMap[urls[i]] = p
    })
    if (!Object.keys(previewMap).length) return
    setMessages(prev => prev.map(m => m.id === msgId ? { ...m, linkPreviews: previewMap } : m))
  }

  // ── File handling ─────────────────────────────────────────────────────────────

  const resizeTextarea = () => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 140) + 'px'
  }

  const handleFileChange = (e: Event) => {
    const files = (e.target as HTMLInputElement).files
    if (!files || files.length === 0) return
    Array.from(files).forEach(file => {
      const reader = new FileReader()
      reader.onload = () => {
        const dataURL = reader.result as string
        const comma = dataURL.indexOf(',')
        const base64 = comma >= 0 ? dataURL.slice(comma + 1) : dataURL
        setAttachments(prev => [...prev, { filename: file.name, mimeType: file.type || 'application/octet-stream', data: base64 }])
      }
      reader.readAsDataURL(file)
    })
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const removeAttachment = (index: number) => {
    setAttachments(prev => prev.filter((_, i) => i !== index))
  }

  // Reads a File into a MessageAttachment (base64).
  const readFileAsAttachment = (file: File): Promise<MessageAttachment> =>
    new Promise((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => {
        const dataURL = reader.result as string
        const comma = dataURL.indexOf(',')
        resolve({ filename: file.name, mimeType: file.type || 'application/octet-stream', data: comma >= 0 ? dataURL.slice(comma + 1) : dataURL })
      }
      reader.onerror = reject
      reader.readAsDataURL(file)
    })

  // ── Drag-and-drop (Feature 5) ─────────────────────────────────────────────────

  const handleDragEnter = (e: DragEvent) => {
    e.preventDefault()
    dragCounterRef.current++
    if (dragCounterRef.current === 1) setDragOver(true)
  }
  const handleDragLeave = (e: DragEvent) => {
    e.preventDefault()
    dragCounterRef.current--
    if (dragCounterRef.current === 0) setDragOver(false)
  }
  const handleDragOver = (e: DragEvent) => { e.preventDefault() }
  const handleDrop = async (e: DragEvent) => {
    e.preventDefault()
    dragCounterRef.current = 0
    setDragOver(false)
    const files = Array.from(e.dataTransfer?.files ?? []).filter(f =>
      f.type.startsWith('image/') || f.type === 'application/pdf'
    )
    if (!files.length) return
    const loaded = await Promise.all(files.map(readFileAsAttachment))
    setAttachments(prev => [...prev, ...loaded])
  }

  // ── Paste (Feature 6) — images pasted from clipboard ──────────────────────────

  const handlePaste = useCallback(async (e: ClipboardEvent) => {
    const items = Array.from(e.clipboardData?.items ?? [])
    const imageItems = items.filter(item => item.type.startsWith('image/'))
    if (!imageItems.length) return
    e.preventDefault()
    const files = imageItems.map(item => item.getAsFile()).filter(Boolean) as File[]
    const loaded = await Promise.all(files.map(readFileAsAttachment))
    setAttachments(prev => [...prev, ...loaded])
  }, [])

  // ── Stop ───────────────────────────────────────────────────────────────────────

  const stopTurn = () => {
    api.cancelTurn(conversationID.current).catch(() => { /* ignore */ })
  }

  // ── Send ───────────────────────────────────────────────────────────────────────

  const send = async () => {
    const text = input.trim()
    if ((!text && attachments.length === 0) || sending) return
    // Warm the shared AudioContext on this user gesture so auto-play TTS can
    // fire later from the SSE done event without the browser's autoplay policy
    // keeping the context suspended. Also pre-warm Kokoro in the background
    // so the first sentence doesn't pay the model load cost.
    if (ttsEnabled) {
      warmupAudioContext()
      api.voiceKokoroWarmup().catch(() => { /* ignore */ })
    }

    const pendingAttachments = [...attachments]
    setInput('')
    setAttachments([])
    if (textareaRef.current) textareaRef.current.style.height = 'auto'
    setError(null)
    setPendingApproval(null)
    setSending(true)
    foregroundActiveRef.current = true

    const userContent = pendingAttachments.length > 0
      ? `${text}${text ? '\n' : ''}📎 ${pendingAttachments.map(a => a.filename).join(', ')}`
      : text
    const userMsg: Message      = { id: uuid(), role: 'user',      content: userContent, createdAt: Date.now() }
    const assistantMsg: Message = { id: uuid(), role: 'assistant', content: '', isTyping: true, createdAt: Date.now() }
    activeMsgId.current = assistantMsg.id   // track the active bubble for presence dots
    setMessages(prev => [...prev, userMsg, assistantMsg])

    esRef.current?.close()
    const es = api.streamMessage(conversationID.current)
    esRef.current = es

    let accumulatedContent = ''
    let resumedMsgID: string | null = null
    let resumedContent = ''
    let awaitingResume = false
    let hasReceivedText = false   // tracks first text delta this turn
    let turnCompleted = false
    let streamTurnID: string | null = null

    es.onmessage = (evt) => {
      try {
        const data = JSON.parse(evt.data) as ChatStreamEvent
        if (data.type === 'assistant_started') {
          streamTurnID = data.turnID ?? streamTurnID
          activeTurnIdRef.current = streamTurnID
        } else if (streamTurnID && data.turnID && data.turnID !== streamTurnID) {
          return
        }
        switch (data.type) {

          // ── Streaming text events ──────────────────────────────────────────────
          case 'assistant_started':
            // A new model turn is beginning. For the resume path we need a typing
            // bubble. If the original assistantMsg is empty (tool-only pre-approval
            // turn), reuse it — avoids a blank ghost bubble sitting above the dots.
            // If it already has text, create a fresh bubble for the new turn.
            if (awaitingResume && !resumedMsgID) {
              if (!accumulatedContent) {
                // Original bubble has no text — flip it back to typing and reuse it
                resumedMsgID = assistantMsg.id
                activeMsgId.current = assistantMsg.id
                setMessages(prev => prev.map(m => m.id === assistantMsg.id ? { ...m, isTyping: true } : m))
              } else {
                // Original bubble has text — open a new bubble for the resumed turn
                const newMsg: Message = { id: uuid(), role: 'assistant', content: '', isTyping: true }
                resumedMsgID = newMsg.id
                activeMsgId.current = newMsg.id
                setMessages(prev => [...prev, newMsg])
              }
            }
            break

          case 'assistant_delta': {
            const delta = data.content ?? ''

            if (!hasReceivedText) {
              hasReceivedText = true
            }

            if (awaitingResume) {
              resumedContent += delta
              if (!resumedMsgID) {
                const newMsg: Message = { id: uuid(), role: 'assistant', content: resumedContent, isTyping: true }
                resumedMsgID = newMsg.id
                setMessages(prev => [...prev, newMsg])
              } else {
                setMessages(prev => prev.map(m => m.id === resumedMsgID ? { ...m, content: resumedContent, isTyping: true } : m))
              }
              if (ttsEnabled && resumedMsgID) streamingAppendDelta(delta, resumedMsgID)
            } else {
              accumulatedContent += delta
              setMessages(prev => prev.map(m => m.id === assistantMsg.id ? { ...m, content: accumulatedContent, isTyping: true } : m))
              if (ttsEnabled) streamingAppendDelta(delta, assistantMsg.id)
            }
            break
          }

          case 'assistant_done':
            if (awaitingResume && resumedMsgID) {
              setMessages(prev => prev.map(m => m.id === resumedMsgID ? { ...m, isTyping: false } : m))
            } else {
              setMessages(prev => prev.map(m => m.id === assistantMsg.id ? { ...m, isTyping: false } : m))
            }
            break

          // ── Tool activity ──────────────────────────────────────────────────────
          case 'tool_started':
          case 'tool_call':
            break

          case 'tool_finished': {
            const blocks = parseToolBlocks(data.toolName, data.result)
            const targetId = awaitingResume ? resumedMsgID : assistantMsg.id
            if (targetId && blocks.length > 0) {
              appendBlocksToMessage(targetId, blocks)
            }
            break
          }

          case 'file_generated': {
            if (!data.fileToken || !data.filename) break
            const attachment: FileAttachment = {
              filename:  data.filename,
              mimeType:  data.mimeType ?? 'application/octet-stream',
              fileSize:  data.fileSize ?? 0,
              fileToken: data.fileToken,
            }
            const targetId = awaitingResume ? resumedMsgID : assistantMsg.id
            if (targetId) {
              appendFileToMessage(targetId, attachment)
            }
            break
          }

          case 'tool_failed':
            break

          // ── Approval ──────────────────────────────────────────────────────────
          case 'approval_required':
            setPendingApproval({
              toolCallID: data.toolCallID ?? '',
              toolName:   data.toolName   ?? '',
              args:       data.arguments  ?? '{}',
            })
            break

          // ── Legacy token (single-shot full-text delivery) ──────────────────────
          case 'token':
            if (!hasReceivedText) {
              hasReceivedText = true
            }
            if (awaitingResume) {
              resumedContent += data.content ?? ''
              if (!resumedMsgID) {
                const newMsg: Message = { id: uuid(), role: 'assistant', content: resumedContent, isTyping: true }
                resumedMsgID = newMsg.id
                setMessages(prev => [...prev, newMsg])
              } else {
                setMessages(prev => prev.map(m => m.id === resumedMsgID ? { ...m, content: resumedContent, isTyping: true } : m))
              }
            } else {
              accumulatedContent += data.content ?? ''
              setMessages(prev => prev.map(m => m.id === assistantMsg.id ? { ...m, content: accumulatedContent, isTyping: true } : m))
            }
            break

          // ── Conversation complete ──────────────────────────────────────────────
          case 'done':
            turnCompleted = true
            if (data.status === 'waitingForApproval') {
              setMessages(prev => prev.map(m => m.id === assistantMsg.id ? { ...m, content: accumulatedContent || m.content, isTyping: false } : m))
              awaitingResume = true
              hasReceivedText = false   // reset for the resumed turn
              activeMsgId.current = null
              activeTurnIdRef.current = null
              // Keep the foreground stream as the owner of the paused turn.
              // Resume emits a fresh assistant_started after the approval POST;
              // if we mark this inactive, the background SSE stream can render
              // the same resumed turn too, creating duplicate thinking bubbles.
              foregroundActiveRef.current = true
            } else if (data.status === 'denied') {
              activeMsgId.current = null
              activeTurnIdRef.current = null
              foregroundActiveRef.current = false
              const targetID = resumedMsgID ?? assistantMsg.id
              setMessages(prev => prev.map(m => m.id === targetID ? { ...m, content: resumedContent || 'The action was denied.', isTyping: false } : m))
              setPendingApproval(null); setSending(false); es.close()
            } else {
              activeMsgId.current = null
              activeTurnIdRef.current = null
              foregroundActiveRef.current = false
              // Last-resort frontend safety net: if the backend somehow produced no text
              // (backend fixes should have covered this), show a minimal fallback so the
              // bubble is never empty on a failed turn.
              const emptyFallback = (data.status === 'failed')
                ? "I ran into an issue with that. Let me know if you'd like to try again."
                : ''
              const finalID      = resumedMsgID ?? assistantMsg.id
              const finalContent = resumedMsgID
                ? (resumedContent || '')
                : (accumulatedContent || '')
              if (resumedMsgID) {
                setMessages(prev => prev.map(m => m.id === resumedMsgID ? { ...m, content: resumedContent || m.content || emptyFallback, isTyping: false } : m))
              } else {
                setMessages(prev => prev.map(m => m.id === assistantMsg.id ? { ...m, content: accumulatedContent || m.content || emptyFallback, isTyping: false } : m))
              }
              // Fetch link previews for assistant replies in the background
              if (data.status === 'completed' && finalContent) {
                fetchAndAttachPreviews(finalID, finalContent)
                if (ttsEnabled) {
                  // Sentence streaming has been firing all along — flush any
                  // remaining tail content and let the player drain. If for
                  // some reason no streaming player exists yet (e.g. the
                  // model emitted everything in one delta with no terminator),
                  // fall back to a single one-shot synth call.
                  if (streamingPlayerRef.current) {
                    streamingFinish()
                  } else {
                    speakText(finalContent, finalID)
                  }
                }
              }
              // Signal sidebar notification for any completed turn while away —
              // use ref to avoid stale closure from the async send() call.
              if (data.status === 'completed' && !isActiveRef.current) {
                onUnreadReplyRef.current?.()
              }
              setPendingApproval(null); setApprovingAction(false); setSending(false); es.close()
            }
            break

          case 'error':
            turnCompleted = true
            activeMsgId.current = null
            activeTurnIdRef.current = null
            foregroundActiveRef.current = false
            setError(extractStreamError(data))
            const targetID = resumedMsgID ?? assistantMsg.id
            setMessages(prev => prev.map(m => m.id === targetID ? { ...m, content: resumedContent || accumulatedContent || 'Failed to get response.', isTyping: false } : m))
            setSending(false); es.close()
            break

          case 'cancelled':
            turnCompleted = true
            activeMsgId.current = null
            activeTurnIdRef.current = null
            foregroundActiveRef.current = false
            // Remove the empty typing bubble; keep any partial content that arrived.
            setMessages(prev => {
              const cancelTargetID = resumedMsgID ?? assistantMsg.id
              const partial = resumedContent || accumulatedContent
              return prev.map(m =>
                m.id === cancelTargetID
                  ? { ...m, content: partial || '', isTyping: false }
                  : m
              ).filter(m => !(m.id === cancelTargetID && !partial))
            })
            setSending(false); es.close()
            break
        }
      } catch { /* ignore parse errors */ }
    }

    es.onerror = async () => {
      if (turnCompleted) return
      activeMsgId.current = null
      activeTurnIdRef.current = null
      foregroundActiveRef.current = false
      setSending(false)
      es.close()
      const reconciled = await reconcileConversationState(conversationID.current)
      if (reconciled && reconciled.length > 0) {
        toast.info('Connection interrupted. Synced the latest conversation state.', { durationMs: 3600 })
        return
      }
      setError('Connection lost while waiting for a reply. Please try again.')
    }

    try {
      await api.sendMessage(conversationID.current, text, pendingAttachments.length > 0 ? pendingAttachments : undefined)
    } catch (err) {
      activeMsgId.current = null
      activeTurnIdRef.current = null
      setError(err instanceof Error ? err.message : 'Failed to send message.')
      setMessages(prev => prev.map(m => m.id === assistantMsg.id ? { ...m, content: 'Failed to send message.', isTyping: false } : m))
      setSending(false); es.close()
    }
  }

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  }

  const copyMessage = async (id: string, msg: Message) => {
    if (copyFeedbackTimer.current) clearTimeout(copyFeedbackTimer.current)
    setRevealedCopyId(id)

    // Collect image attachments from both fileAttachments and blocks
    const imageFiles: FileAttachment[] = [
      ...(msg.fileAttachments ?? []).filter(f => f.mimeType.startsWith('image/')),
      ...(msg.blocks ?? [])
        .filter((b): b is Extract<MessageBlock, { type: 'file' }> => b.type === 'file')
        .map(b => b.file)
        .filter(f => f.mimeType.startsWith('image/')),
    ]

    try {
      if (imageFiles.length > 0 && typeof ClipboardItem !== 'undefined') {
        const token = imageFiles[0].fileToken
        // Build an HTML blob with the image embedded as a data URL + the caption text.
        // Using text/html (not image/png) means plain-text fields use text/plain while
        // rich-text apps (Notes, Mail, Notion) paste both image and text together.
        // Pass a Promise<Blob> so clipboard.write() fires within the user gesture.
        const htmlBlobPromise: Promise<Blob> = fetch(`/artifacts/${token}`)
          .then(r => r.blob())
          .then(raw => new Promise<Blob>((resolve, reject) => {
            const reader = new FileReader()
            reader.onload = () => {
              const dataURL = reader.result as string
              const caption = msg.content
                ? `<p>${msg.content.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;')}</p>`
                : ''
              resolve(new Blob([`<img src="${dataURL}" />${caption}`], { type: 'text/html' }))
            }
            reader.onerror = reject
            reader.readAsDataURL(raw)
          }))

        const types: Record<string, Blob | Promise<Blob>> = { 'text/html': htmlBlobPromise }
        if (msg.content) types['text/plain'] = new Blob([msg.content], { type: 'text/plain' })
        await navigator.clipboard.write([new ClipboardItem(types)])
      } else {
        await navigator.clipboard.writeText(msg.content)
      }
      setCopyFeedback({ id, status: 'copied' })
    } catch {
      try {
        await navigator.clipboard.writeText(msg.content)
        setCopyFeedback({ id, status: 'copied' })
      } catch {
        setCopyFeedback({ id, status: 'failed' })
      }
    }

    copyFeedbackTimer.current = setTimeout(() => {
      setCopyFeedback(prev => prev?.id === id ? null : prev)
      copyFeedbackTimer.current = null
    }, 1800)
  }

  const newConversation = () => {
    const id = uuid()
    localStorage.setItem(STORAGE_ID_KEY, id)
    localStorage.removeItem(STORAGE_MSG_KEY)
    conversationID.current = id
    setMessages([])
    setError(null)
    setPendingApproval(null)
    setAttachments([])
    speechSessionRef.current?.stop()
    speechSessionRef.current = null
    setSpeechListening(false)
    activeMsgId.current = null
    activeTurnIdRef.current = null
  }

  // Derived — model name shown as header subtitle.
  // Provider-specific IDs are normalized to readable labels.
  const activeModelRaw = modelByProvider[activeProvider]?.trim() || (activeProvider === 'openrouter' ? 'openrouter/auto:free' : 'Loading…')
  const activeModel = formatProviderModelName(activeProvider, activeModelRaw)

  const cloudHealthDot = CLOUD_CHAT_PROVIDERS.includes(activeProvider)
    ? (checkingCloudModelHealth
        ? (
          <span
            title="Checking model availability"
            style={{ display: 'inline-block', width: '7px', height: '7px', borderRadius: '50%', marginLeft: '8px', background: 'var(--text-3)', opacity: 0.75, verticalAlign: 'middle' }}
          />
        )
        : cloudModelHealth?.status === 'ok'
          ? (
            <span
              title="Model available"
              style={{ display: 'inline-block', width: '7px', height: '7px', borderRadius: '50%', marginLeft: '8px', background: 'var(--green, #22c55e)', verticalAlign: 'middle' }}
            />
          )
          : cloudModelHealth && cloudModelHealth.status !== 'unknown'
            ? (
              <span
                title={cloudModelHealth.message || 'Model unavailable'}
                style={{ display: 'inline-block', width: '7px', height: '7px', borderRadius: '50%', marginLeft: '8px', background: 'var(--red, #ef4444)', verticalAlign: 'middle' }}
              />
            )
            : null)
    : null

  // ── Render ─────────────────────────────────────────────────────────────────────

  return (
    <div
      class={`chat-screen${dragOver ? ' drag-over' : ''}`}
      onDragEnter={handleDragEnter as any}
      onDragLeave={handleDragLeave as any}
      onDragOver={handleDragOver as any}
      onDrop={handleDrop as any}
    >
      <PageHeader
        title="Chat"
        subtitle={activeModel ? <span>Model: {activeModel}{cloudHealthDot}</span> : ''}
        actions={
          <>
            {/* Search — icon collapses to expanding search bar + dropdown */}
            <div ref={historyContainerRef} class={`chat-history-search${historyOpen ? ' open' : ''}`}>
              <button
                class="chat-history-search-trigger"
                onClick={() => {
                  if (!historyOpen) {
                    setHistoryOpen(true)
                    setHistoryDropdownVisible(false)
                    setTimeout(() => historySearchRef.current?.focus(), 180)
                  }
                }}
                title="Search conversations"
                aria-label="Search conversations"
              >
                <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="6.5" cy="6.5" r="4.5" /><line x1="10" y1="10" x2="14" y2="14" />
                </svg>
              </button>
              <input
                ref={historySearchRef}
                class="chat-history-search-input"
                type="text"
                placeholder="Search conversations…"
                value={historyQuery}
                onClick={() => setHistoryDropdownVisible(true)}
                onInput={(e) => {
                  setHistoryDropdownVisible(true)
                  setHistoryQuery((e.target as HTMLInputElement).value)
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Escape') {
                    setHistoryOpen(false)
                    setHistoryDropdownVisible(false)
                    setHistoryQuery('')
                  }
                }}
                tabIndex={historyOpen ? 0 : -1}
              />
              <button
                class="chat-history-close-btn"
                onClick={() => {
                  setHistoryOpen(false)
                  setHistoryDropdownVisible(false)
                  setHistoryQuery('')
                }}
                title="Close"
                tabIndex={historyOpen ? 0 : -1}
                aria-hidden={historyOpen ? 'false' : 'true'}
              >
                <svg width="9" height="9" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
                  <line x1="1" y1="1" x2="9" y2="9" /><line x1="9" y1="1" x2="1" y2="9" />
                </svg>
              </button>

              {historyOpen && historyDropdownVisible && (
                <div class="chat-history-dropdown">
                    {historyLoading && (
                      <div class="chat-history-empty">Loading…</div>
                    )}
                    {!historyLoading && historySummaries.length === 0 && (
                      <div class="chat-history-empty">
                        {historyQuery ? `No results for "${historyQuery}"` : 'No conversations yet'}
                      </div>
                    )}
                    {!historyLoading && historySummaries.length > 0 && (
                      <div class="chat-history-list">
                        {historySummaries.map((s, i) => {
                          const diff = Date.now() - new Date(s.updatedAt).getTime()
                          const rel = diff < 60000 ? 'Just now' : diff < 3600000 ? `${Math.floor(diff / 60000)}m ago` : diff < 86400000 ? `${Math.floor(diff / 3600000)}h ago` : diff < 604800000 ? `${Math.floor(diff / 86400000)}d ago` : new Date(s.updatedAt).toLocaleDateString()
                          return (
                            <div
                              key={s.id}
                              class={`chat-history-item${i < historySummaries.length - 1 ? ' bordered' : ''}`}
                              onClick={() => resumeConversation(s.id)}
                            >
                              <div class="chat-history-item-meta">
                                <div class="chat-history-item-left">
                                  <span class="chat-history-item-time">{rel}</span>
                                  {s.platform && s.platform !== 'web' && (
                                    <span class="chat-history-platform-badge">{s.platform}</span>
                                  )}
                                </div>
                                <span class="chat-history-item-count">{s.messageCount} msgs</span>
                              </div>
                              <div class="chat-history-item-title">
                                {s.firstUserMessage || <em class="chat-history-item-empty">No messages</em>}
                              </div>
                            </div>
                          )
                        })}
                      </div>
                    )}
                    {/* Clear history footer */}
                    {!historyLoading && historySummaries.length > 0 && (
                      <div class="chat-history-footer">
                        <button
                          class="chat-history-clear-btn"
                          onClick={() => setPendingClearHistory(true)}
                        >
                          Clear all history
                        </button>
                      </div>
                    )}
                </div>
              )}
            </div>

            <button
              class="btn btn-sm btn-icon chat-header-action-btn"
              onClick={newConversation}
              title="New chat"
              aria-label="New chat"
            >
              <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round">
                <path d="M8 3v10M3 8h10" />
              </svg>
            </button>
          </>
        }
      />

      {dragOver && (
        <div class="chat-drop-overlay">
          <div class="chat-drop-overlay-content">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/>
            </svg>
            <div class="chat-drop-overlay-text">
              <strong>Drop to attach</strong>
              <span>Images and PDFs will be added to your next message</span>
            </div>
          </div>
        </div>
      )}

      {/* Messages */}
      <div
        ref={messagesRef}
        class="chat-messages"
        onClick={(e) => { handleCodeCopy(e as any); handleRunCode(e as any) }}
      >
        <div class="chat-thread">
          {messages.length === 0 && (
            thoughtCount > 0 ? (
              // Empty state, presence mood — Atlas has active thoughts on its
              // mind. Intentionally minimal: icon + one italic line, nothing
              // else. A different mood from the call-to-action empty state,
              // composed as one thought rather than a chip substitution.
              <div class="empty-state empty-state-presence">
                <svg class="empty-icon" viewBox="0 0 36 36" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M30 5.5A2.5 2.5 0 0027.5 3h-19A2.5 2.5 0 006 5.5v16A2.5 2.5 0 008.5 24H15l5 6 5-6h2.5A2.5 2.5 0 0030 21.5v-16z" />
                </svg>
                <p class="empty-state-presence-line" aria-live="polite">
                  {presencePhrase}
                  <span class="empty-state-presence-dots" aria-hidden="true">
                    <span>.</span><span>.</span><span>.</span>
                  </span>
                </p>
              </div>
            ) : (
              // Empty state, call-to-action mood — fresh chat, no thoughts
              // waiting. Icon + heading + subtitle + one suggestion chip.
              <div class="empty-state">
                <svg class="empty-icon" viewBox="0 0 36 36" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M30 5.5A2.5 2.5 0 0027.5 3h-19A2.5 2.5 0 006 5.5v16A2.5 2.5 0 008.5 24H15l5 6 5-6h2.5A2.5 2.5 0 0030 21.5v-16z" />
                </svg>
                <h3>Start a conversation</h3>
                <p>Type a message below to chat with {agentName}</p>
                <div class="empty-prompts">
                  <button
                    key={promptIndex}
                    class="empty-prompt-chip"
                    onClick={() => { setInput(PROMPTS[promptIndex % PROMPTS.length]); setTimeout(() => textareaRef.current?.focus(), 0) }}
                  >
                    {PROMPTS[promptIndex % PROMPTS.length]}
                  </button>
                </div>
              </div>
            )
          )}

          {messages.map((msg, i) => {
            const hasBlocks = (messageRenderableBlocks(msg).length > 0)
            // Skip ghost bubbles — empty assistant messages that are no longer typing.
            // These can appear on tool-only approval turns where no text was produced.
            if (!msg.content && !hasBlocks && !msg.isTyping && msg.id !== activeMsgId.current) return null
            const prevMsg = messages[i - 1]
            const msgDate = formatDateLabel(msg.createdAt ?? Date.now())
            const showDateSep = !prevMsg || formatDateLabel(prevMsg.createdAt ?? Date.now()) !== msgDate
            const isLatestVisibleMessage = !messages.slice(i + 1).some(nextMsg =>
              nextMsg.content || nextMsg.isTyping || nextMsg.id === activeMsgId.current
            )
            return (
            <>
              {showDateSep && (
                <div key={`sep-${msg.id}`} class="chat-date-separator">
                  <span>{msgDate}</span>
                </div>
              )}
            <div
              key={msg.id}
              data-msg-id={msg.id}
              class={`chat-message-group ${msg.role}${msg.isTyping ? ' typing' : ''}${revealedCopyId === msg.id ? ' meta-visible' : ''}${isLatestVisibleMessage ? ' is-latest' : ''}`}
            >
              <div class="chat-message-row">
                <div class={`chat-avatar chat-avatar-${msg.role}`}>
                  <span class="chat-avatar-content chat-avatar-content-glyph"><AvatarGlyph /></span>
                  <span class="chat-avatar-content chat-avatar-content-initial">{msg.role === 'assistant' ? agentName[0]?.toUpperCase() ?? 'A' : userName[0]?.toUpperCase() ?? 'Y'}</span>
                  <span class="chat-avatar-content chat-avatar-content-minimal">
                    <span class="chat-avatar-minimal-chevron" aria-hidden="true">{msg.role === 'assistant' ? '>' : '<'}</span>
                  </span>
                </div>
                <div class="chat-bubble-wrap">
                  <div
                    class="chat-bubble"
                    onClick={(e) => {
                      const target = e.target as HTMLElement
                      if (target.closest('a, button, input, textarea, select')) return
                      setRevealedCopyId(current => current === msg.id ? null : msg.id)
                    }}
                  >
                    {renderBlockList(messageRenderableBlocks(msg))}
                    {msg.content
                      ? (msg.role === 'assistant'
                          ? renderMessageContent(msg.content, msg.linkPreviews)
                          : msg.content)
                      : (msg.isTyping || msg.id === activeMsgId.current)
                          ? <TypingDots />
                          : null
                    }
                  </div>
                  {!msg.isTyping && (msg.content || hasBlocks || msg.createdAt) && (
                    <div class="chat-message-meta">
                      {msg.content && (() => {
                        const copyState = copyFeedback?.id === msg.id ? copyFeedback.status : 'idle'
                        const label = copyState === 'copied'
                          ? 'Copied'
                          : copyState === 'failed'
                            ? 'Retry copy'
                            : 'Copy'
                        return (
                          <button
                            class={`chat-meta-copy-btn${copyState !== 'idle' ? ` ${copyState}` : ''}`}
                            onClick={(e) => {
                              e.stopPropagation()
                              copyMessage(msg.id, msg)
                            }}
                            title="Copy message"
                            aria-label={label}
                          >
                            {copyState === 'copied' ? <CheckIcon /> : <CopyIcon />}
                          </button>
                        )
                      })()}
                      {msg.createdAt && (
                        <span class="chat-timestamp">{formatTime(msg.createdAt)}</span>
                      )}
                    </div>
                  )}
                </div>
              </div>
            </div>
            </>
            )
          })}



          {pendingApproval && (
            <InlineApprovalCard
              toolName={pendingApproval.toolName}
              args={pendingApproval.args}
              loading={approvingAction}
              onApprove={async () => {
                setApprovingAction(true)
                try {
                  await api.approve(pendingApproval.toolCallID)
                  setPendingApproval(null)
                  setApprovingAction(false)
                } catch {
                  setApprovingAction(false)
                }
              }}
              onDeny={async () => {
                setApprovingAction(true)
                try {
                  await api.deny(pendingApproval.toolCallID)
                  setPendingApproval(null)
                  setApprovingAction(false)
                } catch {
                  setApprovingAction(false)
                }
              }}
            />
          )}

          {/* Proactive composing indicator (Feature 11) */}
          {proactiveComposing && !sending && (
            <div class="proactive-composing-pill">
              <span class="proactive-composing-dot" /><span class="proactive-composing-dot" /><span class="proactive-composing-dot" />
              <span class="proactive-composing-label">Atlas has something to share</span>
            </div>
          )}

          <ErrorBanner error={error} onDismiss={() => setError(null)} small />
          <div ref={bottomRef} />
        </div>
      </div>

      <button
        class={`chat-scroll-bottom-btn${showScrollBottom ? ' visible' : ''}${hasActiveAssistantOutput ? ' is-active' : ''}`}
        onClick={() => scrollToBottom(true)}
        title={hasActiveAssistantOutput ? 'Assistant is generating — scroll to latest' : 'Scroll to bottom'}
        aria-label={hasActiveAssistantOutput ? 'Assistant is generating — scroll to latest' : 'Scroll to bottom'}
      >
        {hasActiveAssistantOutput ? (
          <span class="chat-scroll-bottom-thinking" aria-hidden="true">
            <span />
            <span />
            <span />
          </span>
        ) : (
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M4 6l4 4 4-4" />
          </svg>
        )}
      </button>

      {/* Composer */}
      <div class="chat-composer">
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*,.pdf"
          multiple
          style={{ display: 'none' }}
          onChange={handleFileChange}
        />

        <div class="chat-composer-inner">
          {/* Attachment chips */}
          {attachments.length > 0 && (
            <div class="chat-attachment-chips">
              {attachments.map((att, i) => (
                <div key={i} class="chat-attachment-chip">
                  <span class="chat-attachment-glyph" aria-hidden="true">
                    <AttachmentChipIcon mimeType={att.mimeType} />
                  </span>
                  <span class="chat-attachment-name">{att.filename}</span>
                  <button
                    class="chat-attachment-remove"
                    onClick={() => removeAttachment(i)}
                    title="Remove"
                    aria-label={`Remove attachment ${att.filename}`}
                  >
                    ×
                  </button>
                </div>
              ))}
            </div>
          )}

          {/* Textarea with mic + send inside */}
          <div class="chat-textarea-wrap">
            <textarea
              ref={textareaRef}
              class="chat-input"
              placeholder={`Message ${agentName}…`}
              aria-label={`Message ${agentName}`}
              value={input}
              onInput={(e) => { setInput((e.target as HTMLTextAreaElement).value); resizeTextarea() }}
              onKeyDown={handleKeyDown}
              onPaste={handlePaste as any}
              disabled={sending || speechListening}
              rows={1}
            />
            <button
              class={`chat-mic-btn${speechListening ? ' active' : ''}${!speechAvailable ? ' unsupported' : ''}`}
              onClick={toggleSpeechInput}
              disabled={sending}
              type="button"
              title={speechListening ? 'Stop voice input' : speechAvailable ? 'Voice input (Whisper)' : 'Voice input unavailable in this browser'}
              aria-label={speechListening ? 'Stop voice input' : 'Start voice input'}
              aria-pressed={speechListening ? 'true' : 'false'}
            >
              <MicIcon />
            </button>
            {sending ? (
              <button
                class="chat-send-btn chat-stop-btn"
                onClick={stopTurn}
                title="Stop generation"
                aria-label="Stop generation"
              >
                <StopIcon />
              </button>
            ) : (
              <button
                class="chat-send-btn"
                onClick={send}
                disabled={speechListening || (!input.trim() && attachments.length === 0)}
                title="Send message"
                aria-label="Send message"
              >
                <SendIcon />
              </button>
            )}
          </div>

          {/* Bottom toolbar: tools left — provider right */}
          <div class="chat-composer-toolbar">
            <div class="chat-toolbar-left">
              <button
                class={`chat-tool-btn${attachments.length > 0 ? ' active' : ''}`}
                onClick={() => fileInputRef.current?.click()}
                disabled={sending && !speakingMsgId}
                title="Attach image or PDF"
                aria-label="Attach file"
              >
                <AttachIcon />
              </button>
              <button
                class={`chat-tool-btn${ttsEnabled ? ' active' : ''}`}
                onClick={speakingMsgId ? stopSpeaking : toggleTTS}
                disabled={sending}
                type="button"
                title={speakingMsgId ? 'Stop reading' : ttsEnabled ? 'Disable auto-read' : 'Enable auto-read'}
                aria-label={speakingMsgId ? 'Stop reading' : ttsEnabled ? 'Disable auto-read' : 'Enable auto-read'}
                aria-pressed={ttsEnabled ? 'true' : 'false'}
              >
                {speakingMsgId ? <SpeakerStopIcon /> : ttsEnabled ? <SpeakerIcon /> : <SpeakerMutedIcon />}
              </button>
              {mlxHasThinking && (
                <button
                  class={`chat-tool-btn${thinkingEnabled ? ' active' : ''}`}
                  onClick={() => {
                    const next = !thinkingEnabled
                    setThinkingEnabled(next)
                    void api.updateConfig({ atlasMLXThinkingEnabled: next })
                  }}
                  disabled={sending}
                  type="button"
                  title={thinkingEnabled ? 'Thinking on — click to disable' : 'Enable thinking'}
                  aria-label={thinkingEnabled ? 'Disable thinking' : 'Enable thinking'}
                  aria-pressed={thinkingEnabled ? 'true' : 'false'}
                >
                  <ThinkingIcon />
                </button>
              )}
            </div>

            <div class="chat-toolbar-right">
              {/* Provider — visible label with transparent select overlay */}
              <div class="chat-provider-wrap">
                <select
                  class="chat-provider-select"
                  value={LOCAL_LM_PROVIDERS.has(activeProvider) ? 'local_lm' : activeProvider}
                  onChange={(e) => handleProviderChange((e.target as HTMLSelectElement).value)}
                  aria-label="Model provider"
                >
                  <optgroup label="Cloud">
                    <option value="openai">OpenAI</option>
                    <option value="anthropic">Anthropic</option>
                    <option value="gemini">Gemini</option>
                    <option value="openrouter">OpenRouter</option>
                  </optgroup>
                  <optgroup label="Local">
                    <option value="local_lm">Local LM</option>
                    <option value="lm_studio">LM Studio</option>
                    <option value="ollama">Ollama</option>
                  </optgroup>
                </select>
                <span class="chat-provider-label" aria-hidden="true">
                  {PROVIDER_LABELS[activeProvider]} ▾
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
      {pendingClearHistory && (
        <ConfirmDialog
          title="Clear all history?"
          body="Every conversation will be permanently deleted."
          confirmLabel="Clear All"
          danger
          onConfirm={async () => {
            setPendingClearHistory(false)
            await api.clearAllConversations()
            setHistorySummaries([])
            setHistoryOpen(false)
            newConversation()
          }}
          onCancel={() => setPendingClearHistory(false)}
        />
      )}
    </div>
  )
}

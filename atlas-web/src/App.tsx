import { useState, useEffect, useRef } from 'preact/hooks'
import { type ThemeMode, type ThemePreset, type ThemeConfig, type DensityMode, type ChatFontSize, type ChatRadius, type ChatFont, type ChatAvatarStyle, loadTheme, saveTheme, applyTheme, watchSystemTheme, THEME_PRESETS } from './theme'
import { Chat } from './screens/Chat'
import { Communications } from './screens/Communications'
import { Approvals } from './screens/Approvals'
import { Skills } from './screens/Skills'
import { Forge } from './screens/Forge'
import { Mind } from './screens/Mind'
import { Activity } from './screens/Activity'
import { Settings } from './screens/Settings'
import { AIProviders } from './screens/AIProviders'
import { Automations } from './screens/Automations'
import { Workflows } from './screens/Workflows'
import { Dashboards } from './screens/Dashboards'
import { APIKeys } from './screens/APIKeys'
import { Theme } from './screens/Theme'
import { Docs } from './screens/Docs'
import { LocalLM } from './screens/LocalLM'
import { Usage } from './screens/Usage'
import { Onboarding } from './screens/Onboarding'
import { Toaster } from './components/Toaster'
import { HeaderChromeContext } from './components/PageHeader'
import { api, RuntimeStatus } from './api/client'

type Screen =
  | 'chat'
  | 'onboarding'
  | 'communications'
  | 'approvals'
  | 'skills'
  | 'forge'
  | 'mind'
  | 'automations'
  | 'workflows'
  | 'dashboards'
  | 'activity'
  | 'settings'
  | 'ai-providers'
  | 'api-keys'
  | 'theme'
  | 'local-lm'
  | 'usage'
  | 'docs'

const VALID_SCREENS: Screen[] = [
  'chat', 'onboarding', 'communications', 'approvals', 'skills', 'forge', 'mind',
  'automations', 'workflows', 'dashboards', 'activity', 'settings', 'ai-providers', 'api-keys', 'theme',
  'local-lm', 'usage',
  'docs',
]

function getInitialScreen(): Screen {
  const hash = window.location.hash.replace('#', '') as Screen
  return VALID_SCREENS.includes(hash) ? hash : 'chat'
}

/* ── SVG Icons ─────────────────────────────────────────── */

/* ── Notification chime ────────────────────────────────────────────────── */
function playNotifyChime() {
  try {
    const AudioCtx = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
    const ctx = new AudioCtx()

    const play = () => {
      const master = ctx.createGain()
      master.gain.value = 0.18
      master.connect(ctx.destination)
      // Two ascending sine tones: A5 → C6, bright and clean, 140ms apart
      const notes = [{ freq: 880, t: 0 }, { freq: 1047, t: 0.14 }]
      notes.forEach(({ freq, t }) => {
        const osc  = ctx.createOscillator()
        const gain = ctx.createGain()
        osc.type = 'sine'
        osc.frequency.value = freq
        osc.connect(gain)
        gain.connect(master)
        const at = ctx.currentTime + t
        gain.gain.setValueAtTime(0, at)
        gain.gain.linearRampToValueAtTime(1, at + 0.012)
        gain.gain.exponentialRampToValueAtTime(0.0001, at + 0.32)
        osc.start(at)
        osc.stop(at + 0.35)
      })
      setTimeout(() => ctx.close().catch(() => {}), 900)
    }

    // Resume the context if the browser suspended it (no recent user gesture)
    if (ctx.state === 'suspended') {
      ctx.resume().then(play).catch(() => {})
    } else {
      play()
    }
  } catch { /* audio blocked or unavailable */ }
}

const Icon = {
  chat: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M14 2.5A1.5 1.5 0 0012.5 1h-9A1.5 1.5 0 002 2.5v7A1.5 1.5 0 003.5 11H7l3 3v-3h2.5A1.5 1.5 0 0014 9.5v-7z" />
    </svg>
  ),
  // Speech bubble with three filled dots — "message waiting"
  chatActive: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M14 2.5A1.5 1.5 0 0012.5 1h-9A1.5 1.5 0 002 2.5v7A1.5 1.5 0 003.5 11H7l3 3v-3h2.5A1.5 1.5 0 0014 9.5v-7z" />
      <circle cx="5.5" cy="5.75" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="8"   cy="5.75" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="10.5" cy="5.75" r="0.9" fill="currentColor" stroke="none" />
    </svg>
  ),
  onboarding: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M8 1.8l1.2 3.2 3.2 1.2-3.2 1.2L8 10.6 6.8 7.4 3.6 6.2l3.2-1.2L8 1.8z" />
      <path d="M12.4 10.6l.6 1.6 1.6.6-1.6.6-.6 1.6-.6-1.6-1.6-.6 1.6-.6.6-1.6z" />
    </svg>
  ),
  approvals: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="8" cy="8" r="6.5" />
      <path d="M5.5 8l2 2 3-3.5" />
    </svg>
  ),
  communications: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M14 4.5A1.5 1.5 0 0 0 12.5 3h-9A1.5 1.5 0 0 0 2 4.5v5A1.5 1.5 0 0 0 3.5 11H6l2 2 2-2h2.5A1.5 1.5 0 0 0 14 9.5z" />
      <path d="M5 6.5h6M5 8.5h4" />
    </svg>
  ),
  automations: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="8" cy="8" r="3" />
      <path d="M8 1v2M8 13v2M1 8h2M13 8h2M3.1 3.1l1.4 1.4M11.5 11.5l1.4 1.4M12.9 3.1l-1.4 1.4M4.5 11.5l-1.4 1.4" />
    </svg>
  ),
  workflows: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <rect x="2" y="2.5" width="12" height="11" rx="2" />
      <path d="M5 5.5h6M5 8h6M5 10.5h3" />
    </svg>
  ),
  forge: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M3 13h10M5 13V8.5L8 4l3 4.5V13" />
      <path d="M6.5 13v-3h3v3" />
    </svg>
  ),
  skills: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M8.5 1L10 5.5h4.5L11 8.5l1.5 4.5L8.5 10l-4 3 1.5-4.5L2.5 5.5H7z" />
    </svg>
  ),
  mind: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M8 2C5.2 2 3 4.2 3 7c0 1.5.6 2.9 1.6 3.9L4 13h8l-.6-2.1C12.4 9.9 13 8.5 13 7c0-2.8-2.2-5-5-5z" />
      <path d="M6 9.5c0 1.1.9 2 2 2s2-.9 2-2" />
    </svg>
  ),
  activity: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <polyline points="1,9 4,5 7,8 10,3 15,7" />
    </svg>
  ),
  apiKeys: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="5" cy="8" r="3" />
      <path d="M7.5 8H14M11 8v2M13 8v1.5" />
    </svg>
  ),
  settings: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="8" cy="8" r="2.5" />
      <path d="M8 1v1.5M8 13.5V15M1 8h1.5M13.5 8H15M3.1 3.1l1 1M11.9 11.9l1 1M12.9 3.1l-1 1M4.1 11.9l-1 1" />
    </svg>
  ),
  aiProviders: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <line x1="2" y1="5" x2="14" y2="5" />
      <line x1="2" y1="11" x2="14" y2="11" />
      <circle cx="6" cy="5" r="2" />
      <circle cx="10" cy="11" r="2" />
    </svg>
  ),
  theme: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="8" cy="8" r="6.5" />
      <circle cx="5" cy="6.5" r="0.8" fill="currentColor" stroke="none" />
      <circle cx="10.5" cy="5.5" r="0.8" fill="currentColor" stroke="none" />
      <circle cx="11.5" cy="10" r="0.8" fill="currentColor" stroke="none" />
      <circle cx="5.5" cy="11" r="0.8" fill="currentColor" stroke="none" />
    </svg>
  ),
  controlCenter: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
      <rect x="3.5" y="4" width="17" height="16" rx="4" />
      <path d="M8 8.5h8" />
      <path d="M8 12h8" />
      <path d="M8 15.5h5" />
      <circle cx="15.5" cy="8.5" r="1.25" fill="currentColor" stroke="none" />
      <circle cx="10.5" cy="12" r="1.25" fill="currentColor" stroke="none" />
      <circle cx="15" cy="15.5" r="1.25" fill="currentColor" stroke="none" />
    </svg>
  ),
  atlasEngine: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <rect x="2" y="5" width="12" height="7" rx="1.5" />
      <path d="M5 5V3.5a3 3 0 0 1 6 0V5" />
      <circle cx="8" cy="8.5" r="1.5" />
    </svg>
  ),
  atlasMLX: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <polygon points="8,1.5 14.5,5 14.5,11 8,14.5 1.5,11 1.5,5" />
      <circle cx="8" cy="8" r="2" />
      <path d="M8 3.5V6M8 10v2.5M12.2 5.5l-2 1.2M5.8 9.3l-2 1.2M12.2 10.5l-2-1.2M5.8 6.7l-2-1.2" />
    </svg>
  ),
  usage: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <rect x="1.5" y="9" width="3" height="5.5" rx="0.75" />
      <rect x="6.5" y="5.5" width="3" height="9" rx="0.75" />
      <rect x="11.5" y="1.5" width="3" height="13" rx="0.75" />
    </svg>
  ),
  docs: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.45" stroke-linecap="round" stroke-linejoin="round">
      <path d="M3 2.5h7.5a2 2 0 0 1 2 2V13H5a2 2 0 0 0-2 2z" />
      <path d="M5 2.5v10.5" />
      <path d="M7 5.5h3.5M7 8h3.5" />
    </svg>
  ),
  logo: (
    <svg width="26" height="26" viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
      <defs>
        <filter id="logo-s" x="-30%" y="-30%" width="160%" height="160%">
          <feDropShadow dx="0" dy="1.5" stdDeviation="1.8" flood-color="currentColor" flood-opacity="0.3"/>
        </filter>
      </defs>
      <rect width="32" height="32" rx="7" fill="currentColor" fill-opacity="0.08"/>
      <g stroke="currentColor" stroke-width="3.0" stroke-linecap="round" stroke-linejoin="round" fill="none" filter="url(#logo-s)">
        <line x1="18.5" y1="5.5" x2="5.5" y2="26.5"/>
        <line x1="18.5" y1="5.5" x2="22.5" y2="26.5"/>
        <line x1="11.5" y1="17.5" x2="20.5" y2="17.5"/>
      </g>
    </svg>
  ),
  collapse: (
    <svg width="13" height="13" viewBox="0 0 13 13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M8 2.5L4.5 6.5L8 10.5" />
    </svg>
  ),
  expand: (
    <svg width="13" height="13" viewBox="0 0 13 13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M5 2.5L8.5 6.5L5 10.5" />
    </svg>
  ),
  hamburger: (
    <svg width="15" height="15" viewBox="0 0 15 15" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round">
      <path d="M2 4h11M2 7.5h11M2 11h11" />
    </svg>
  ),
  dashboards: (
    <svg width="17" height="17" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <rect x="2" y="2" width="5" height="5" rx="0.7" />
      <rect x="9" y="2" width="5" height="3" rx="0.7" />
      <rect x="9" y="7" width="5" height="7" rx="0.7" />
      <rect x="2" y="9" width="5" height="5" rx="0.7" />
    </svg>
  ),
}

const SCREEN_LABELS: Partial<Record<Screen, string>> = {
  chat: 'Chat',
  onboarding: 'Onboarding',
  communications: 'Communications',
  approvals: 'Approvals',
  skills: 'Skills',
  forge: 'Forge',
  mind: 'Mind',
  automations: 'Automations',
  workflows: 'Workflows',
  dashboards: 'Dashboards',
  activity: 'Activity',
  settings: 'General',
  'ai-providers': 'AI Providers',
  'api-keys': 'Credentials',
  theme: 'Appearance',
  'local-lm': 'Local LM',
  usage: 'Usage',
  docs: 'Docs',
}

/* ── Nav groups ────────────────────────────────────────── */

type NavItem = { id: Screen; icon: preact.ComponentChild; label: string }
type NavGroupID = 'operator' | 'capabilities' | 'settings'
type NavGroup = { id: NavGroupID; label: string; items: NavItem[]; defaultExpanded: boolean }

const NAV_GROUPS: NavGroup[] = [
  {
    id: 'operator',
    label: 'Operator',
    defaultExpanded: true,
    items: [
      { id: 'automations', icon: Icon.automations, label: 'Automations' },
      { id: 'workflows',   icon: Icon.workflows,   label: 'Workflows' },
      { id: 'dashboards',  icon: Icon.dashboards,  label: 'Dashboards' },
      { id: 'approvals',   icon: Icon.approvals,   label: 'Approvals' },
      { id: 'usage',       icon: Icon.usage,       label: 'Usage' },
    ],
  },
  {
    id: 'capabilities',
    label: 'Capabilities',
    defaultExpanded: false,
    items: [
      { id: 'skills',        icon: Icon.skills,        label: 'Skills' },
      { id: 'forge',         icon: Icon.forge,         label: 'Forge' },
      { id: 'mind',          icon: Icon.mind,          label: 'Mind' },
      { id: 'local-lm',      icon: Icon.atlasEngine,   label: 'Local LM' },
    ],
  },
  {
    id: 'settings',
    label: 'Settings',
    defaultExpanded: false,
    items: [
      { id: 'settings',        icon: Icon.settings,        label: 'General' },
      { id: 'ai-providers',    icon: Icon.aiProviders,     label: 'AI Providers' },
      { id: 'api-keys',        icon: Icon.apiKeys,         label: 'Credentials' },
      { id: 'theme',           icon: Icon.theme,           label: 'Appearance' },
      { id: 'communications',  icon: Icon.communications,  label: 'Communications' },
      { id: 'activity',        icon: Icon.activity,        label: 'Activity' },
    ],
  },
]

/* ── App ───────────────────────────────────────────────── */

export function App() {
  const [screen, setScreen]               = useState<Screen>(getInitialScreen)
  const [pendingApprovals, setPendingApprovals] = useState(0)
  const [pendingProposals, setPendingProposals] = useState(0)
  const [pendingGreetings, setPendingGreetings] = useState(0)
  const [unreadChatReplies, setUnreadChatReplies] = useState(0)
  const [runtimeStatus, setRuntimeStatus] = useState<RuntimeStatus | null>(null)
  const [onboardingComplete, setOnboardingComplete] = useState<boolean | null>(null)
  const [collapsed, setCollapsed]         = useState<boolean>(() =>
    localStorage.getItem('sidebarCollapsed') === 'true'
  )
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [isMobile, setIsMobile]           = useState(() => window.innerWidth <= 480)
  const [expandedGroups, setExpandedGroups] = useState<Record<NavGroupID, boolean>>(() => {
    // Start from defaults
    const defaults = NAV_GROUPS.reduce((acc, g) => ({ ...acc, [g.id]: g.defaultExpanded }), {} as Record<NavGroupID, boolean>)
    // Restore persisted expand/collapse state if available
    try {
      const stored = localStorage.getItem('sidebarGroups')
      if (stored) Object.assign(defaults, JSON.parse(stored))
    } catch { /* ignore */ }
    // Always expand the group containing the currently active screen
    const activeGroup = NAV_GROUPS.find(g => g.items.some(item => item.id === getInitialScreen()))
    if (activeGroup) defaults[activeGroup.id] = true
    return defaults
  })
  const [themeConfig, setThemeConfig] = useState<ThemeConfig>(loadTheme)

  const setActiveTheme = (mode: ThemeMode) =>
    setThemeConfig(prev => ({ ...prev, mode }))

  const setActivePreset = (preset: ThemePreset) => {
    const presetOption = THEME_PRESETS.find(p => p.id === preset)
    const resolvedMode = themeConfig.mode === 'system'
      ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
      : themeConfig.mode
    const presetAccent = presetOption?.preview[resolvedMode].accent
    setThemeConfig(prev => ({
      ...prev,
      preset,
      ...(presetAccent ? { accent: presetAccent } : {}),
    }))
  }

  const setActiveAccent = (accent: string) =>
    setThemeConfig(prev => ({ ...prev, accent }))

  const setActiveDensity = (density: DensityMode) =>
    setThemeConfig(prev => ({ ...prev, density }))

  const setChatFontSize = (chatFontSize: ChatFontSize) =>
    setThemeConfig(prev => ({ ...prev, chatFontSize }))

  const setChatRadius = (chatRadius: ChatRadius) =>
    setThemeConfig(prev => ({ ...prev, chatRadius }))

  const setChatFont = (chatFont: ChatFont) =>
    setThemeConfig(prev => ({ ...prev, chatFont }))

  const setChatAvatarStyle = (chatAvatarStyle: ChatAvatarStyle) =>
    setThemeConfig(prev => ({ ...prev, chatAvatarStyle }))

  const activeTheme = themeConfig.mode

  // Chime when a chat notification first arrives (0 → >0 transition).
  // A 1-second grace period prevents chiming on initial page load.
  const chimeReadyRef   = useRef(false)
  const prevNotifyRef   = useRef(false)
  useEffect(() => {
    const t = setTimeout(() => { chimeReadyRef.current = true }, 1000)
    return () => clearTimeout(t)
  }, [])
  useEffect(() => {
    const hasNotify = (pendingGreetings > 0 || unreadChatReplies > 0) && screen !== 'chat'
    if (hasNotify && !prevNotifyRef.current && chimeReadyRef.current) {
      playNotifyChime()
    }
    prevNotifyRef.current = hasNotify
  }, [pendingGreetings, unreadChatReplies, screen])

  // Poll approval count + status for sidebar badge
  useEffect(() => {
    api.onboardingStatus()
      .then((status) => setOnboardingComplete(status.completed))
      .catch(() => setOnboardingComplete(true))
  }, [])

  useEffect(() => {
    const poll = async () => {
      try {
        const [approvals, status, proposals, greetings] = await Promise.allSettled([
          api.approvals(), api.status(), api.forgeProposals(), api.pendingGreetings(),
        ])
        if (approvals.status === 'fulfilled') {
          setPendingApprovals(approvals.value.filter(a => a.status === 'pending').length)
        }
        if (status.status === 'fulfilled') {
          setRuntimeStatus(status.value)
        }
        if (proposals.status === 'fulfilled') {
          setPendingProposals(proposals.value.filter(p => p.status === 'pending').length)
        }
        if (greetings.status === 'fulfilled') {
          setPendingGreetings(greetings.value.count)
        }
      } catch {
        // daemon may not be running
      }
    }
    poll()
    const interval = setInterval(poll, 5000)
    return () => clearInterval(interval)
  }, [])

  // Apply + persist theme; re-run whenever config changes or OS flips
  useEffect(() => {
    saveTheme(themeConfig)
    applyTheme(themeConfig)
    return watchSystemTheme(themeConfig, () => applyTheme(themeConfig))
  }, [themeConfig])

  const navigate = (s: Screen) => {
    if (s === 'chat') {
      setUnreadChatReplies(0)
      setPendingGreetings(0) // optimistic — refills on next poll if greeting not yet delivered
    }
    setScreen(s)
    window.location.hash = s
    // Auto-expand the group containing this screen
    const activeGroup = NAV_GROUPS.find(g => g.items.some(item => item.id === s))
    if (activeGroup) {
      setExpandedGroups(prev => {
        if (prev[activeGroup.id]) return prev
        const next = { ...prev, [activeGroup.id]: true }
        try { localStorage.setItem('sidebarGroups', JSON.stringify(next)) } catch { /* ignore */ }
        return next
      })
    }
    if (mobileNavOpen) closeMobileNav()
  }

  useEffect(() => {
    const handler = () => setScreen(getInitialScreen())
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [])

  const toggleCollapsed = () => {
    const next = !collapsed
    autoCollapsedRef.current = false          // user took manual control
    setCollapsed(next)
    localStorage.setItem('sidebarCollapsed', String(next))
  }

  const toggleGroup = (groupID: NavGroupID) => {
    setExpandedGroups((current) => {
      const next = { ...current, [groupID]: !current[groupID] }
      try { localStorage.setItem('sidebarGroups', JSON.stringify(next)) } catch { /* ignore */ }
      return next
    })
  }

  const openMobileNav = () => {
    setCollapsed(false)
    setMobileNavOpen(true)
  }

  const closeMobileNav = () => {
    setCollapsed(true)
    setMobileNavOpen(false)
  }

  // ── Auto-collapse sidebar on narrow viewports ──────────────
  const SIDEBAR_BREAKPOINT = 700             // px — below this, sidebar collapses
  const MOBILE_BREAKPOINT  = 480             // px — below this, sidebar goes to overlay mode
  const autoCollapsedRef  = useRef(false)    // true when WE collapsed it (not the user)
  const collapsedRef      = useRef(collapsed)
  collapsedRef.current = collapsed

  useEffect(() => {
    const check = () => {
      const mobile = window.innerWidth <= MOBILE_BREAKPOINT
      setIsMobile(mobile)
      if (window.innerWidth < SIDEBAR_BREAKPOINT && !collapsedRef.current) {
        autoCollapsedRef.current = true
        setCollapsed(true)
      } else if (window.innerWidth >= SIDEBAR_BREAKPOINT && autoCollapsedRef.current) {
        autoCollapsedRef.current = false
        setCollapsed(false)
      }
    }
    check()                                  // evaluate immediately on mount
    window.addEventListener('resize', check)
    return () => window.removeEventListener('resize', check)
  }, [])

  const dotClass = runtimeStatus
    ? `status-dot ${runtimeStatus.state}`
    : 'status-dot unknown'

  const statusLabel = runtimeStatus
    ? runtimeStatus.state.charAt(0).toUpperCase() + runtimeStatus.state.slice(1)
    : 'Connecting…'

  if (onboardingComplete === null) {
    return (
      <div class="onboarding-shell">
        <div class="onboarding-card">
          <div class="onboarding-loading">
            <span class="spinner" />
            <span>Loading Atlas…</span>
          </div>
        </div>
      </div>
    )
  }

  if (!onboardingComplete) {
    return (
      <Onboarding
        onCompleted={() => {
          setOnboardingComplete(true)
          navigate('chat')
        }}
      />
    )
  }

  return (
    <div class="app">
      {/* Mobile backdrop — closes sidebar overlay on tap */}
      {mobileNavOpen && (
        <div class="mobile-backdrop" onClick={closeMobileNav} />
      )}

      <aside class={`sidebar${collapsed ? ' collapsed' : ''}${isMobile && mobileNavOpen ? ' mobile-open' : ''}`}>
        <div class="sidebar-header">
          {!collapsed && <div class="sidebar-control-glyph">{Icon.controlCenter}</div>}
          {!collapsed && (
            <div class="sidebar-wordmark">
              <div class="sidebar-wordmark-name">Atlas</div>
            </div>
          )}
          <button
            class="sidebar-collapse-btn"
            onClick={isMobile && mobileNavOpen ? closeMobileNav : toggleCollapsed}
            title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            data-tooltip={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            style={{ marginLeft: collapsed ? undefined : 'auto' }}
          >
            {collapsed ? Icon.expand : Icon.collapse}
          </button>
        </div>

        <nav class="sidebar-nav">

          {/* ── Chat ───────────────────────────────────────── */}
          {(() => {
            const hasChatNotify = (pendingGreetings > 0 || unreadChatReplies > 0) && screen !== 'chat'
            return (
              <div class="nav-group">
                <a
                  class={`nav-item${screen === 'chat' ? ' active' : ''}${hasChatNotify ? ' nav-item--notified' : ''}`}
                  onClick={(e) => { e.preventDefault(); navigate('chat') }}
                  href="#chat"
                  data-tooltip={pendingGreetings > 0 ? 'Atlas has something to tell you' : hasChatNotify ? 'New reply waiting' : 'Chat'}
                  aria-label="Chat"
                >
                  <span class="nav-icon">{hasChatNotify ? Icon.chatActive : Icon.chat}</span>
                  {!collapsed && 'Chat'}
                </a>
              </div>
            )
          })()}

          {NAV_GROUPS.map((group) => {
            const isGroupActive = group.items.some(i => i.id === screen)
            return (
            <div class="nav-group" key={group.label}>
              {!collapsed && (
                <button
                  class={`nav-group-toggle${isGroupActive ? ' nav-group-toggle--active' : ''}`}
                  onClick={() => toggleGroup(group.id)}
                  aria-expanded={expandedGroups[group.id]}
                >
                  <span>{group.label}</span>
                  <span class={`nav-group-caret${expandedGroups[group.id] ? ' expanded' : ''}`}>⌃</span>
                </button>
              )}
              {collapsed && <div class="nav-group-sep" />}
              {(collapsed || expandedGroups[group.id]) && group.items.map(item => {
                const isNotified = (
                  (item.id === 'approvals' && pendingApprovals > 0) ||
                  (item.id === 'forge'     && pendingProposals > 0)
                ) && screen !== item.id
                return (
                <div class="nav-item-stack" key={item.id}>
                  <a
                    class={`nav-item${screen === item.id ? ' active' : ''}${isNotified ? ' nav-item--notified' : ''}`}
                    onClick={(e) => { e.preventDefault(); navigate(item.id) }}
                    href={`#${item.id}`}
                    data-tooltip={item.label}
                    aria-label={item.label}
                  >
                    <span class="nav-icon">{item.icon}</span>
                    {!collapsed && item.label}
                  </a>
                </div>
                )
              })}
            </div>
          )})}

          {/* ── Docs ───────────────────────────────────────── */}
          <div class="nav-group">
            <a
              class={`nav-item${screen === 'docs' ? ' active' : ''}`}
              onClick={(e) => { e.preventDefault(); navigate('docs') }}
              href="#docs"
              data-tooltip="Docs"
              aria-label="Docs"
            >
              <span class="nav-icon">{Icon.docs}</span>
              {!collapsed && 'Docs'}
            </a>
          </div>

        </nav>

        <div class="sidebar-footer">
          {/* Theme icons — placeholder until theming engine is built */}
          {collapsed ? (
            /* Collapsed: show only the active theme icon, centered */
            <div class="theme-strip">
              {activeTheme === 'system' && (
                <button class="theme-btn active" data-tooltip="System" title="System">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                    <rect x="2" y="3" width="20" height="14" rx="2"/>
                    <path d="M8 21h8M12 17v4"/>
                    <path d="M2 10h20"/>
                  </svg>
                </button>
              )}
              {activeTheme === 'light' && (
                <button class="theme-btn active" data-tooltip="Light" title="Light">
                  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                    <circle cx="8" cy="8" r="3" />
                    <path d="M8 1v2M8 13v2M1 8h2M13 8h2M3.1 3.1l1.4 1.4M11.5 11.5l1.4 1.4M12.9 3.1l-1.4 1.4M4.5 11.5l-1.4 1.4" />
                  </svg>
                </button>
              )}
              {activeTheme === 'dark' && (
                <button class="theme-btn active" data-tooltip="Dark" title="Dark">
                  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M13.5 10.5A6 6 0 015.5 2.5a6.5 6.5 0 108 8z" />
                  </svg>
                </button>
              )}
            </div>
          ) : (
            /* Expanded: show all three */
            <div class="theme-strip">
              <button class={`theme-btn${activeTheme === 'system' ? ' active' : ''}`} data-tooltip="System" onClick={() => setActiveTheme('system')}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="2" y="3" width="20" height="14" rx="2"/>
                  <path d="M8 21h8M12 17v4"/>
                  <path d="M2 10h20"/>
                </svg>
              </button>
              <span class="theme-sep" />
              <button class={`theme-btn${activeTheme === 'light' ? ' active' : ''}`} data-tooltip="Light" onClick={() => setActiveTheme('light')}>
                <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="8" cy="8" r="3" />
                  <path d="M8 1v2M8 13v2M1 8h2M13 8h2M3.1 3.1l1.4 1.4M11.5 11.5l1.4 1.4M12.9 3.1l-1.4 1.4M4.5 11.5l-1.4 1.4" />
                </svg>
              </button>
              <span class="theme-sep" />
              <button class={`theme-btn${activeTheme === 'dark' ? ' active' : ''}`} data-tooltip="Dark" onClick={() => setActiveTheme('dark')}>
                <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M13.5 10.5A6 6 0 015.5 2.5a6.5 6.5 0 108 8z" />
                </svg>
              </button>
            </div>
          )}

          {/* Version + status */}
          {collapsed ? (
            <div class="sidebar-collapsed-utilities">
              <div
                class="runtime-status-collapsed"
                title={`Runtime ${statusLabel}`}
                data-tooltip={`Runtime ${statusLabel}`}
                aria-label={`Runtime ${statusLabel}`}
              >
                <span class={dotClass} />
              </div>
            </div>
          ) : (
            <div class="runtime-status">
              <span style={{ color: 'var(--theme-text-muted)', letterSpacing: '0.01em' }}>v0.2</span>
              <span style={{ color: 'var(--theme-text-muted)' }}>—</span>
              <span class={dotClass} />
              <span>{statusLabel}</span>
            </div>
          )}
        </div>
      </aside>

      <HeaderChromeContext.Provider
        value={isMobile ? (
          <button class="mobile-menu-btn" onClick={openMobileNav} aria-label="Open navigation">
            {Icon.hamburger}
          </button>
        ) : null}
      >
      <main>
        {/* Chat is always mounted so its EventSource survives navigation */}
        <div style={{ display: screen === 'chat' ? 'contents' : 'none' }}>
          <Chat
            isActive={screen === 'chat'}
            onUnreadReply={() => setUnreadChatReplies(n => n + 1)}
          />
        </div>
        {screen === 'communications' && <Communications />}
        {screen === 'onboarding'  && <Onboarding onCompleted={() => navigate('chat')} />}
        {screen === 'approvals'   && <Approvals onBadgeChange={setPendingApprovals} onApproved={() => navigate('chat')} />}
        {screen === 'skills'      && <Skills />}
        {screen === 'forge'       && <Forge />}
        {screen === 'automations' && <Automations />}
        {screen === 'workflows'   && <Workflows />}
        {screen === 'dashboards'  && <Dashboards />}
        {screen === 'mind'        && <Mind />}
        {screen === 'activity'    && <Activity />}
        {screen === 'settings'    && <Settings />}
        {screen === 'ai-providers' && <AIProviders />}
        {screen === 'api-keys'    && <APIKeys />}
        {screen === 'theme'       && <Theme activePreset={themeConfig.preset} onPresetChange={setActivePreset} activeTheme={activeTheme} onThemeChange={setActiveTheme} activeAccent={themeConfig.accent} onAccentChange={setActiveAccent} activeDensity={themeConfig.density} onDensityChange={setActiveDensity} activeChatFontSize={themeConfig.chatFontSize} onChatFontSizeChange={setChatFontSize} activeChatRadius={themeConfig.chatRadius} onChatRadiusChange={setChatRadius} activeChatFont={themeConfig.chatFont} onChatFontChange={setChatFont} activeChatAvatarStyle={themeConfig.chatAvatarStyle} onChatAvatarStyleChange={setChatAvatarStyle} />}
        {screen === 'local-lm'    && <LocalLM />}
        {screen === 'usage'       && <Usage />}
        {screen === 'docs'        && <Docs />}
      </main>
      </HeaderChromeContext.Provider>

      <Toaster />
    </div>
  )
}

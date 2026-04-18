import { useEffect, useState } from 'preact/hooks'
import { api } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { AtlasEngine } from './AtlasEngine'
import { AtlasMLX } from './AtlasMLX'
import { AtlasVoice } from './AtlasVoice'

type LocalPanel = 'atlas_engine' | 'atlas_mlx' | 'voice'

const PANELS: { id: LocalPanel; label: string; sublabel: string }[] = [
  { id: 'atlas_engine', label: 'Llama',        sublabel: 'llama.cpp — GGUF models' },
  { id: 'atlas_mlx',   label: 'MLX',          sublabel: 'Apple Silicon inference' },
  { id: 'voice',       label: 'Local Voice',  sublabel: 'Whisper STT · Kokoro TTS' },
]

export function LocalLM() {
  const [panel, setPanel] = useState<LocalPanel>('atlas_engine')

  useEffect(() => {
    api.config().then(cfg => {
      const sel = cfg.selectedLocalEngine as LocalPanel
      if (sel === 'atlas_engine' || sel === 'atlas_mlx' || sel === 'voice') setPanel(sel)
    }).catch(() => {})
  }, [])

  const switchPanel = async (next: LocalPanel) => {
    setPanel(next)
    try { await api.updateConfig({ selectedLocalEngine: next }) } catch { /* non-fatal */ }
  }

  return (
    <div class="screen local-lm-screen">
      <PageHeader
        title="Local LM"
        subtitle="On-device inference — no cloud, no cost."
      />

      {/* Hero card picker */}
      <div style={{ marginBottom: '4px' }}>
        <div class="card ai-provider-setup-card">
          <div class="ai-mode-grid">
            {PANELS.map((p) => (
              <button
                key={p.id}
                type="button"
                class={`card ai-mode-card${panel === p.id ? ' ai-mode-card-selected' : ''}`}
                onClick={() => void switchPanel(p.id)}
              >
                <span class="ai-mode-card-title">{p.label}</span>
                <span class="ai-mode-card-copy">{p.sublabel}</span>
              </button>
            ))}
          </div>
        </div>
      </div>

      {panel === 'atlas_engine' && <AtlasEngine hidePageHeader />}
      {panel === 'atlas_mlx'   && <AtlasMLX   hidePageHeader />}
      {panel === 'voice'       && <AtlasVoice />}
    </div>
  )
}

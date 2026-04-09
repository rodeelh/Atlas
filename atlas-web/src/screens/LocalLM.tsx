import { useEffect, useState } from 'preact/hooks'
import { api } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { AtlasEngine } from './AtlasEngine'
import { AtlasMLX } from './AtlasMLX'

type LocalEngine = 'atlas_engine' | 'atlas_mlx'

export function LocalLM() {
  const [engine, setEngine] = useState<LocalEngine>('atlas_engine')

  // Load configured engine from backend config on mount.
  useEffect(() => {
    api.config().then(cfg => {
      const sel = cfg.selectedLocalEngine as LocalEngine
      if (sel === 'atlas_engine' || sel === 'atlas_mlx') setEngine(sel)
    }).catch(() => {})
  }, [])

  const switchEngine = async (next: LocalEngine) => {
    if (next === engine) return
    setEngine(next)
    try {
      await api.updateConfig({ selectedLocalEngine: next })
    } catch { /* non-fatal — UI already updated */ }
  }

  return (
    <div class="screen">
      <PageHeader
        title="Local LM"
        subtitle="On-device inference — no cloud, no cost."
        actions={
          <div class="segmented-ctrl" role="group" aria-label="Local engine">
            <button
              class={`segmented-ctrl__btn${engine === 'atlas_engine' ? ' active' : ''}`}
              onClick={() => switchEngine('atlas_engine')}
            >
              Llama
            </button>
            <button
              class={`segmented-ctrl__btn${engine === 'atlas_mlx' ? ' active' : ''}`}
              onClick={() => switchEngine('atlas_mlx')}
            >
              MLX
            </button>
          </div>
        }
      />

      {engine === 'atlas_engine'
        ? <AtlasEngine hidePageHeader />
        : <AtlasMLX   hidePageHeader />}
    </div>
  )
}

import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'

export function Docs() {
  return (
    <div class="screen">
      <PageHeader title="Docs" />
      <EmptyState
        icon={<svg viewBox="0 0 36 36" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><rect x="8" y="4" width="20" height="28" rx="2" /><path d="M13 11h10M13 17h10M13 23h6" /></svg>}
        title="Documentation coming soon"
        body="Guides and reference material for Atlas will appear here."
      />
    </div>
  )
}

import { ComponentChildren, createContext } from 'preact'
import { useContext } from 'preact/hooks'

export const HeaderChromeContext = createContext<ComponentChildren>(null)

interface PageHeaderProps {
  title: string
  subtitle: ComponentChildren
  actions?: ComponentChildren
}

/**
 * Shared top-bar used by every screen.
 * When rendered as a direct child of .screen it negates the screen padding
 * so the border-bottom spans edge-to-edge. Outside .screen (Chat) it sits
 * naturally as a flex sibling.
 */
export function PageHeader({ title, subtitle, actions }: PageHeaderProps) {
  const mobileLead = useContext(HeaderChromeContext)

  return (
    <div class="page-header">
      {mobileLead && <div class="page-header-mobile-lead">{mobileLead}</div>}
      <div class="page-header-left">
        <div class="page-header-title">
          {title}
        </div>
        <div class="page-header-sub">{subtitle}</div>
      </div>
      {actions && <div class="page-header-actions">{actions}</div>}
    </div>
  )
}

import { ComponentChildren, VNode } from 'preact'
import { createPortal } from 'preact/compat'

/**
 * Renders children directly into document.body, escaping any parent
 * stacking context (e.g. `main { z-index: 1 }` would otherwise trap
 * position:fixed modal overlays below the sidebar at z-index: 30).
 */
export function Portal({ children }: { children: ComponentChildren }) {
  return createPortal(children, document.body) as unknown as VNode
}

import { describe, expect, it } from 'vitest'
import { buildDocumentTitle, extractStreamError } from './chatStream'

describe('extractStreamError', () => {
  it('prefers the explicit error field', () => {
    expect(extractStreamError({ type: 'error', error: 'Provider unavailable', message: 'fallback' })).toBe('Provider unavailable')
  })

  it('falls back to the legacy message field', () => {
    expect(extractStreamError({ type: 'error', message: 'Legacy error' })).toBe('Legacy error')
  })

  it('returns a generic message when neither field is present', () => {
    expect(extractStreamError({ type: 'error' })).toBe('An error occurred.')
  })
})

describe('buildDocumentTitle', () => {
  it('adds a badge count when notifications are pending', () => {
    expect(buildDocumentTitle('Atlas', { pendingGreetings: 1, unreadReplies: 2, pendingApprovals: 0 })).toBe('(3) Atlas')
  })

  it('keeps the base title when nothing is pending', () => {
    expect(buildDocumentTitle('Atlas', { pendingGreetings: 0, unreadReplies: 0, pendingApprovals: 0 })).toBe('Atlas')
  })
})

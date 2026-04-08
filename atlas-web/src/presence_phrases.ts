// presence_phrases.ts — phrase library + selector for the "Atlas was
// thinking…" presence line in the chat empty state.
//
// Design:
//   - Phrases read like things a person would say about another person's
//     inner life. No status text, no counts, no "thinking (2)".
//   - Mix of contemplative (most), casual (some), poetic (sparingly).
//   - Selection is seeded by (day, thoughtCount) so the phrase is stable
//     within a session AND across refreshes on the same day, but drifts
//     naturally as days pass and when the thought count changes.
//   - Singular vs plural pools are separate — a single thought and a
//     few thoughts read differently in natural prose.
//
// The selector is a pure function. No random, no Date.now() — the
// caller passes the current time so tests can pin it.

/** Phrases used when exactly one thought is active. */
export const SINGULAR_PRESENCE_PHRASES: string[] = [
  'Atlas was turning something over',
  'Atlas has been sitting with an idea',
  'Something crossed Atlas\u2019s mind',
  'A thought is lingering',
  'Atlas was mulling something over',
  'Something has been on Atlas\u2019s mind',
  'Atlas noticed something earlier',
  'A notion has been settling',
  'Atlas was thinking about something',
  'Atlas had a thought while you were away',
  'A thought took shape while you were gone',
  'Something quiet has been forming',
  'Atlas was listening to an idea',
]

/** Phrases used when two or more thoughts are active. */
export const PLURAL_PRESENCE_PHRASES: string[] = [
  'Atlas has been thinking about a few things',
  'A couple of thoughts have been lingering',
  'Atlas has been turning a few things over',
  'Some quiet thoughts have been stirring',
  'A few things have been on Atlas\u2019s mind',
  'Atlas has been noticing a few things',
  'A handful of ideas have been forming',
  'Atlas has been sitting with a few observations',
]

/**
 * pickPresencePhrase returns one phrase from the appropriate pool,
 * deterministic for a given (day, thoughtCount) pair.
 *
 * The seed mixes the day bucket (floor of now / 24h) with the count
 * and a small odd multiplier so small count changes produce meaningful
 * drift through the pool rather than just +1 offsets.
 *
 * @param thoughtCount how many thoughts are currently on Atlas's mind
 * @param nowMs the current time in milliseconds since epoch
 */
export function pickPresencePhrase(thoughtCount: number, nowMs: number): string {
  const pool = thoughtCount > 1 ? PLURAL_PRESENCE_PHRASES : SINGULAR_PRESENCE_PHRASES
  if (pool.length === 0) return 'Atlas was thinking'

  // Day bucket in UTC so TZ shifts don't cause sub-day phrase flicker.
  const dayMs = 24 * 60 * 60 * 1000
  const dayBucket = Math.floor(nowMs / dayMs)

  // Prime-ish mixing so the phrase moves meaningfully across the pool
  // as day or count ticks up. Small numbers, no cryptographic need.
  const seed = (dayBucket * 31 + thoughtCount * 17) % pool.length
  const idx = seed < 0 ? seed + pool.length : seed
  return pool[idx]
}

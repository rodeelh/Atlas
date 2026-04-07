// voicePlayback.ts — truly gapless playback of streamed TTS PCM chunks.
//
// Architecture: ONE shared AudioContext, and per-play a ScriptProcessorNode
// with an internal Float32 ring buffer. Incoming base64 PCM chunks are
// decoded directly (int16 → float32) and appended to the buffer; the
// processor drains the buffer into the audio output every render quantum.
//
// This replaces the previous mini-WAV + decodeAudioData approach, which
// produced audible clicks at chunk boundaries because each decoded buffer
// was padded/normalized independently.
//
// Autoplay policy: warmupAudioContext() resumes the shared context from a
// user gesture so later network-triggered playback is unblocked.

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AudioContextClass = typeof AudioContext
function getAudioContextClass(): AudioContextClass | null {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return ((window as any).AudioContext || (window as any).webkitAudioContext) ?? null
}

let sharedAudioContext: AudioContext | null = null

export function warmupAudioContext(): void {
  const AC = getAudioContextClass()
  if (!AC) { console.warn('[voice] AudioContext unavailable'); return }
  if (!sharedAudioContext || sharedAudioContext.state === 'closed') {
    try {
      sharedAudioContext = new AC()
      console.log('[voice] created AudioContext, state=', sharedAudioContext.state, 'sampleRate=', sharedAudioContext.sampleRate)
    } catch (err) { console.error('[voice] new AudioContext failed', err); return }
  }
  if (sharedAudioContext.state === 'suspended') {
    sharedAudioContext.resume().then(() => {
      console.log('[voice] resume() ok, state=', sharedAudioContext?.state)
    }).catch((err) => { console.warn('[voice] resume() failed', err) })
  }
}

function getSharedContext(): AudioContext {
  const AC = getAudioContextClass()
  if (!AC) throw new Error('AudioContext is not available in this browser.')
  if (!sharedAudioContext || sharedAudioContext.state === 'closed') {
    sharedAudioContext = new AC()
  }
  if (sharedAudioContext.state === 'suspended') {
    sharedAudioContext.resume().catch(() => { /* ignore */ })
  }
  return sharedAudioContext
}

export interface VoicePlayer {
  /** Append one chunk of raw PCM (int16 LE mono, base64-encoded) at the given source sample rate. */
  enqueueChunk(b64: string, index: number, sourceSampleRate: number): void
  /** Signal no more chunks will arrive; onFinished fires once the buffer drains. */
  finish(): void
  /** Abort playback immediately, discarding any queued audio. */
  stop(): void
  onFinished?: () => void
  onError?: (message: string) => void
  readonly playing: boolean
}

// Processor render buffer size — 4096 samples × 2 buffers worth of latency at
// ctx.sampleRate. 4096 is a safe mid-range that works across browsers without
// glitching on main-thread work.
const PROCESSOR_BUFFER = 4096

export function createVoicePlayer(): VoicePlayer {
  const ctx = getSharedContext()
  const targetRate = ctx.sampleRate

  let ringBuffer: Float32Array = new Float32Array(0)
  let readPos = 0
  let ended = false
  let finishedFired = false
  let stopped = false
  let sourceRate = 0           // inferred from first chunk
  let resampleRemainder = 0    // fractional index carry between chunks for resampler

  // ScriptProcessorNode is deprecated but universally available and avoids the
  // AudioWorklet module-loading complexity. For this use case (short-lived
  // speech playback, no tight real-time constraints beyond "don't glitch"),
  // it's entirely adequate.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const proc = (ctx as any).createScriptProcessor(PROCESSOR_BUFFER, 0, 1) as ScriptProcessorNode
  proc.connect(ctx.destination)

  proc.onaudioprocess = (event: AudioProcessingEvent) => {
    const out = event.outputBuffer.getChannelData(0)
    const need = out.length
    const available = ringBuffer.length - readPos
    const copy = Math.min(available, need)
    if (copy > 0) {
      out.set(ringBuffer.subarray(readPos, readPos + copy))
      readPos += copy
    }
    // Fill any remaining samples with silence
    for (let i = copy; i < need; i++) out[i] = 0

    // Drained? Fire onFinished once after end is signaled.
    if (ended && readPos >= ringBuffer.length && !finishedFired) {
      finishedFired = true
      // Delay the callback slightly so the final audio actually plays out.
      setTimeout(() => {
        if (stopped) return
        try { player.onFinished?.() } catch { /* ignore */ }
      }, 50)
    }
  }

  function enqueuePCM(samples: Float32Array) {
    if (stopped) return
    // Compact the ring buffer (drop already-played samples) then append.
    const remaining = ringBuffer.length - readPos
    const next = new Float32Array(remaining + samples.length)
    if (remaining > 0) next.set(ringBuffer.subarray(readPos))
    next.set(samples, remaining)
    ringBuffer = next
    readPos = 0
  }

  const player: VoicePlayer = {
    enqueueChunk(b64: string, index: number, chunkSampleRate: number) {
      if (stopped) return
      try {
        // Decode base64 → Int16Array → Float32Array
        const binary = atob(b64)
        const bytes = new Uint8Array(binary.length)
        for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
        // int16 little-endian → float32 normalized to [-1, 1]
        const sampleCount = bytes.length >> 1
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        let float: Float32Array = new Float32Array(sampleCount) as any
        const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
        for (let i = 0; i < sampleCount; i++) {
          float[i] = view.getInt16(i * 2, true) / 32768
        }

        // Lock in source rate on first chunk.
        if (sourceRate === 0) sourceRate = chunkSampleRate || 22050

        // Resample linearly if source rate differs from destination. Linear
        // interpolation is fine for speech — no one perceives aliasing on
        // a 22 kHz → 48 kHz upsample for a monophonic voice signal.
        if (sourceRate !== targetRate) {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          float = resampleLinear(float, sourceRate, targetRate, resampleRemainder) as any
          // Store the carried fractional offset for the next chunk.
          resampleRemainder = resampleLinearRemainder
        }

        enqueuePCM(float)
        if (index === 0) console.log('[voice] first PCM chunk enqueued, sourceRate=', sourceRate, 'targetRate=', targetRate, 'samples=', float.length)
      } catch (err) {
        console.error('[voice] enqueue failed', err)
        player.onError?.(err instanceof Error ? err.message : 'decode failed')
      }
    },
    finish() {
      ended = true
      console.log('[voice] finish() — waiting for ring buffer to drain')
    },
    stop() {
      if (stopped) return
      stopped = true
      ringBuffer = new Float32Array(0)
      readPos = 0
      try { proc.disconnect() } catch { /* ignore */ }
      proc.onaudioprocess = null
    },
    get playing() {
      return !stopped && readPos < ringBuffer.length
    },
  }

  return player
}

// Module-level state for the fractional carry between consecutive calls to
// resampleLinear. Exported-adjacent rather than embedded in the function so
// each player instance can capture it; createVoicePlayer reads this after
// each call. Not thread-safe, but JS is single-threaded.
let resampleLinearRemainder = 0

function resampleLinear(
  input: Float32Array,
  inRate: number,
  outRate: number,
  startOffset: number,
): Float32Array {
  if (inRate === outRate) {
    resampleLinearRemainder = 0
    return input
  }
  const ratio = inRate / outRate
  // Number of output samples we can produce from this input without reading past the end.
  const firstSrcIdx = startOffset
  const maxOutLen = Math.max(0, Math.floor((input.length - 1 - firstSrcIdx) / ratio) + 1)
  const out = new Float32Array(maxOutLen)
  let srcIdx = firstSrcIdx
  for (let i = 0; i < maxOutLen; i++) {
    const i0 = Math.floor(srcIdx)
    const i1 = Math.min(i0 + 1, input.length - 1)
    const frac = srcIdx - i0
    out[i] = input[i0] * (1 - frac) + input[i1] * frac
    srcIdx += ratio
  }
  // Carry the fractional offset (relative to the END of this chunk) into the next chunk.
  // srcIdx now points just past the last sample we read. The unused part of the
  // input is input.length − srcIdx; for the next chunk, we start at (srcIdx − input.length).
  resampleLinearRemainder = srcIdx - input.length
  if (resampleLinearRemainder < 0) resampleLinearRemainder = 0
  return out
}

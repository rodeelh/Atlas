// voiceSpeech.ts — microphone capture + Whisper transcription via the Atlas runtime.
//
// This replaces the Web Speech API wrapper (browserSpeech.ts). The runtime
// manages the Whisper subprocess; this module captures microphone audio and
// POSTs the blob to /voice/transcribe, returning the final transcript.
//
// The session shape matches browserSpeech's BrowserSpeechSession so Chat.tsx
// can swap the import without restructuring its state.

export interface VoiceSpeechUpdate {
  finalText: string
  interimText: string
}

export interface VoiceSpeechSession {
  stop(): void
}

type TranscribeFn = (blob: Blob, language?: string) => Promise<{ text: string }>

interface StartVoiceSpeechOptions {
  lang?: string
  maxDurationMs?: number
  skipWavConversion?: boolean
  onStart?: () => void
  onRecording?: (elapsedMs: number) => void
  onResult: (update: VoiceSpeechUpdate) => void
  onError?: (message: string) => void
  onEnd?: () => void
  transcribe: TranscribeFn
}

/** Returns true if this browser has both getUserMedia and MediaRecorder. */
export function voiceSpeechSupported(): boolean {
  if (typeof navigator === 'undefined' || typeof window === 'undefined') return false
  if (!navigator.mediaDevices || typeof navigator.mediaDevices.getUserMedia !== 'function') return false
  if (typeof (window as unknown as { MediaRecorder?: unknown }).MediaRecorder === 'undefined') return false
  return true
}

function pickMimeType(): string {
  const candidates = [
    'audio/webm;codecs=opus',
    'audio/webm',
    'audio/ogg;codecs=opus',
    'audio/mp4',
    'audio/wav',
  ]
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const MR = (window as any).MediaRecorder
  if (MR && typeof MR.isTypeSupported === 'function') {
    for (const m of candidates) {
      if (MR.isTypeSupported(m)) return m
    }
  }
  return ''
}

/**
 * Start microphone capture. Returns a session whose `stop()` finalizes the
 * recording, uploads it to the runtime for transcription, and fires `onResult`
 * with the transcript. If the user cancels mid-recording call `stop()` — the
 * recorded audio is still transcribed (a very short blob may return empty text).
 */
export function startVoiceSpeech(options: StartVoiceSpeechOptions): VoiceSpeechSession {
  if (!voiceSpeechSupported()) {
    throw new Error('Voice input is not available in this browser.')
  }

  const maxMs = options.maxDurationMs ?? 30_000
  const chunks: Blob[] = []
  let recorder: MediaRecorder | null = null
  let stream: MediaStream | null = null
  let ended = false
  let stopTimeout: ReturnType<typeof setTimeout> | null = null
  let tickTimer: ReturnType<typeof setInterval> | null = null
  let startedAt = 0

  const cleanup = () => {
    if (stopTimeout) { clearTimeout(stopTimeout); stopTimeout = null }
    if (tickTimer) { clearInterval(tickTimer); tickTimer = null }
    if (stream) {
      for (const track of stream.getTracks()) track.stop()
      stream = null
    }
    recorder = null
  }

  const finish = async () => {
    if (ended) return
    ended = true
    try {
      const mimeType = chunks[0]?.type || pickMimeType() || 'audio/webm'
      const blob = new Blob(chunks, { type: mimeType })
      cleanup()
      if (blob.size === 0) {
        options.onResult({ finalText: '', interimText: '' })
        options.onEnd?.()
        return
      }
      // whisper.cpp requires 16 kHz mono WAV. Cloud providers (OpenAI, Gemini)
      // accept raw WebM directly — skip the conversion for them.
      const uploadBlob = options.skipWavConversion ? blob : await encodeBlobAsWav(blob)
      const result = await options.transcribe(uploadBlob, options.lang?.split('-')[0])
      options.onResult({ finalText: (result.text || '').trim(), interimText: '' })
      options.onEnd?.()
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Voice transcription failed.'
      options.onError?.(message)
      cleanup()
      options.onEnd?.()
    }
  }

  navigator.mediaDevices
    .getUserMedia({ audio: true })
    .then((ms) => {
      stream = ms
      const mimeType = pickMimeType()
      recorder = mimeType ? new MediaRecorder(ms, { mimeType }) : new MediaRecorder(ms)
      recorder.ondataavailable = (event) => {
        if (event.data && event.data.size > 0) chunks.push(event.data)
      }
      recorder.onerror = (event) => {
        const anyEvt = event as unknown as { error?: { message?: string } }
        const msg = anyEvt.error?.message || 'Voice recording error.'
        options.onError?.(msg)
        cleanup()
        options.onEnd?.()
      }
      recorder.onstop = () => { void finish() }

      recorder.start()
      startedAt = Date.now()
      options.onStart?.()

      if (options.onRecording) {
        tickTimer = setInterval(() => {
          options.onRecording?.(Date.now() - startedAt)
        }, 100)
      }

      stopTimeout = setTimeout(() => {
        if (recorder && recorder.state === 'recording') recorder.stop()
      }, maxMs)
    })
    .catch((err) => {
      const message = err instanceof Error ? err.message : 'Microphone access was denied.'
      options.onError?.(message)
      options.onEnd?.()
    })

  return {
    stop() {
      if (ended) return
      try {
        if (recorder && recorder.state === 'recording') {
          recorder.stop() // triggers onstop → finish()
          return
        }
      } catch { /* ignore */ }
      void finish()
    },
  }
}

// ── WAV encoding ─────────────────────────────────────────────────────────────
// Decode any audio blob via AudioContext, downmix to mono, resample to 16 kHz,
// and wrap as a 16-bit PCM WAV file. whisper-server accepts this directly.

async function encodeBlobAsWav(blob: Blob): Promise<Blob> {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const AC: typeof AudioContext = (window as any).AudioContext || (window as any).webkitAudioContext
  if (!AC) throw new Error('AudioContext is not available in this browser.')
  const ctx = new AC()
  try {
    const arrayBuf = await blob.arrayBuffer()
    const decoded = await ctx.decodeAudioData(arrayBuf.slice(0))
    const target = 16000
    const mono = mixdownToMono(decoded)
    const resampled = decoded.sampleRate === target ? mono : resampleLinear(mono, decoded.sampleRate, target)
    return encodePCM16Wav(resampled, target)
  } finally {
    try { await ctx.close() } catch { /* ignore */ }
  }
}

function mixdownToMono(buffer: AudioBuffer): Float32Array {
  if (buffer.numberOfChannels === 1) return buffer.getChannelData(0).slice()
  const len = buffer.length
  const out = new Float32Array(len)
  for (let ch = 0; ch < buffer.numberOfChannels; ch++) {
    const data = buffer.getChannelData(ch)
    for (let i = 0; i < len; i++) out[i] += data[i]
  }
  const n = buffer.numberOfChannels
  for (let i = 0; i < len; i++) out[i] /= n
  return out
}

function resampleLinear(input: Float32Array, inRate: number, outRate: number): Float32Array {
  if (inRate === outRate) return input
  const ratio = inRate / outRate
  const outLen = Math.round(input.length / ratio)
  const out = new Float32Array(outLen)
  for (let i = 0; i < outLen; i++) {
    const srcIdx = i * ratio
    const i0 = Math.floor(srcIdx)
    const i1 = Math.min(i0 + 1, input.length - 1)
    const frac = srcIdx - i0
    out[i] = input[i0] * (1 - frac) + input[i1] * frac
  }
  return out
}

function encodePCM16Wav(samples: Float32Array, sampleRate: number): Blob {
  const bytesPerSample = 2
  const blockAlign = bytesPerSample // mono
  const byteRate = sampleRate * blockAlign
  const dataSize = samples.length * bytesPerSample
  const buffer = new ArrayBuffer(44 + dataSize)
  const view = new DataView(buffer)

  // RIFF header
  writeStr(view, 0, 'RIFF')
  view.setUint32(4, 36 + dataSize, true)
  writeStr(view, 8, 'WAVE')
  writeStr(view, 12, 'fmt ')
  view.setUint32(16, 16, true)       // PCM chunk size
  view.setUint16(20, 1, true)        // PCM format
  view.setUint16(22, 1, true)        // channels
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, byteRate, true)
  view.setUint16(32, blockAlign, true)
  view.setUint16(34, 16, true)       // bits per sample
  writeStr(view, 36, 'data')
  view.setUint32(40, dataSize, true)

  // samples
  let offset = 44
  for (let i = 0; i < samples.length; i++, offset += 2) {
    const s = Math.max(-1, Math.min(1, samples[i]))
    view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7fff, true)
  }
  return new Blob([buffer], { type: 'audio/wav' })
}

function writeStr(view: DataView, offset: number, str: string): void {
  for (let i = 0; i < str.length; i++) view.setUint8(offset + i, str.charCodeAt(i))
}

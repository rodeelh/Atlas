type BrowserSpeechConstructor = new () => BrowserSpeechRecognition

interface BrowserSpeechRecognitionAlternative {
  transcript: string
}

interface BrowserSpeechRecognitionResult {
  isFinal: boolean
  0: BrowserSpeechRecognitionAlternative
}

interface BrowserSpeechRecognitionResultList {
  length: number
  [index: number]: BrowserSpeechRecognitionResult
}

interface BrowserSpeechRecognitionEvent {
  resultIndex: number
  results: BrowserSpeechRecognitionResultList
}

interface BrowserSpeechRecognitionErrorEvent {
  error: string
  message?: string
}

interface BrowserSpeechRecognition {
  continuous: boolean
  interimResults: boolean
  lang: string
  maxAlternatives: number
  onstart: (() => void) | null
  onresult: ((event: BrowserSpeechRecognitionEvent) => void) | null
  onerror: ((event: BrowserSpeechRecognitionErrorEvent) => void) | null
  onend: (() => void) | null
  start(): void
  stop(): void
}

interface BrowserSpeechWindow extends Window {
  SpeechRecognition?: BrowserSpeechConstructor
  webkitSpeechRecognition?: BrowserSpeechConstructor
}

export interface BrowserSpeechUpdate {
  finalText: string
  interimText: string
}

export interface BrowserSpeechSession {
  stop(): void
}

interface StartBrowserSpeechOptions {
  lang?: string
  onStart?: () => void
  onResult: (update: BrowserSpeechUpdate) => void
  onError?: (message: string) => void
  onEnd?: () => void
}

function speechRecognitionCtor(): BrowserSpeechConstructor | null {
  if (typeof window === 'undefined') return null
  const speechWindow = window as BrowserSpeechWindow
  return speechWindow.SpeechRecognition ?? speechWindow.webkitSpeechRecognition ?? null
}

function mapRecognitionError(errorCode: string, message?: string): string {
  switch (errorCode) {
    case 'not-allowed':
    case 'service-not-allowed':
      return 'Microphone access was blocked for browser voice input.'
    case 'audio-capture':
      return 'No microphone was available for browser voice input.'
    case 'language-not-supported':
      return 'This browser does not support voice input for the current language.'
    case 'network':
      return 'Browser voice input failed due to a network error.'
    case 'aborted':
      return 'Browser voice input was interrupted.'
    case 'no-speech':
      return 'No speech was detected.'
    default:
      return message?.trim() || 'Browser voice input failed.'
  }
}

export function browserSpeechSupported(): boolean {
  return speechRecognitionCtor() !== null
}

export function startBrowserSpeech(options: StartBrowserSpeechOptions): BrowserSpeechSession {
  const SpeechRecognition = speechRecognitionCtor()
  if (!SpeechRecognition) {
    throw new Error('Browser speech recognition is unavailable.')
  }

  const recognition = new SpeechRecognition()
  recognition.continuous = true
  recognition.interimResults = true
  recognition.maxAlternatives = 1
  recognition.lang = options.lang || 'en-US'

  recognition.onstart = () => {
    options.onStart?.()
  }

  recognition.onresult = (event) => {
    let finalText = ''
    let interimText = ''
    for (let i = event.resultIndex; i < event.results.length; i += 1) {
      const transcript = event.results[i]?.[0]?.transcript ?? ''
      if (!transcript) continue
      if (event.results[i].isFinal) {
        finalText += transcript
      } else {
        interimText += transcript
      }
    }
    options.onResult({ finalText, interimText })
  }

  recognition.onerror = (event) => {
    options.onError?.(mapRecognitionError(event.error, event.message))
  }

  recognition.onend = () => {
    options.onEnd?.()
  }

  recognition.start()

  return {
    stop() {
      recognition.stop()
    },
  }
}

/**
 * parseModelInfo — extracts a human-readable display name and quantization
 * tag from a GGUF filename.
 *
 * Examples:
 *   "qwen2.5-3b-instruct-q4_k_m.gguf"  → { display: "Qwen 2.5 3B Instruct", quant: "Q4_K_M" }
 *   "gemma-3-4b-it-Q4_K_M.gguf"        → { display: "Gemma 3 4B It",         quant: "Q4_K_M" }
 *   "phi-4-mini-instruct-Q4_K_M.gguf"  → { display: "Phi 4 Mini Instruct",   quant: "Q4_K_M" }
 *   "llama3.2"                          → { display: "Llama3.2",              quant: null }
 */
export function parseModelInfo(filename: string): { display: string; quant: string | null } {
  const base = filename.replace(/\.gguf$/i, '')
  // Match common quant patterns at the end: Q4_K_M, Q8_0, IQ2_M, IQ3_XS, F16, BF16, etc.
  const quantRe = /[-_]((?:IQ|Q)\d+[_A-Za-z0-9]*|[BF]16|f16|bf16)$/i
  const quantMatch = base.match(quantRe)
  const quant = quantMatch ? quantMatch[1].toUpperCase() : null
  const nameBase = quantMatch ? base.slice(0, quantMatch.index) : base
  const display = nameBase
    .replace(/[-_.]/g, ' ')
    .replace(/\s+/g, ' ')
    .replace(/\b(\w)/g, c => c.toUpperCase())
    .trim()
  return { display, quant }
}

/**
 * parseMLXModelInfo — extracts a human-readable display name and quantization
 * tag from an MLX model directory name.
 *
 * Examples:
 *   "gemma-4-e2b-it-4bit"              → { display: "Gemma 4 E2B It", quant: "4bit" }
 *   "Llama-3.2-3B-Instruct-4bit"       → { display: "Llama 3.2 3B Instruct", quant: "4bit" }
 *   "Qwen2.5-7B-Instruct-8bit"         → { display: "Qwen2.5 7B Instruct", quant: "8bit" }
 */
export function parseMLXModelInfo(name: string): { display: string; quant: string | null } {
  // Model may be a full path — extract the basename.
  const base = name.split('/').pop() || name
  const quantMatch = base.match(/-((?:\d+)bit)$/i)
  const quant = quantMatch ? quantMatch[1].toLowerCase() : null
  const nameBase = quantMatch ? base.slice(0, quantMatch.index) : base
  const display = nameBase
    .replace(/[-_.]/g, ' ')
    .replace(/\s+/g, ' ')
    .replace(/\b(\w)/g, c => c.toUpperCase())
    .trim()
  return { display, quant }
}

/**
 * formatAtlasModelName — returns a single display string combining the
 * parsed name and quantization tag, suitable for compact UI labels.
 *
 * "qwen2.5-3b-instruct-q4_k_m.gguf" → "Qwen 2.5 3B Instruct · Q4_K_M"
 * "gemma-3-4b-it-Q4_K_M.gguf"       → "Gemma 3 4B It · Q4_K_M"
 * "llama3.2"                          → "Llama3.2"
 */
export function formatAtlasModelName(filename: string): string {
  if (!filename) return filename
  const { display, quant } = parseModelInfo(filename)
  return quant ? `${display} · ${quant}` : display
}

function titleCaseWords(input: string): string {
  return input
    .replace(/[-_./:]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/\b\w/g, (c) => c.toUpperCase())
}

function formatOpenAIModel(id: string): string {
  const lower = id.toLowerCase()
  if (lower.startsWith('gpt-5.4-mini')) return 'GPT-5.4 Mini'
  if (lower.startsWith('gpt-5.4-nano')) return 'GPT-5.4 Nano'
  if (lower.startsWith('gpt-5.4-pro')) return 'GPT-5.4 Pro'
  if (lower.startsWith('gpt-5.4')) return 'GPT-5.4'
  if (lower.startsWith('gpt-5-mini')) return 'GPT-5 Mini'
  if (lower.startsWith('gpt-5-nano')) return 'GPT-5 Nano'
  if (lower.startsWith('gpt-5-pro')) return 'GPT-5 Pro'
  if (lower.startsWith('gpt-5')) return 'GPT-5'
  if (lower.startsWith('gpt-4.1-mini')) return 'GPT-4.1 Mini'
  if (lower.startsWith('gpt-4.1-nano')) return 'GPT-4.1 Nano'
  if (lower.startsWith('gpt-4.1')) return 'GPT-4.1'
  if (lower.startsWith('gpt-4o-mini')) return 'GPT-4o Mini'
  if (lower.startsWith('gpt-4o')) return 'GPT-4o'
  if (lower.startsWith('gpt-4-turbo')) return 'GPT-4 Turbo'
  if (lower.startsWith('gpt-4')) return 'GPT-4'
  if (lower.startsWith('o4-mini')) return 'O4 Mini'
  if (lower.startsWith('o3-mini')) return 'O3 Mini'
  if (lower.startsWith('o3')) return 'O3'
  return id
}

function formatAnthropicModel(id: string): string {
  const noDate = id.replace(/-\d{8}$/, '')
  if (noDate.startsWith('claude-sonnet-')) return `Claude Sonnet ${noDate.replace('claude-sonnet-', '').replace(/-/g, '.')}`
  if (noDate.startsWith('claude-opus-')) return `Claude Opus ${noDate.replace('claude-opus-', '').replace(/-/g, '.')}`
  if (noDate.startsWith('claude-haiku-')) return `Claude Haiku ${noDate.replace('claude-haiku-', '').replace(/-/g, '.')}`
  if (noDate.startsWith('claude-3-5-sonnet')) return 'Claude 3.5 Sonnet'
  if (noDate.startsWith('claude-3-5-haiku')) return 'Claude 3.5 Haiku'
  if (noDate.startsWith('claude-3-haiku')) return 'Claude 3 Haiku'
  return id
}

function formatGeminiModel(id: string): string {
  const bare = id.replace(/^models\//, '')
  if (bare.startsWith('gemini-2.5-pro')) return 'Gemini 2.5 Pro'
  if (bare.startsWith('gemini-2.5-flash-lite')) return 'Gemini 2.5 Flash Lite'
  if (bare.startsWith('gemini-2.5-flash')) return 'Gemini 2.5 Flash'
  if (bare.startsWith('gemini-2.0-pro')) return 'Gemini 2.0 Pro'
  if (bare.startsWith('gemini-2.0-flash-lite')) return 'Gemini 2.0 Flash Lite'
  if (bare.startsWith('gemini-2.0-flash')) return 'Gemini 2.0 Flash'
  if (bare.startsWith('gemini-1.5-pro')) return 'Gemini 1.5 Pro'
  if (bare.startsWith('gemini-1.5-flash-8b')) return 'Gemini 1.5 Flash 8B'
  if (bare.startsWith('gemini-1.5-flash')) return 'Gemini 1.5 Flash'
  if (bare.startsWith('gemini-3.1-pro')) return 'Gemini 3.1 Pro'
  if (bare.startsWith('gemini-3.1-flash')) return 'Gemini 3.1 Flash'
  if (bare.startsWith('gemini-3-pro')) return 'Gemini 3 Pro'
  if (bare.startsWith('gemini-3-flash')) return 'Gemini 3 Flash'
  return bare
}

function formatOpenRouterModel(id: string): string {
  const bare = id.trim()
  if (!bare) return bare
  if (bare === 'openrouter/auto:free') return 'Free Models Router'
  if (bare === 'openrouter/auto') return 'OpenRouter Auto Router'
  const [org, restRaw] = bare.includes('/') ? bare.split('/', 2) : ['', bare]
  const rest = restRaw || bare
  const free = rest.endsWith(':free')
  const core = free ? rest.replace(/:free$/, '') : rest
  const pretty = titleCaseWords(core)
  if (org && org !== 'openrouter') {
    return free ? `${pretty} (Free)` : pretty
  }
  return free ? `${pretty} (Free)` : pretty
}

export function formatProviderModelName(provider: string, model: string): string {
  const raw = (model || '').trim()
  if (!raw) return raw
  switch (provider) {
    case 'atlas_engine':
      return formatAtlasModelName(raw)
    case 'atlas_mlx': {
      // Model may be a full path (/…/mlx-models/gemma-4-e2b-it-4bit) or bare name.
      const base = raw.split('/').pop() || raw
      // Separate trailing quantization suffix (e.g. "4bit", "8bit") from the model name.
      const quantMatch = base.match(/-((?:\d+)bit)$/i)
      const quant = quantMatch ? quantMatch[1].toLowerCase() : null
      const nameBase = quantMatch ? base.slice(0, quantMatch.index) : base
      const display = nameBase
        .replace(/[-_.]/g, ' ')
        .replace(/\s+/g, ' ')
        .replace(/\b(\w)/g, c => c.toUpperCase())
        .trim()
      return quant ? `${display} · ${quant}` : display
    }
    case 'openai':
      return formatOpenAIModel(raw)
    case 'anthropic':
      return formatAnthropicModel(raw)
    case 'gemini':
      return formatGeminiModel(raw)
    case 'openrouter':
      return formatOpenRouterModel(raw)
    default:
      return raw
  }
}

import { describe, expect, it } from 'bun:test'
import {
  cachedTokensFor,
  fmt,
  latencyBadgeTone,
  latencyClass,
  latencyTextClass,
  providerDisplayName,
  ttftRatio,
  type RequestDetailItem,
} from './types'

describe('request detail token formatting', () => {
  it('prefers canonical cached_tokens', () => {
    const detail: RequestDetailItem = {
      tokens: { cached_tokens: 120, cache_read_input_tokens: 90 },
    }

    expect(cachedTokensFor(detail)).toBe(120)
    expect(fmt(cachedTokensFor(detail))).toBe('120')
  })

  it('supports reasoning tokens and token compression details', () => {
    const detail: RequestDetailItem = {
      tokens: {
        prompt_tokens: 203897,
        completion_tokens: 1500,
        reasoning_tokens: 800,
        original_input_tokens: 369000,
        saved_tokens: 165103,
        saved_percent: 45,
      },
    }

    expect(detail.tokens?.prompt_tokens).toBe(203897)
    expect(detail.tokens?.original_input_tokens).toBe(369000)
    expect(detail.tokens?.saved_tokens).toBe(165103)
    expect(detail.tokens?.saved_percent).toBe(45)
    expect(detail.tokens?.reasoning_tokens).toBe(800)
    expect(
      `Compressed: ${fmt(detail.tokens?.original_input_tokens)} → ${fmt(detail.tokens?.prompt_tokens)} (${detail.tokens?.saved_percent}% saved)`
    ).toBe('Compressed: 369,000 → 203,897 (45% saved)')
  })

  it('falls back to legacy cache_read_input_tokens', () => {
    expect(cachedTokensFor({ tokens: { cache_read_input_tokens: 80 } })).toBe(80)
  })

  it('renders missing cache usage as zero', () => {
    expect(fmt(cachedTokensFor({}))).toBe('0')
    expect(fmt(cachedTokensFor({ tokens: { cached_tokens: 0, cache_read_input_tokens: 25 } }))).toBe('0')
  })
})

describe('latency classification', () => {
  it('buckets TTFT into fast/mid/slow bands', () => {
    expect(latencyClass(250)).toBe('fast')
    expect(latencyClass(499)).toBe('fast')
    expect(latencyClass(500)).toBe('mid')
    expect(latencyClass(1000)).toBe('mid')
    expect(latencyClass(1499)).toBe('mid')
    expect(latencyClass(1500)).toBe('slow')
    expect(latencyClass(5000)).toBe('slow')
  })

  it('treats a missing or zero TTFT as fast', () => {
    expect(latencyClass(undefined)).toBe('fast')
    expect(latencyClass(0)).toBe('fast')
    expect(latencyClass(-1)).toBe('fast')
  })

  it('maps bands to tailwind text colors', () => {
    expect(latencyTextClass(250)).toBe('text-emerald-500')
    expect(latencyTextClass(750)).toBe('text-amber-500')
    expect(latencyTextClass(2000)).toBe('text-rose-500')
  })

  it('maps bands to badge tones', () => {
    expect(latencyBadgeTone(250)).toBe('success')
    expect(latencyBadgeTone(750)).toBe('warning')
    expect(latencyBadgeTone(2000)).toBe('danger')
  })
})

describe('ttftRatio', () => {
  it('computes the TTFT share of total latency', () => {
    expect(ttftRatio(250, 1000)).toBe(0.25)
    expect(ttftRatio(1000, 1000)).toBe(1)
  })

  it('clamps to [0, 1]', () => {
    expect(ttftRatio(2000, 1000)).toBe(1)
    expect(ttftRatio(-5, 1000)).toBe(0)
  })

  it('returns 0 when either value is missing', () => {
    expect(ttftRatio(undefined, 1000)).toBe(0)
    expect(ttftRatio(250, undefined)).toBe(0)
    expect(ttftRatio(0, 0)).toBe(0)
  })
})

describe('providerDisplayName', () => {
  it('maps a custom compatible node id to its configured name', () => {
    const nodes = [{ id: 'openai-compatible-chat-abc', name: 'My vLLM' }]

    expect(providerDisplayName('openai-compatible-chat-abc', nodes)).toBe('My vLLM')
  })

  it('resolves catalog providers by id', () => {
    expect(providerDisplayName('deepseek')).toBe('DeepSeek')
  })

  it('passes an unknown id through unchanged', () => {
    expect(providerDisplayName('some-unknown-provider')).toBe('some-unknown-provider')
  })

  it('renders missing provider as unknown', () => {
    expect(providerDisplayName(undefined)).toBe('unknown')
    expect(providerDisplayName('')).toBe('unknown')
  })
})

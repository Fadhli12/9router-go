<script lang="ts">
  import {
    formatResetTime,
    formatResetTimeDisplay,
    getRemainingPercentage,
    type NormalizedQuota,
  } from './types'

  interface Props {
    quotas: NormalizedQuota[]
  }

  let { quotas = [] }: Props = $props()

  function colorFor(pct: number) {
    if (pct > 50) {
      return {
        bar: 'bg-emerald-500',
        text: 'text-emerald-600 dark:text-emerald-400',
        badge: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/20',
      }
    }
    if (pct >= 20) {
      return {
        bar: 'bg-amber-500',
        text: 'text-amber-600 dark:text-amber-400',
        badge: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/20',
      }
    }
    return {
      bar: 'bg-red-500',
      text: 'text-red-600 dark:text-red-400',
      badge: 'bg-red-500/10 text-red-600 dark:text-red-400 border-red-500/20',
    }
  }

  let geminiSession = $derived(quotas.find((q) => q.modelKey === 'gemini_session'))
  let geminiWeekly = $derived(quotas.find((q) => q.modelKey === 'gemini_weekly'))
  let claudeSession = $derived(quotas.find((q) => q.modelKey === 'claude_gpt_session'))
  let claudeWeekly = $derived(quotas.find((q) => q.modelKey === 'claude_gpt_weekly'))

  let canonicalCards = $derived([
    {
      title: 'Gemini Models',
      windowLabel: '5-Hour Window',
      quota: geminiSession,
    },
    {
      title: 'Gemini Models',
      windowLabel: 'Weekly Window',
      quota: geminiWeekly,
    },
    {
      title: 'Claude & GPT Models',
      windowLabel: '5-Hour Window',
      quota: claudeSession,
    },
    {
      title: 'Claude & GPT Models',
      windowLabel: 'Weekly Window',
      quota: claudeWeekly,
    },
  ].filter((c) => Boolean(c.quota)))

  let extraQuotas = $derived(
    quotas.filter(
      (q) => !['gemini_session', 'gemini_weekly', 'claude_gpt_session', 'claude_gpt_weekly'].includes(q.modelKey || '')
    )
  )
</script>

<div class="grid grid-cols-1 sm:grid-cols-2 gap-2.5 my-1">
  {#each canonicalCards as card (card.title + card.windowLabel)}
    {@const q = card.quota!}
    {@const pct = getRemainingPercentage(q)}
    {@const col = colorFor(pct)}
    {@const countdown = formatResetTime(q.resetAt)}
    {@const resetAbsolute = formatResetTimeDisplay(q.resetAt)}
    <div class="rounded-lg border border-border bg-surface-2/60 p-2.5 flex flex-col justify-between gap-2 shadow-xs">
      <div class="flex items-center justify-between gap-1">
        <span class="text-xs font-semibold text-text-main">{card.title}</span>
        <span class="text-[10px] font-medium px-1.5 py-0.5 rounded border {col.badge}">
          {card.windowLabel}
        </span>
      </div>

      <div class="w-full bg-border-subtle/60 rounded-full h-1.5 overflow-hidden">
        <div class="h-full rounded-full transition-all duration-300 {col.bar}" style="width: {pct}%"></div>
      </div>

      <div class="flex items-center justify-between text-[11px] text-text-muted">
        <span class="font-medium {col.text}">Remaining: {pct}%</span>
        {#if countdown !== '-'}
          <span class="text-[10px]" title={resetAbsolute ? `Resets at ${resetAbsolute}` : ''}>
            Resets in {countdown}
          </span>
        {/if}
      </div>
    </div>
  {/each}
</div>

{#if extraQuotas.length > 0}
  <div class="mt-2 pt-2 border-t border-border-subtle/60">
    <span class="text-[10px] font-medium text-text-muted uppercase tracking-wider block mb-1">Additional Quotas</span>
    <div class="space-y-1">
      {#each extraQuotas as q (q.modelKey || q.name)}
        {@const pct = getRemainingPercentage(q)}
        {@const col = colorFor(pct)}
        <div class="flex items-center justify-between text-xs py-1 px-1.5 rounded hover:bg-surface-2">
          <span class="text-text-main font-medium">{q.name}</span>
          <span class="text-[11px] font-mono {col.text}">{pct}%</span>
        </div>
      {/each}
    </div>
  </div>
{/if}

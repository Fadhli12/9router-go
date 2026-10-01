<script lang="ts">
  import Button from '../../lib/ui/Button.svelte'

  interface Props {
    onCreateClick: () => void
    selectedCount?: number
    deletableCount?: number
    onDeleteSelected?: () => void
    onDeleteAll?: () => void
  }

  let {
    onCreateClick,
    selectedCount = 0,
    deletableCount = 0,
    onDeleteSelected,
    onDeleteAll,
  }: Props = $props()
</script>

<div class="flex flex-col gap-4">
  <div class="min-w-0">
    <p class="text-sm text-text-muted">
      Group models under one name, then pick a strategy per combo:
    </p>
    <ul class="text-sm text-text-muted mt-2 flex flex-col gap-1">
      <li>
        <span class="font-medium text-text-main">Fallback</span> — tries models in order (next on failure)
      </li>
      <li>
        <span class="font-medium text-text-main">Round Robin</span> — rotates models across requests to spread load
      </li>
      <li>
        <span class="font-medium text-text-main">Fusion</span> — queries all models in parallel, then a judge
        synthesizes one answer. Best quality, but costs the most: every request bills all panel models + the judge
        (N+1 calls)
      </li>
    </ul>
  </div>

  <!-- One wrapping toolbar instead of a five-row column. Create stays primary and
       always first; the destructive pair is pushed to the far end so Delete All
       (which wipes every combo) can never sit next to a plain selection control. -->
  <div
    class="flex flex-wrap items-center gap-2 border-t border-border pt-4"
    role="group"
    aria-label="Combo actions"
  >
    <Button icon="add" size="sm" onclick={onCreateClick} class="whitespace-nowrap">
      Create Combo
    </Button>
    {#if onDeleteSelected}
      <span class="flex-1"></span>
      <Button
        icon="delete"
        size="sm"
        onclick={onDeleteSelected}
        disabled={selectedCount === 0}
        variant="ghost"
        title={selectedCount === 0
          ? 'Select at least one combo first'
          : `Delete ${selectedCount} selected combo(s)`}
        class="whitespace-nowrap"
      >
        {selectedCount > 0 ? `Delete Selected (${selectedCount})` : 'Delete Selected'}
      </Button>
    {/if}
    {#if onDeleteAll}
      <Button
        icon="delete_forever"
        size="sm"
        onclick={onDeleteAll}
        disabled={deletableCount === 0}
        variant="danger"
        title={`Delete all ${deletableCount} deletable combo(s)`}
        class="whitespace-nowrap"
      >
        Delete All ({deletableCount})
      </Button>
    {/if}
  </div>
</div>

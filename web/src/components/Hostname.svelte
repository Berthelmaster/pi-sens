<script lang="ts">
  // Header slot for the Pi's hostname. The system info task fills it from the
  // live snapshot; until then it shows a placeholder.
  let hostname = $state<string | null>(null)
</script>

<span class="hostname" title="Hostname">{hostname ?? '–'}</span>

<style>
  .hostname {
    font-family: var(--font-mono);
    font-size: 0.875rem;
    color: var(--color-text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>

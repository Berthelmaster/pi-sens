<script lang="ts">
  // Header slot for the Pi's hostname, from the sysinfo section of the live
  // snapshot. Shows a placeholder until it is known.
  import { PLACEHOLDER } from '../lib/format'
  import { snapshot } from '../lib/live'
  import { hostnameOf } from '../types/sysinfo'

  let hostname = $derived(hostnameOf($snapshot))
</script>

<span class="hostname" title="Hostname">{hostname ?? PLACEHOLDER}</span>

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

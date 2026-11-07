<script lang="ts">
  import { getHealth, type Health } from './api'

  let health = $state<Health | null>(null)
  let error = $state<string | null>(null)

  $effect(() => {
    const ctrl = new AbortController()
    getHealth(ctrl.signal)
      .then((h) => (health = h))
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return
        error = err instanceof Error ? err.message : String(err)
      })
    return () => ctrl.abort()
  })
</script>

<main>
  <h1>pisens</h1>
  {#if error}
    <p class="error">Backend unreachable: {error}</p>
  {:else if health}
    <p>
      Status <strong>{health.status}</strong>, version
      <code>{health.version}</code>
    </p>
  {:else}
    <p>Connecting…</p>
  {/if}
</main>

<style>
  main {
    max-width: 40rem;
    margin: 4rem auto;
    padding: 0 1rem;
  }
  .error {
    color: #c51a4a;
  }
</style>

<script lang="ts">
  // "A new version is ready" toast, shown once a deploy is noticed (see
  // lib/update.ts). It sits at the bottom of the screen so it never covers
  // the header or the alert banner under it, and it does not take focus.
  import {
    dismissUpdate,
    reloadToUpdate,
    updatePrompt,
  } from '../lib/updatePrompt.svelte'

  let reloading = $state(false)

  function reload() {
    reloading = true
    reloadToUpdate()
  }
</script>

<!--
  The live region is always there, so screen readers announce the toast when
  it appears inside it.
-->
<div class="region" role="status">
  {#if updatePrompt.ready}
    <div class="toast">
      <p>A new version of pisens is ready</p>
      <button
        type="button"
        class="primary"
        onclick={reload}
        disabled={reloading}
      >
        {reloading ? 'Reloading…' : 'Reload'}
      </button>
      <button
        type="button"
        class="dismiss"
        aria-label="Dismiss"
        title="Dismiss"
        onclick={dismissUpdate}
      >
        <span aria-hidden="true">×</span>
      </button>
    </div>
  {/if}
</div>

<style>
  .region {
    position: fixed;
    right: calc(var(--space-4) + var(--safe-right));
    bottom: calc(var(--space-4) + var(--safe-bottom));
    left: calc(var(--space-4) + var(--safe-left));
    z-index: 20;
    display: flex;
    justify-content: center;
    /* Only the toast itself takes clicks, not the empty strip around it. */
    pointer-events: none;
  }

  .toast {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    max-width: 100%;
    padding: var(--space-2) var(--space-2) var(--space-2) var(--space-4);
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    background: var(--color-surface-raised);
    box-shadow: var(--shadow);
    pointer-events: auto;
  }

  p {
    margin: 0;
    font-size: 0.875rem;
  }

  button {
    padding: var(--space-1) var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    background: var(--color-surface);
    color: var(--color-text);
    font: inherit;
    font-size: 0.8125rem;
    cursor: pointer;
  }

  button:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }

  .primary {
    border-color: var(--color-accent);
    background: var(--color-accent);
    color: var(--color-accent-contrast);
    font-weight: 600;
  }

  .primary:disabled {
    cursor: progress;
    opacity: 0.7;
  }

  .dismiss {
    border-color: transparent;
    background: transparent;
    color: var(--color-text-muted);
    font-size: 1rem;
    line-height: 1;
  }

  .dismiss:hover {
    color: var(--color-text);
  }

  @media (min-width: 640px) {
    .region {
      justify-content: flex-end;
    }
  }
</style>

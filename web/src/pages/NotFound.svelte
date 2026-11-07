<script lang="ts">
  import Card from '../components/Card.svelte'
  import { followLink, router } from '../lib/router.svelte'
</script>

<h1 class="visually-hidden">Page not found</h1>

<Card title="Not found">
  <p>
    There is nothing at <code>{router.path}</code>.
    <a href="/" onclick={followLink}>Back to the overview</a>
  </p>
</Card>

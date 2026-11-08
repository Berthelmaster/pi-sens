<script lang="ts">
  // Shown instead of a page when the service worker served the cached shell
  // because the Pi could not be reached. App.svelte swaps it for the
  // dashboard as soon as the live stream connects.
  import Card from '../components/Card.svelte'
</script>

<h1 class="visually-hidden">Offline</h1>

<Card title="Offline" status="unknown">
  <p role="status">
    Can't reach pisens. It will reconnect when the Pi is back.
  </p>
</Card>

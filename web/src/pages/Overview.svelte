<script lang="ts">
  import CpuWidget from '../widgets/CpuWidget.svelte'
  import MemoryWidget from '../widgets/MemoryWidget.svelte'
  import NetworkWidget from '../widgets/NetworkWidget.svelte'
  import PiHealthWidget from '../widgets/PiHealthWidget.svelte'
  import ProcessesWidget from '../widgets/ProcessesWidget.svelte'
  import ServicesWidget from '../widgets/ServicesWidget.svelte'
  import StorageWidget from '../widgets/StorageWidget.svelte'
  import SystemInfoWidget from '../widgets/SystemInfoWidget.svelte'
  import TemperatureWidget from '../widgets/TemperatureWidget.svelte'
</script>

<h1 class="visually-hidden">Overview</h1>

<div class="grid">
  <SystemInfoWidget />
  <CpuWidget />
  <MemoryWidget />
  <TemperatureWidget />
  <StorageWidget />
  <NetworkWidget />
  <PiHealthWidget />
  <ServicesWidget />
  <div class="full">
    <ProcessesWidget />
  </div>
</div>

<style>
  /* 1 column on phones, 2 on tablets, 3 on laptops, 4 on wide screens. */
  .grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-4);
  }

  @media (min-width: 640px) {
    .grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (min-width: 1024px) {
    .grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }

  @media (min-width: 1600px) {
    .grid {
      grid-template-columns: repeat(4, minmax(0, 1fr));
    }
  }

  .full {
    grid-column: 1 / -1;
  }
</style>

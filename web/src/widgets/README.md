# Widgets

Each card on the Overview page is one component in this folder. The layout shell (`App.svelte`, `pages/Overview.svelte`, `components/Card.svelte`) is shared; the widgets are not.

## Rules

- A widget owns its own file, e.g. `CpuWidget.svelte`. Work on one widget only touches that file, so several widgets can be built in parallel without merge conflicts.
- A widget keeps its TypeScript types in its own file under `web/src/types/<area>.ts`, e.g. `types/cpu.ts` for the `cpu` collector's section of the snapshot. Do not add widget types to a shared file.
- Render the widget inside `Card` from `../components/Card.svelte`. Pass `status` (`ok`, `warning`, `critical` or `unknown`) to colour the card when the widget has a health state.
- For charts and meters use the shared components: `Sparkline`, `TimeSeriesChart` and `Gauge` in `../components/` (`gaugeStatus` from `Gauge.svelte` gives the matching Card `status`). Run `npm run dev` and open `/dev/charts` to see them with demo data.
- Use the CSS variables from `web/src/styles/theme.css` for colours so the light and dark themes both work. Component styles stay in the component's `<style>` block.
- Only edit `pages/Overview.svelte` to add or move a widget, not to build one.

## Live data

`web/src/lib/live.ts` holds one shared connection to `/api/stream`. Do not open your own.

```svelte
<script lang="ts">
  import { formatPercent } from '../lib/format'
  import { seriesHistory, snapshot } from '../lib/live'
  import { isCollectorError } from '../types/snapshot'
  import type { CpuSection } from '../types/cpu'

  let cpu = $derived($snapshot?.collectors.cpu as CpuSection | undefined)
  // [unix seconds[], values[]] for the last 5 minutes, ready for uPlot.
  const usage = seriesHistory('cpu.usage_pct')
</script>

{formatPercent($snapshot?.series['cpu.usage_pct'])}
```

- `snapshot` is the latest snapshot, `null` until the first one arrives. A failed collector's section is `{ "error": "..." }`; check it with `isCollectorError`.
- `alerts` is the latest set of firing and resolved alerts (`types/alerts.ts`), `null` until known or when the backend has alerts turned off.
- `seriesHistory(key)` keeps the last five minutes of one series key. Samples without the key are left out.
- Format values with the helpers in `web/src/lib/format.ts` (`formatBytes`, `formatBitsPerSecond`, `formatPercent`, `formatDuration`, `formatTemp`, `formatHz`). They show `–` for a missing value, so series values can go straight in.

## Widgets

| File                       | Area        | Types file             |
| -------------------------- | ----------- | ---------------------- |
| `SystemInfoWidget.svelte`  | System info | `types/sysinfo.ts`     |
| `CpuWidget.svelte`         | CPU         | `types/cpu.ts`         |
| `MemoryWidget.svelte`      | Memory      | `types/memory.ts`      |
| `TemperatureWidget.svelte` | Temperature | `types/temperature.ts` |
| `StorageWidget.svelte`     | Storage     | `types/storage.ts`     |
| `NetworkWidget.svelte`     | Network     | `types/network.ts`     |
| `PiHealthWidget.svelte`    | Pi health   | `types/pihealth.ts`    |
| `ProcessesWidget.svelte`   | Processes   | `types/processes.ts`   |

`ProcessesWidget` spans the full width of the grid; the others take one column each.

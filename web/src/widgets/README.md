# Widgets

Each card on the Overview page is one component in this folder. The layout shell (`App.svelte`, `pages/Overview.svelte`, `components/Card.svelte`) is shared; the widgets are not.

## Rules

- A widget owns its own file, e.g. `CpuWidget.svelte`. Work on one widget only touches that file, so several widgets can be built in parallel without merge conflicts.
- A widget keeps its TypeScript types in its own file under `web/src/types/<area>.ts`, e.g. `types/cpu.ts` for the `cpu` collector's section of the snapshot. Do not add widget types to a shared file.
- Render the widget inside `Card` from `../components/Card.svelte`. Pass `status` (`ok`, `warning`, `critical` or `unknown`) to colour the card when the widget has a health state.
- For charts and meters use the shared components: `Sparkline`, `TimeSeriesChart` and `Gauge` in `../components/` (`gaugeStatus` from `Gauge.svelte` gives the matching Card `status`). Run `npm run dev` and open `/dev/charts` to see them with demo data.
- Use the CSS variables from `web/src/styles/theme.css` for colours so the light and dark themes both work. Component styles stay in the component's `<style>` block.
- Only edit `pages/Overview.svelte` to add or move a widget, not to build one.
- `npm run dev` without a backend has no live data. `/dev/energy` shows `EnergyWidget` and `/dev/netquality` shows `NetQualityWidget` with mock snapshots in each of their states, passed in through their `snap` and `history` props.

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
- Read untyped number fields from collector sections with `num(v)` from `web/src/lib/format.ts`. It returns the value when it is a finite number and `undefined` otherwise, so don't write your own copy.

## Component tests

`npm test` runs two Vitest projects: `*.test.ts` files in node for plain logic, and `*.svelte.test.ts` files in jsdom for components. Name a component test `<Component>.svelte.test.ts` next to the component and mount it with `@testing-library/svelte`; the DOM is cleaned up after each test. `components/AlertBanner.svelte.test.ts` is a full example.

- Mock `../lib/live` with plain writable stores so the test sets the data itself and no stream is opened. `vi.mock` is hoisted above the imports, so create the stores inside the factory and import them back from the mocked module:

  ```ts
  import { snapshot } from '../lib/live'

  vi.mock('../lib/live', async () => {
    const { writable } = await import('svelte/store')
    return { snapshot: writable(null) }
  })

  const live = snapshot as Writable<Snapshot | null>
  ```

- After changing a store, `await tick()` (from `svelte`) before checking the DOM. `await fireEvent.click(button)` waits for the update itself.
- Find elements by role and accessible name (`screen.getByRole('button', { name: 'Recent alerts' })`), which also checks the markup is accessible.
- Shared `$state` modules such as `lib/alertPanel.svelte.ts` keep their values between tests; reset them in `beforeEach`.

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
| `EnergyWidget.svelte`      | Energy      | `types/energy.ts`      |
| `NetQualityWidget.svelte`  | Internet    | `types/netquality.ts`  |
| `ServicesWidget.svelte`    | Services    | `types/services.ts`    |
| `ProcessesWidget.svelte`   | Processes   | `types/processes.ts`   |

`ProcessesWidget` spans the full width of the grid; the others take one column each. `ServicesWidget` renders nothing when the backend reports services as unavailable (no systemd). `EnergyWidget` shows a short "needs a Raspberry Pi 5" note when the backend has no PMIC readings. `NetQualityWidget` says so when the probes are turned off (`PISENS_NETQ=off`).

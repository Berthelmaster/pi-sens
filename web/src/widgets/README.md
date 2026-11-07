# Widgets

Each card on the Overview page is one component in this folder. The layout shell (`App.svelte`, `pages/Overview.svelte`, `components/Card.svelte`) is shared; the widgets are not.

## Rules

- A widget owns its own file, e.g. `CpuWidget.svelte`. Work on one widget only touches that file, so several widgets can be built in parallel without merge conflicts.
- A widget keeps its TypeScript types in its own file under `web/src/types/<area>.ts`, e.g. `types/cpu.ts` for the `cpu` collector's section of the snapshot. Do not add widget types to a shared file.
- Render the widget inside `Card` from `../components/Card.svelte`. Pass `status` (`ok`, `warning`, `critical` or `unknown`) to colour the card when the widget has a health state.
- Use the CSS variables from `web/src/styles/theme.css` for colours so the light and dark themes both work. Component styles stay in the component's `<style>` block.
- Only edit `pages/Overview.svelte` to add or move a widget, not to build one.

## Widgets

| File                       | Area        | Types file             |
| -------------------------- | ----------- | ---------------------- |
| `SystemInfoWidget.svelte`  | System info | `types/system.ts`      |
| `CpuWidget.svelte`         | CPU         | `types/cpu.ts`         |
| `MemoryWidget.svelte`      | Memory      | `types/memory.ts`      |
| `TemperatureWidget.svelte` | Temperature | `types/temperature.ts` |
| `StorageWidget.svelte`     | Storage     | `types/storage.ts`     |
| `NetworkWidget.svelte`     | Network     | `types/network.ts`     |
| `PiHealthWidget.svelte`    | Pi health   | `types/pihealth.ts`    |
| `ProcessesWidget.svelte`   | Processes   | `types/processes.ts`   |

`ProcessesWidget` spans the full width of the grid; the others take one column each.

<script lang="ts" module>
  import { formatBytes, PLACEHOLDER } from '../lib/format'
  import type { BoardInfo } from '../types/sysinfo'

  /**
   * Formats an uptime with every unit from the largest non-zero one down to
   * seconds, e.g. 334893 -> "3d 21h 01m 33s", so it visibly ticks each second.
   * formatDuration stops at two units, which would hide that.
   */
  export function formatUptime(seconds: number | null | undefined): string {
    if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds < 0)
      return PLACEHOLDER
    let rest = Math.floor(seconds)
    const parts: string[] = []
    for (const [unit, size] of [
      ['d', 86400],
      ['h', 3600],
      ['m', 60],
      ['s', 1],
    ] as const) {
      const n = Math.floor(rest / size)
      rest -= n * size
      if (parts.length > 0) parts.push(`${String(n).padStart(2, '0')}${unit}`)
      else if (n > 0 || unit === 's') parts.push(`${n}${unit}`)
    }
    return parts.join(' ')
  }

  /** Formats Unix milliseconds as local "YYYY-MM-DD HH:MM". */
  export function formatDateTime(ms: number): string {
    if (!Number.isFinite(ms)) return PLACEHOLDER
    const d = new Date(ms)
    const p = (n: number) => String(n).padStart(2, '0')
    return (
      `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ` +
      `${p(d.getHours())}:${p(d.getMinutes())}`
    )
  }

  /**
   * The fitted RAM, SoC and manufacturer decoded from the revision code, e.g.
   * "8 GiB · BCM2712 · Sony UK", or undefined when there are none.
   */
  export function boardDetails(board: BoardInfo): string | undefined {
    const ram = num(board.fitted_ram_bytes)
    const parts = [
      ram !== undefined && ram > 0
        ? formatBytes(ram, { decimals: 0 })
        : undefined,
      text(board.processor),
      text(board.manufacturer),
    ].filter((p) => p !== undefined)
    return parts.length > 0 ? parts.join(' · ') : undefined
  }

  /** A non-empty string field, or undefined. */
  function text(v: unknown): string | undefined {
    return typeof v === 'string' && v.trim() !== '' ? v.trim() : undefined
  }

  /** A finite number field, or undefined. */
  function num(v: unknown): number | undefined {
    return typeof v === 'number' && Number.isFinite(v) ? v : undefined
  }
</script>

<script lang="ts">
  import Card from '../components/Card.svelte'
  import { connection, snapshot } from '../lib/live'
  import { isCollectorError, parseTimestamp } from '../types/snapshot'
  import { sysinfoSection, type SysinfoSection } from '../types/sysinfo'

  let section = $derived(sysinfoSection($snapshot))
  let error = $derived(isCollectorError(section) ? section.error : null)
  let sys = $derived<SysinfoSection>(
    section && !isCollectorError(section) ? section : {},
  )

  // uptime_s is as of the snapshot; count on from when it arrived so the
  // uptime ticks between samples. The client clock is only used for the
  // elapsed time, so it does not matter if it is off from the Pi's.
  let now = $state(Date.now())
  $effect(() => {
    const timer = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(timer)
  })

  let uptime = $derived.by(() => {
    const base = num(sys.uptime_s)
    if (base === undefined) return undefined
    const since = $connection.lastUpdate
    return base + (since === null ? 0 : Math.max(0, (now - since) / 1000))
  })

  // Go's zero time (year 1) would mean the backend never set it.
  let bootMs = $derived.by(() => {
    const t = text(sys.boot_time)
      ? parseTimestamp(sys.boot_time as string)
      : NaN
    return t > 0 ? t : NaN
  })
  let procs = $derived(num(sys.procs))
  let threads = $derived(num(sys.threads))
  let board = $derived<BoardInfo>(
    typeof sys.board === 'object' && sys.board !== null ? sys.board : {},
  )
  let details = $derived(boardDetails(board))
</script>

<Card title="System" status={error ? 'unknown' : undefined}>
  {#if error}
    <p class="muted">System info unavailable: {error}</p>
  {:else}
    {#if $snapshot && !section}
      <p class="muted note">The latest snapshot has no system info.</p>
    {/if}
    <dl>
      <dt>Board</dt>
      <dd>
        {text(board.model) ?? PLACEHOLDER}
        {#if details}
          <span class="muted">· {details}</span>
        {/if}
      </dd>

      <dt>OS</dt>
      <dd>{text(sys.os) ?? PLACEHOLDER}</dd>

      <dt>Kernel</dt>
      <dd class="mono">{text(sys.kernel) ?? PLACEHOLDER}</dd>

      <dt>Arch</dt>
      <dd class="mono">{text(sys.arch) ?? PLACEHOLDER}</dd>

      <dt>Uptime</dt>
      <dd class="num">{formatUptime(uptime)}</dd>

      <dt>Booted</dt>
      <dd
        class="num"
        title={Number.isFinite(bootMs)
          ? new Date(bootMs).toISOString()
          : undefined}
      >
        {formatDateTime(bootMs)}
      </dd>

      <dt>Processes</dt>
      <dd class="num">
        {procs ?? PLACEHOLDER}
        {#if threads !== undefined}
          <span class="muted">· {threads} threads</span>
        {/if}
      </dd>
    </dl>
  {/if}
</Card>

<style>
  dl {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: var(--space-1) var(--space-4);
    margin: 0;
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.875rem;
    line-height: 1.5rem;
  }

  dd {
    margin: 0;
    line-height: 1.5rem;
    overflow-wrap: anywhere;
  }

  .mono {
    font-family: var(--font-mono);
    font-size: 0.875rem;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }

  .note {
    margin: 0 0 var(--space-2);
    font-size: 0.875rem;
  }
</style>

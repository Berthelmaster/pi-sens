<script lang="ts">
  import Card, { type CardStatus } from '../components/Card.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import { formatHz, num } from '../lib/format'
  import { seriesHistory, snapshot } from '../lib/live'
  import {
    EXT5V_WARN_V,
    formatVolts,
    formatWatts,
    piHealthStatus,
    POWER_SERIES,
    powerSection,
    psuSummary,
    RTC_BATTERY_WARN_V,
    rtcBattery,
    rtcSection,
    supplyLow,
    throttleBadges,
    throttleSection,
    undervoltageSummary,
    type PowerSection,
    type ThrottleSection,
  } from '../types/pihealth'
  import { isCollectorError } from '../types/snapshot'

  const powerHistory = seriesHistory(POWER_SERIES)

  let throttleRaw = $derived(throttleSection($snapshot))
  let throttleError = $derived(
    isCollectorError(throttleRaw) ? throttleRaw.error : null,
  )
  let throttle = $derived<Partial<ThrottleSection> | undefined>(
    throttleRaw && !isCollectorError(throttleRaw) ? throttleRaw : undefined,
  )

  let powerRaw = $derived(powerSection($snapshot))
  let powerError = $derived(isCollectorError(powerRaw) ? powerRaw.error : null)
  let power = $derived<Partial<PowerSection> | undefined>(
    powerRaw && !isCollectorError(powerRaw) ? powerRaw : undefined,
  )

  // Both collectors go through vcgencmd and give up on it together.
  let unavailable = $derived(
    throttle?.available === false || power?.available === false,
  )

  let flags = $derived(throttle?.available ? throttle.flags : undefined)
  let badges = $derived(throttleBadges(flags))
  // Only a Pi 5 reports the supply rating.
  let psu = $derived(throttle?.available ? psuSummary(throttle) : undefined)
  let undervoltage = $derived(
    throttle?.available ? undervoltageSummary(throttle) : undefined,
  )

  // Read from sysfs rather than vcgencmd; only there with a battery fitted.
  let rtc = $derived(rtcBattery(rtcSection($snapshot)))

  let volts = $derived(power?.available ? power.volts : undefined)
  let clocks = $derived(power?.available ? power.clocks_mhz : undefined)
  // Only a Pi 5 has the PMIC that reports the supply and the power draw.
  let pmic = $derived(power?.available ? power.pmic : undefined)
  let ext5v = $derived(num(pmic?.ext5v_v))
  let watts = $derived(num(pmic?.total_w))
  let lowSupply = $derived(supplyLow(ext5v))

  // Keep the card neutral until the first snapshot arrives.
  let status = $derived<CardStatus | undefined>(
    !$snapshot
      ? undefined
      : unavailable
        ? 'unknown'
        : piHealthStatus(flags, ext5v, rtc?.volts),
  )

  function mhzToHz(mhz: unknown): number | undefined {
    const v = num(mhz)
    return v === undefined ? undefined : v * 1e6
  }
</script>

{#snippet rtcRow()}
  {#if rtc}
    <div>
      <dt title="Backup battery that keeps the clock running without power">
        RTC battery:
      </dt>
      <dd
        class:low={rtc.low}
        title={rtc.low
          ? `Below ${RTC_BATTERY_WARN_V} V, the clock may be wrong after a power cut until NTP syncs`
          : undefined}
      >
        <span class="num">{formatVolts(rtc.volts)}</span>{#if rtc.charging},
          <span
            class="muted"
            title={rtc.chargingVolts !== undefined
              ? `Trickle charging to ${formatVolts(rtc.chargingVolts)}`
              : 'Trickle charging'}>charging</span
          >{/if}
      </dd>
    </div>
  {/if}
{/snippet}

<Card title="Pi health" {status}>
  {#if !$snapshot}
    <p class="muted">Waiting for data…</p>
  {:else if unavailable}
    <p class="muted">
      vcgencmd unavailable, add the service user to the video group
    </p>
    {#if rtc}
      <dl class="supply">{@render rtcRow()}</dl>
    {/if}
  {:else}
    <h3>Throttling</h3>
    {#if throttleError}
      <p class="muted note">Throttle flags unavailable: {throttleError}</p>
    {:else if !flags}
      <p class="muted note">The latest snapshot has no throttle flags.</p>
    {:else}
      <ul
        class="badges"
        title={throttle?.raw && `get_throttled ${throttle.raw}`}
      >
        {#each badges as b (b.key)}
          <li
            class="badge"
            data-state={b.state}
            title={b.state === 'active'
              ? `${b.description} right now`
              : `${b.description} at some point since boot`}
          >
            {b.label}{#if b.state === 'occurred'}<span class="since">
                since boot</span
              >{/if}
          </li>
        {:else}
          <li class="badge" data-state="ok">All good</li>
        {/each}
      </ul>
    {/if}
    {#if psu || undervoltage || rtc}
      <dl class="supply">
        {#if psu}
          <div>
            <dt>Power supply:</dt>
            <dd class:low={!psu.ok}>
              {psu.rating}
              {#if psu.ok}<span
                  class="ok"
                  title="Enough for a Pi 5 with USB devices at full current"
                  >✓</span
                >{:else}– {psu.note}{/if}
            </dd>
          </div>
        {/if}
        {#if undervoltage}
          <div>
            <dt title="Times the supply voltage dropped too low">
              Under-voltage:
            </dt>
            <dd>{undervoltage}</dd>
          </div>
        {/if}
        {@render rtcRow()}
      </dl>
    {/if}

    <h3>Voltages and clocks</h3>
    {#if powerError}
      <p class="muted note">Voltages and clocks unavailable: {powerError}</p>
    {:else if !volts && !clocks}
      <p class="muted note">The latest snapshot has no voltages or clocks.</p>
    {:else}
      <dl class="stats">
        {#if ext5v !== undefined}
          <div>
            <dt title="5 V input supply, measured by the PMIC">
              Supply (EXT5V)
            </dt>
            <dd class="num" class:low={lowSupply}>{formatVolts(ext5v)}</dd>
            {#if lowSupply}
              <dd class="warn">
                Below {EXT5V_WARN_V} V, check the power supply
              </dd>
            {/if}
          </div>
        {/if}
        <div>
          <dt>Core voltage</dt>
          <dd class="num">{formatVolts(num(volts?.core))}</dd>
        </div>
        <div>
          <dt>ARM clock</dt>
          <dd class="num">{formatHz(mhzToHz(clocks?.arm))}</dd>
        </div>
        <div>
          <dt>Core clock</dt>
          <dd class="num">{formatHz(mhzToHz(clocks?.core))}</dd>
        </div>
        <div>
          <dt>GPU clock</dt>
          <dd class="num">{formatHz(mhzToHz(clocks?.v3d))}</dd>
        </div>
      </dl>
    {/if}

    {#if watts !== undefined}
      <h3>Power</h3>
      <div class="power">
        <div class="reading">
          <span class="watts">{formatWatts(watts)}</span>
          <span
            class="muted hint"
            title="Sum of the PMIC rails; USB devices and PMIC losses are not included"
          >
            estimated
          </span>
        </div>
        <div class="chart">
          <Sparkline
            data={$powerHistory}
            min={0}
            label="Estimated power draw, last 5 minutes"
            height={32}
          />
        </div>
      </div>
    {/if}
  {/if}
</Card>

<style>
  h3 {
    margin: var(--space-4) 0 var(--space-2);
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--color-text-muted);
  }

  h3:first-child {
    margin-top: 0;
  }

  .note {
    margin: 0;
    font-size: 0.875rem;
    overflow-wrap: anywhere;
  }

  .badges {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .badge {
    --badge-color: var(--color-unknown);

    display: inline-flex;
    align-items: baseline;
    gap: var(--space-1);
    padding: 0.125rem var(--space-2);
    border: 1px solid var(--badge-color);
    border-radius: 999px;
    color: var(--badge-color);
    background: color-mix(in srgb, var(--badge-color) 12%, transparent);
    font-size: 0.8125rem;
    font-weight: 600;
    line-height: 1.25rem;
  }

  .badge[data-state='active'] {
    --badge-color: var(--color-critical);
  }
  .badge[data-state='occurred'] {
    --badge-color: var(--color-warning);
  }
  .badge[data-state='ok'] {
    --badge-color: var(--color-ok);
  }

  .since {
    font-weight: 400;
    font-size: 0.75rem;
  }

  .supply {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    margin: var(--space-3) 0 0;
    font-size: 0.875rem;
  }

  .supply div {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
  }

  .supply dt {
    font-size: inherit;
  }

  .ok {
    color: var(--color-ok);
    font-weight: 600;
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(7.5rem, 1fr));
    gap: var(--space-3) var(--space-4);
    margin: 0;
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.75rem;
  }

  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }

  .low {
    color: var(--color-warning);
    font-weight: 600;
  }

  .warn {
    color: var(--color-warning);
    font-size: 0.75rem;
  }

  .power {
    display: flex;
    align-items: center;
    gap: var(--space-4);
  }

  .reading {
    display: flex;
    flex-direction: column;
    flex-shrink: 0;
  }

  .watts {
    font-size: 1.5rem;
    font-weight: 600;
    line-height: 1.2;
    font-variant-numeric: tabular-nums;
  }

  .hint {
    font-size: 0.75rem;
  }

  .chart {
    flex: 1;
    min-width: 0;
  }
</style>

<script lang="ts">
  import Card, { type CardStatus } from '../components/Card.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import { formatPercent, formatTemp, PLACEHOLDER } from '../lib/format'
  import { seriesHistory, snapshot } from '../lib/live'
  import {
    FAN_PWM_SERIES,
    FAN_RPM_SERIES,
    fanSection,
    type FanSection,
  } from '../types/fan'
  import { isCollectorError } from '../types/snapshot'
  import {
    PI_THROTTLE_C,
    pickCpuSensor,
    sensorDisplayName,
    temperatureSection,
    temperatureSeriesKey,
    tempStatus,
    type TemperatureSensor,
  } from '../types/temperature'

  let tempSec = $derived(temperatureSection($snapshot))
  let tempError = $derived(isCollectorError(tempSec) ? tempSec.error : null)
  let sensors = $derived<TemperatureSensor[]>(
    tempSec && !isCollectorError(tempSec) && Array.isArray(tempSec.sensors)
      ? tempSec.sensors.filter(
          (s) => typeof s.temp_c === 'number' && Number.isFinite(s.temp_c),
        )
      : [],
  )
  let cpu = $derived(pickCpuSensor(sensors))
  let others = $derived(sensors.filter((s) => s !== cpu))
  let cpuStatus = $derived(tempStatus(cpu?.temp_c))
  // Keep the card neutral until the first snapshot arrives.
  let cardStatus = $derived<CardStatus | undefined>(
    $snapshot ? cpuStatus : undefined,
  )
  let cpuHistory = $derived(
    seriesHistory(temperatureSeriesKey(cpu?.name ?? 'cpu')),
  )

  let fanSec = $derived(fanSection($snapshot))
  let fanError = $derived(isCollectorError(fanSec) ? fanSec.error : null)
  let fan = $derived<FanSection | undefined>(
    fanSec && !isCollectorError(fanSec) ? fanSec : undefined,
  )
  let rpm = $derived(num(fan?.rpm))
  let pwmPct = $derived(num(fan?.pwm_pct))
  let level = $derived(num(fan?.level))
  let maxLevel = $derived(num(fan?.max_level))
  // Speed and PWM come from the hwmon chip; a fan known only from its
  // cooling device has neither.
  let hasChip = $derived(
    !!fan?.hwmon || rpm !== undefined || pwmPct !== undefined,
  )
  // Chart the speed, or the duty cycle when the fan has no tachometer.
  let fanHistory = $derived(
    seriesHistory(rpm === undefined ? FAN_PWM_SERIES : FAN_RPM_SERIES),
  )

  function num(v: unknown): number | undefined {
    return typeof v === 'number' && Number.isFinite(v) ? v : undefined
  }

  function sensorTitle(s: TemperatureSensor): string {
    const what = [s.type, s.label].filter(Boolean).join(' · ')
    return what ? `${what} (${s.source})` : s.source
  }
</script>

<Card title="Temperature" status={cardStatus}>
  {#if tempError}
    <p class="muted">Temperatures unavailable: {tempError}</p>
  {:else if $snapshot && !tempSec}
    <p class="muted">The latest snapshot has no temperatures.</p>
  {:else if $snapshot && sensors.length === 0}
    <p class="muted">No temperature sensors found.</p>
  {:else}
    <div class="cpu">
      <div class="reading">
        <span class="label" title={cpu ? sensorTitle(cpu) : undefined}>
          {cpu && cpu.name !== 'cpu' ? sensorDisplayName(cpu.name) : 'CPU'}
        </span>
        <span class="temp" data-status={cpu ? cpuStatus : undefined}>
          {formatTemp(cpu?.temp_c)}
        </span>
        {#if cpu?.name === 'cpu'}
          <span class="muted hint">Soft throttle at {PI_THROTTLE_C} °C</span>
        {/if}
      </div>
      <Sparkline
        data={$cpuHistory}
        label="CPU temperature, last 5 minutes"
        height={48}
      />
    </div>

    {#if others.length > 0}
      <ul class="sensors">
        {#each others as s (s.name)}
          <li title={sensorTitle(s)}>
            <span class="name">{sensorDisplayName(s.name)}</span>
            <span class="num">{formatTemp(s.temp_c)}</span>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}

  {#if $snapshot}
    <div class="fan">
      <h3>Fan</h3>
      {#if fanError}
        <p class="muted">Fan unavailable: {fanError}</p>
      {:else if !fan}
        <p class="muted">The latest snapshot has no fan data.</p>
      {:else if !fan.present}
        <p class="muted">No fan detected</p>
      {:else}
        <div class="fan-row">
          <dl>
            {#if hasChip}
              <dt>Speed</dt>
              <dd class="num">
                {rpm === undefined ? PLACEHOLDER : `${Math.round(rpm)} RPM`}
              </dd>
              <dt>PWM</dt>
              <dd class="num">{formatPercent(pwmPct, 0)}</dd>
            {/if}
            {#if level !== undefined}
              <dt>Level</dt>
              <dd class="num">
                {level}{#if maxLevel !== undefined}
                  <span class="muted"> / {maxLevel}</span>{/if}
              </dd>
            {/if}
          </dl>
          {#if hasChip}
            <div class="fan-chart">
              <Sparkline
                data={$fanHistory}
                min={0}
                max={rpm === undefined ? 100 : undefined}
                color="var(--chart-2)"
                label={rpm === undefined
                  ? 'Fan duty cycle, last 5 minutes'
                  : 'Fan speed, last 5 minutes'}
                height={32}
              />
            </div>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</Card>

<style>
  .cpu {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .reading {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-3);
  }

  .label {
    color: var(--color-text-muted);
    font-size: 0.875rem;
  }

  .temp {
    font-size: 2.25rem;
    font-weight: 600;
    line-height: 1.1;
    font-variant-numeric: tabular-nums;
  }

  .temp[data-status='ok'] {
    color: var(--color-ok);
  }
  .temp[data-status='warning'] {
    color: var(--color-warning);
  }
  .temp[data-status='critical'] {
    color: var(--color-critical);
  }

  .hint {
    margin-left: auto;
    font-size: 0.75rem;
  }

  .sensors {
    list-style: none;
    margin: var(--space-3) 0 0;
    padding: 0;
  }

  .sensors li {
    display: flex;
    justify-content: space-between;
    gap: var(--space-3);
    padding: var(--space-1) 0;
    border-top: 1px solid var(--color-border);
    line-height: 1.5rem;
  }

  .name {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .num {
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .fan {
    margin-top: var(--space-4);
    padding-top: var(--space-3);
    border-top: 1px solid var(--color-border);
  }

  h3 {
    margin: 0 0 var(--space-2);
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--color-text-muted);
  }

  .fan p {
    margin: 0;
  }

  .fan-row {
    display: flex;
    align-items: center;
    gap: var(--space-4);
  }

  dl {
    display: grid;
    grid-template-columns: auto auto;
    gap: 0 var(--space-3);
    margin: 0;
    flex-shrink: 0;
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.875rem;
    line-height: 1.5rem;
  }

  dd {
    margin: 0;
    line-height: 1.5rem;
  }

  .fan-chart {
    flex: 1;
    min-width: 0;
  }
</style>

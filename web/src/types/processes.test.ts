import { describe, expect, it } from 'vitest'
import {
  DEFAULT_SORT,
  nextSort,
  processesSection,
  processStateName,
  sortProcesses,
} from './processes'
import type { ProcessInfo } from './processes'
import type { Snapshot } from './snapshot'

function proc(pid: number, cpu: number | undefined, rss: number): ProcessInfo {
  return {
    pid,
    name: `p${pid}`,
    command: `/usr/bin/p${pid}`,
    user: 'pi',
    state: 'S',
    threads: 1,
    rss_bytes: rss,
    ...(cpu === undefined ? {} : { cpu_pct: cpu }),
  }
}

const pids = (list: ProcessInfo[]) => list.map((p) => p.pid)

describe('sortProcesses', () => {
  const list = [
    proc(1, 5, 100),
    proc(2, undefined, 900),
    proc(3, 40, 50),
    proc(4, 5, 300),
  ]

  it('sorts by CPU, high to low by default, missing values last', () => {
    expect(pids(sortProcesses(list, DEFAULT_SORT))).toEqual([3, 4, 1, 2])
  })

  it('keeps missing CPU values last when sorting low to high', () => {
    expect(pids(sortProcesses(list, { key: 'cpu', dir: 'asc' }))).toEqual([
      4, 1, 3, 2,
    ])
  })

  it('sorts by memory both ways', () => {
    expect(pids(sortProcesses(list, { key: 'mem', dir: 'desc' }))).toEqual([
      2, 4, 1, 3,
    ])
    expect(pids(sortProcesses(list, { key: 'mem', dir: 'asc' }))).toEqual([
      3, 1, 4, 2,
    ])
  })

  it('breaks full ties by pid', () => {
    const tied = [proc(9, 1, 10), proc(3, 1, 10), proc(5, 1, 10)]
    expect(pids(sortProcesses(tied, DEFAULT_SORT))).toEqual([3, 5, 9])
  })

  it('does not change the input', () => {
    const copy = [...list]
    sortProcesses(list, DEFAULT_SORT)
    expect(list).toEqual(copy)
  })
})

describe('nextSort', () => {
  it('flips the direction of the same column', () => {
    expect(nextSort(DEFAULT_SORT, 'cpu')).toEqual({ key: 'cpu', dir: 'asc' })
    expect(nextSort({ key: 'cpu', dir: 'asc' }, 'cpu')).toEqual(DEFAULT_SORT)
  })

  it('starts a new column high to low', () => {
    expect(nextSort({ key: 'cpu', dir: 'asc' }, 'mem')).toEqual({
      key: 'mem',
      dir: 'desc',
    })
  })
})

describe('processesSection', () => {
  const snap = (collectors: Record<string, unknown>): Snapshot => ({
    timestamp: '2026-11-07T12:00:00Z',
    collectors,
    series: {},
  })

  it('returns the section or an error', () => {
    expect(processesSection(null)).toBeUndefined()
    expect(processesSection(snap({}))).toBeUndefined()
    expect(processesSection(snap({ processes: { error: 'x' } }))).toEqual({
      error: 'x',
    })
  })
})

describe('processStateName', () => {
  it('names known states and passes others through', () => {
    expect(processStateName('R')).toBe('Running')
    expect(processStateName('?')).toBe('?')
  })
})

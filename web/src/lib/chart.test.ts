import { describe, expect, it } from 'vitest'
import { alignHistories } from './chart'

describe('alignHistories', () => {
  it('puts histories with the same times side by side', () => {
    expect(
      alignHistories(
        [
          [1, 2],
          [10, 20],
        ],
        [
          [1, 2],
          [3, 4],
        ],
      ),
    ).toEqual([
      [1, 2],
      [10, 20],
      [3, 4],
    ])
  })

  it('fills times a history lacks with null', () => {
    expect(
      alignHistories(
        [
          [1, 2, 3],
          [10, 20, 30],
        ],
        [
          [2, 3, 4],
          [1, 2, 3],
        ],
      ),
    ).toEqual([
      [1, 2, 3, 4],
      [10, 20, 30, null],
      [null, 1, 2, 3],
    ])
  })

  it('handles empty histories', () => {
    expect(alignHistories([[], []], [[], []])).toEqual([[], [], []])
  })
})

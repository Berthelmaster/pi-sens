import { describe, expect, it } from 'vitest'
import { alignHistories, spreadIndices } from './chart'

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

describe('spreadIndices', () => {
  it('keeps every row when there are few enough', () => {
    expect(spreadIndices(3, 5)).toEqual([0, 1, 2])
    expect(spreadIndices(5, 5)).toEqual([0, 1, 2, 3, 4])
  })

  it('spreads the rows from the first to the last', () => {
    expect(spreadIndices(9, 3)).toEqual([0, 4, 8])
    expect(spreadIndices(100, 4)).toEqual([0, 33, 66, 99])
  })

  it('keeps only the latest row for a limit of one', () => {
    expect(spreadIndices(10, 1)).toEqual([9])
  })

  it('returns nothing for no rows or a limit of zero', () => {
    expect(spreadIndices(0, 5)).toEqual([])
    expect(spreadIndices(10, 0)).toEqual([])
  })
})

import { getHistory, type HistoryResponse } from '../api'

/** A history response and the range it was asked for. */
export interface LoadedHistory extends HistoryResponse {
  from: number
  to: number
}

/**
 * One set of series loaded from /api/history. A new load cancels the one in
 * flight, so a fast run of range changes only ever shows the last answer.
 */
export class HistoryQuery {
  /** The last answer, kept while a newer one loads. */
  result = $state.raw<LoadedHistory | null>(null)
  /** A load the user is waiting for is in flight. */
  loading = $state(false)
  error = $state<string | null>(null)

  #ctrl: AbortController | undefined

  /**
   * Loads `keys` for [from, to). A quiet load (an auto-refresh) keeps the
   * loading flag as it is.
   */
  async load(keys: string[], from: number, to: number, quiet = false) {
    this.#ctrl?.abort()
    const ctrl = new AbortController()
    this.#ctrl = ctrl
    if (keys.length === 0) {
      this.result = null
      this.error = null
      this.loading = false
      return
    }
    if (!quiet) this.loading = true
    try {
      const res = await getHistory(keys, from, to, ctrl.signal)
      if (ctrl.signal.aborted) return
      this.result = { ...res, from, to }
      this.error = null
    } catch (err) {
      if (ctrl.signal.aborted) return
      this.error = err instanceof Error ? err.message : String(err)
    } finally {
      if (this.#ctrl === ctrl) this.loading = false
    }
  }

  /** Cancels the load in flight, if any. */
  abort() {
    this.#ctrl?.abort()
    this.#ctrl = undefined
    this.loading = false
  }
}

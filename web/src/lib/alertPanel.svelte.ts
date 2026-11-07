// UI state of the alert banner, shared by the banner and the nav button that
// opens it.

export const alertPanel = $state({
  /** Show every firing alert instead of only the first when there are many. */
  expanded: false,
  /** Show the recent (resolved) alerts; keeps the banner open without any firing. */
  recentOpen: false,
})

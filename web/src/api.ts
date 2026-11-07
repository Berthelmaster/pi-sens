export interface Health {
  status: string
  version: string
}

export async function getHealth(signal?: AbortSignal): Promise<Health> {
  const res = await fetch('/api/health', { signal })
  if (!res.ok) {
    throw new Error(`GET /api/health: ${res.status} ${res.statusText}`)
  }
  return (await res.json()) as Health
}

import { useCallback, useEffect, useRef, useState } from 'react'

export type Role = 'viewer' | 'developer' | 'admin'
export type Format = 'maven' | 'npm' | 'pypi' | 'docker' | 'go' | 'apt' | 'yum' | 'conda' | 'cargo' | 'pub' | 'huggingface' | 'lfs' | 'alpine' | 'raw'
export type RepoType = 'hosted' | 'proxy' | 'group'
export type Access = 'none' | 'read' | 'write'
export type TokenScope = 'read' | 'write' | 'full'

export interface User {
  username: string
  email: string
  role: Role
  disabled: boolean
  createdAt: number
  lastLogin: number
  /** Present when the request was made with a narrowed API token (never for browser sessions). */
  tokenScope?: TokenScope
}

export interface OidcPublic { enabled: boolean; mergeEnabled: boolean; label: string }
export interface OidcConfig {
  enabled: boolean; mergeEnabled: boolean; autoCreate: boolean; issuer: string; clientId: string; clientSecret?: string; hasSecret: boolean
  scopes: string; buttonLabel: string; usernameClaim: string; defaultRole: Role
}
export interface OidcIdentity { linked: boolean; email?: string; linkedAt?: number; hasPassword: boolean }

export interface ProxyHealth { lastOk: number; lastError?: string; lastErrorAt?: number }

export interface Repo {
  name: string
  format: Format
  type: RepoType
  description?: string
  offline: boolean
  private: boolean
  remoteUrl?: string
  remoteUsername?: string
  remotePassword?: string
  metadataTtl: number
  members?: string[]
  allowRedeploy: boolean
  distribution?: string
  quotaMb: number
  cleanupDays: number
  retainVersions: number
  createdAt: number
  // present on list/get responses
  assetCount?: number
  size?: number
  downloads?: number
  status?: 'online' | 'offline' | 'degraded'
  health?: ProxyHealth
  access?: Access
}

export interface Asset {
  id: number
  repo: string
  path: string
  sha256: string
  size: number
  contentType: string
  component: string
  version: string
  uploader: string
  createdAt: number
  updatedAt: number
  lastDownloaded: number
  downloads: number
  attrs?: Record<string, unknown>
}

export interface BrowseEntry { name: string; path: string; folder: boolean; count?: number; size?: number; asset?: Asset }
export interface VersionInfo { version: string; assets: number; size: number; downloads: number; updatedAt: number }
export interface Component { name: string; versions: VersionInfo[]; assets: number; size: number; downloads: number; updatedAt: number }
export interface PkgDep { name: string; version?: string; scope?: string; optional?: boolean }
export interface PkgInfo { description?: string; url?: string; licenses?: string[]; scm?: string; developers?: string[]; readme?: string; dependencies: PkgDep[] }
export interface FileSums { md5?: string; sha1?: string; sha256: string; sha512?: string; signed: boolean }
export interface DayCount { day: string; n: number }
export interface PackageView {
  repo: string; format: Format; component: string; version: string; latest: string
  versions: VersionInfo[]; files: { asset: Asset; sums: FileSums }[]; info: PkgInfo
  dependents: number; downloadsDaily: DayCount[]; downloadsTotal: number
}
export interface Dependent { repo: string; component: string; version: string; depVersion: string; scope: string }
export interface SavedSearch { id: number; name: string; query: string; createdAt: number }
export interface StorageReport {
  repos: { name: string; format: Format; type: RepoType; assets: number; bytes: number; downloads: number; quotaMb: number; growth: number }[]
  series: { day: string; bytes: number; logical: number }[]
  physical: number; logical: number; reclaimable?: number; orphans?: number
}

export interface Token { id: number; name: string; scope: TokenScope; createdAt: number; expiresAt: number; lastUsed: number }
export interface Settings { anonymousAccess: boolean; baseUrl: string; banner: string; auditRetentionDays: number }
export interface AuditEvent { id: number; ts: number; username: string; action: string; target: string; detail: string }
export interface Permission { repo?: string; username: string; level: Access }
export interface RemoteTest { ok: boolean; status: string; latencyMs: number; message: string }

export interface Webhook {
  id: number
  name: string
  url: string
  secret?: string
  hasSecret: boolean
  events: string[]
  repos: string[]
  enabled: boolean
  createdAt: number
  lastStatus: string
  lastDelivery: number
}

export interface Notifier {
  id: number
  name: string
  kind: 'gotify'
  url: string
  token?: string
  hasToken: boolean
  priority: number
  alertPriority: number
  events: string[]
  repos: string[]
  enabled: boolean
  createdAt: number
  lastStatus: string
  lastDelivery: number
}

export interface Task {
  id: string
  name: string
  description: string
  schedule: string
  nextRun: number
  lastRun: number
  durationMs: number
  result: string
  error?: string
  running: boolean
}

export interface Stats {
  repositories: number
  assets: number
  downloads: number
  storageBytes: number
  logicalBytes: number
  users: number
  types: Partial<Record<RepoType, number>>
  statuses: Partial<Record<'online' | 'degraded' | 'offline', number>>
  hostedBytes: number
  cachedBytes: number
  downloadsDaily: { day: string; n: number }[]
  formats: { format: Format; repos: number; assets: number; bytes: number }[]
  attention: Repo[] | null
  recent: Asset[]
  popular: Asset[]
  setup: Record<'passwordChanged' | 'baseUrl' | 'token' | 'clientConnected' | 'teamInvited' | 'notifications', boolean>
}

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message) }
}

interface Response<T> { data: T; total?: number; text: string }

async function request<T>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<Response<T>> {
  const isForm = opts.body instanceof FormData
  const method = opts.method ?? (opts.body ? 'POST' : 'GET')
  if (method !== 'GET') inflight.clear() // a read that started before this change must not answer for one after it
  let res: globalThis.Response
  try {
    res = await fetch('/api' + path, {
      method,
      credentials: 'same-origin',
      headers: opts.body && !isForm ? { 'Content-Type': 'application/json' } : undefined,
      body: isForm ? (opts.body as FormData) : opts.body ? JSON.stringify(opts.body) : undefined,
    })
  } catch {
    throw new ApiError(0, "Can't reach the server. Check your connection and try again.")
  } finally {
    if (method !== 'GET') inflight.clear()
  }
  if (res.status === 204) return { data: undefined as T, text: '' }
  const text = await res.text().catch(() => '')
  let data: any = {}
  try { data = JSON.parse(text) } catch { /* an error page from a proxy in front of RepoMan */ }
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/auth/')) window.dispatchEvent(new Event('rm:unauthorized'))
    throw new ApiError(res.status, data.error ?? (res.statusText || `The server answered ${res.status}`))
  }
  const total = res.headers.get('X-Total-Count')
  return { data: data as T, total: total === null ? undefined : Number(total), text }
}

export async function api<T = unknown>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  return (await request<T>(path, opts)).data
}

/** Like api, plus the X-Total-Count header that paged list endpoints send (capped at 10,001). */
export async function apiWithTotal<T = unknown>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<{ data: T; total?: number }> {
  return request<T>(path, opts)
}

/*
 * The last answer to each GET, so a page that was open before paints at once while its fresh copy loads
 * (stale-while-revalidate). Data is never served from here without also being fetched again. Signing in
 * or out empties it: one account's data must never flash on another account's screen.
 */
const CACHE_ENTRIES = 150
const cache = new Map<string, Response<unknown> & { seq: number }>()
const inflight = new Map<string, Promise<Response<unknown>>>()
let seq = 0

export function clearApiCache() { cache.clear(); inflight.clear() }

/** `fresh` starts a new request even when one for the same path is already on its way. */
function load<T>(path: string, fresh: boolean): Promise<Response<T>> {
  let p = fresh ? undefined : inflight.get(path)
  if (!p) {
    const mine = ++seq
    p = request(path).then((r) => {
      const prev = cache.get(path)
      if (prev && prev.seq > mine) return prev // a newer answer got here first
      // an unchanged answer keeps its object identity: no re-render, and forms built from it are left alone
      const entry = prev && prev.text === r.text ? { ...prev, total: r.total, seq: mine } : { ...r, seq: mine }
      cache.delete(path)
      cache.set(path, entry)
      if (cache.size > CACHE_ENTRIES) cache.delete(cache.keys().next().value!)
      return entry
    }, (e) => { cache.delete(path); throw e }).finally(() => { if (inflight.get(path) === p) inflight.delete(path) })
    inflight.set(path, p)
  }
  return p as Promise<Response<T>>
}

/**
 * Fetches `path` on mount and whenever it changes; `reload` re-runs it. Pass null to skip.
 * Data seen before is shown immediately and refreshed in the background, previous data stays visible while
 * the next page loads, and responses that arrive out of order are dropped.
 */
export function useApi<T>(path: string | null) {
  const hit = () => (path === null ? undefined : cache.get(path)) as Response<T> | undefined
  const [data, setData] = useState<T | undefined>(() => hit()?.data)
  const [total, setTotal] = useState<number | undefined>(() => hit()?.total)
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(path !== null)
  const latest = useRef(0)
  const run = useCallback((fresh: boolean) => {
    if (path === null) return Promise.resolve()
    const id = ++latest.current
    setLoading(true)
    return load<T>(path, fresh)
      .then((r) => { if (id === latest.current) { setData(r.data); setTotal(r.total); setError(undefined) } })
      .catch((e: Error) => { if (id === latest.current) setError(e.message) })
      .finally(() => { if (id === latest.current) setLoading(false) })
  }, [path])
  const reload = useCallback(() => run(true), [run])
  useEffect(() => {
    const seen = hit()
    if (seen) { setData(seen.data); setTotal(seen.total) }
    run(false)
  }, [run]) // eslint-disable-line react-hooks/exhaustive-deps
  return { data, total, error, loading, reload, setData }
}

import { useCallback, useEffect, useRef, useState } from 'react'

export type Role = 'viewer' | 'developer' | 'admin'
export type Format = 'maven' | 'npm' | 'pypi' | 'docker' | 'go' | 'apt' | 'yum' | 'conda' | 'raw'
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
  formats: { format: Format; repos: number; assets: number; bytes: number }[]
  attention: Repo[] | null
  recent: Asset[]
  popular: Asset[]
  setup: Record<'passwordChanged' | 'baseUrl' | 'token' | 'clientConnected' | 'teamInvited' | 'notifications', boolean>
}

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message) }
}

export async function api<T = unknown>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  return (await apiWithTotal<T>(path, opts)).data
}

/** Like api, plus the X-Total-Count header that paged list endpoints send (capped at 10,001). */
export async function apiWithTotal<T = unknown>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<{ data: T; total?: number }> {
  const isForm = opts.body instanceof FormData
  const res = await fetch('/api' + path, {
    method: opts.method ?? (opts.body ? 'POST' : 'GET'),
    credentials: 'same-origin',
    headers: opts.body && !isForm ? { 'Content-Type': 'application/json' } : undefined,
    body: isForm ? (opts.body as FormData) : opts.body ? JSON.stringify(opts.body) : undefined,
  })
  if (res.status === 204) return { data: undefined as T }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/auth/')) window.dispatchEvent(new Event('rm:unauthorized'))
    throw new ApiError(res.status, data.error ?? res.statusText)
  }
  const total = res.headers.get('X-Total-Count')
  return { data: data as T, total: total === null ? undefined : Number(total) }
}

/**
 * Fetches `path` on mount and whenever it changes; `reload` re-runs it. Pass null to skip.
 * Previous data stays visible while the next page loads, and responses that arrive out of order are dropped.
 */
export function useApi<T>(path: string | null) {
  const [data, setData] = useState<T>()
  const [total, setTotal] = useState<number>()
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(path !== null)
  const latest = useRef(0)
  const reload = useCallback(() => {
    if (path === null) return Promise.resolve()
    const id = ++latest.current
    setLoading(true)
    return apiWithTotal<T>(path)
      .then((r) => { if (id === latest.current) { setData(r.data); setTotal(r.total); setError(undefined) } })
      .catch((e: Error) => { if (id === latest.current) setError(e.message) })
      .finally(() => { if (id === latest.current) setLoading(false) })
  }, [path])
  useEffect(() => { reload() }, [reload])
  return { data, total, error, loading, reload, setData }
}

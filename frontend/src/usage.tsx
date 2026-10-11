import { Alert, Badge, Button, Code, Group, Modal, SegmentedControl, Select, Stack, Tabs, Text, Tooltip } from '@mantine/core'
import { IconInfoCircle, IconKey } from '@tabler/icons-react'
import { useMemo, useState, useSyncExternalStore } from 'react'
import { useAuth, useBaseUrl } from './App'
import { api, useApi, type Asset, type DayCount, type FileSums, type PackageView, type Repo } from './api'
import { usageSnippets } from './setup'
import { Copy, notifyError, notifyOk, plural } from './util'

// ---------- API tokens filled into snippets ----------

/** Tokens created from a snippet panel this session, per repository. Kept in memory only: a token is shown once. */
const tokens = new Map<string, string>()
const listeners = new Set<() => void>()
const subscribe = (fn: () => void) => { listeners.add(fn); return () => { listeners.delete(fn) } }

export function useRepoToken(repo: Repo) {
  const token = useSyncExternalStore(subscribe, () => tokens.get(repo.name) ?? '')
  const [busy, setBusy] = useState(false)
  const generate = () => {
    setBusy(true)
    api<{ token: string }>('/tokens', { body: { name: `${repo.name} setup`, scope: repo.type === 'hosted' ? 'write' : 'read', expiresDays: 365 } })
      .then((r) => {
        tokens.set(repo.name, r.token)
        listeners.forEach((l) => l())
        notifyOk('Token created and filled into the snippets. Copy them now — the token is not shown again.', 'API token created')
      })
      .catch(notifyError).finally(() => setBusy(false))
  }
  return { token, busy, generate }
}

// ---------- usage snippets ----------

/** Copy-paste snippets for depending on a package, per client (what Maven Central shows on a version page). */
export function UsageTabs({ repo, component, version, file, latest }: { repo: Repo; component: string; version: string; file?: string; latest?: string }) {
  const base = useBaseUrl()
  const { user } = useAuth()
  const { token, busy, generate } = useRepoToken(repo)
  // without a known latest version, ask for the version list (cheap: no files or hashing)
  const brief = useApi<PackageView>(latest === undefined && repo.format !== 'raw' ? `/repositories/${repo.name}/package?component=${encodeURIComponent(component)}&brief=1` : null)
  const newest = latest ?? brief.data?.latest
  const [pick, setPick] = useState<'this' | 'latest'>('this')
  const shown = pick === 'latest' && newest ? newest : version
  const items = useMemo(() => usageSnippets(base, repo, component, shown, { file: pick === 'latest' ? undefined : file, user: user.username, token }),
    [base, repo, component, shown, file, pick, user.username, token])
  const [tab, setTab] = useState<string | null>(null)
  if (!items.length) return null
  const cur = items.find((i) => i.id === tab) ?? items[0]
  return (
    <Stack gap="xs">
      <Group justify="space-between" wrap="nowrap" gap="xs">
        <Text fw={600} size="sm">Use this package</Text>
        {newest && newest !== version && (
          <SegmentedControl size="xs" value={pick} onChange={(v) => setPick(v as 'this' | 'latest')}
            data={[{ value: 'this', label: version || 'This version' }, { value: 'latest', label: `Latest (${newest})` }]} aria-label="Version to use" />
        )}
      </Group>
      <Tabs value={cur.id} onChange={setTab} variant="pills"><Tabs.List>{items.map((i) => <Tabs.Tab key={i.id} value={i.id}>{i.label}</Tabs.Tab>)}</Tabs.List></Tabs>
      <Group justify="space-between" wrap="nowrap" align="flex-start">
        {cur.file ? <Text size="xs" c="dimmed" ff="monospace">{cur.file}</Text> : <span />}
        <Copy value={cur.code} />
      </Group>
      <Code block className="snippet">{cur.code}</Code>
      {!token && /<YOUR_API_TOKEN>|<USERNAME>/.test(cur.code) && (
        <Group gap="xs"><Text size="xs" c="dimmed">This snippet needs credentials.</Text>
          <Button size="compact-xs" variant="light" leftSection={<IconKey size={12} />} loading={busy} onClick={generate}>Fill in my API token</Button></Group>
      )}
    </Stack>
  )
}

// ---------- download chart ----------

/** Bars of a count per day, no chart library: a title on each bar gives the exact number. */
export function DayChart({ days, label = 'downloads' }: { days: DayCount[]; label?: string }) {
  const max = Math.max(1, ...days.map((d) => d.n))
  const total = days.reduce((n, d) => n + d.n, 0)
  const w = 300, h = 56, gap = 2, bw = (w - gap * (days.length - 1)) / Math.max(1, days.length)
  return (
    <div>
      <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} role="img" aria-label={`${plural(total, label.replace(/s$/, ''))} in the last ${days.length} days`} preserveAspectRatio="none">
        {days.map((d, i) => (
          <rect key={d.day} x={i * (bw + gap)} y={h - (d.n / max) * (h - 2) - (d.n ? 0 : -1)} width={bw} height={d.n ? (d.n / max) * (h - 2) : 1} rx={1}
            fill={d.n ? 'var(--mantine-color-brand-6)' : 'var(--mantine-color-default-border)'}><title>{`${d.day}: ${plural(d.n, label.replace(/s$/, ''))}`}</title></rect>
        ))}
      </svg>
      <Group justify="space-between"><Text size="xs" c="dimmed">{days[0]?.day}</Text><Text size="xs" c="dimmed">{plural(total, label.replace(/s$/, ''))} in {days.length} days</Text><Text size="xs" c="dimmed">today</Text></Group>
    </div>
  )
}

// ---------- checksums ----------

const hashes: [keyof FileSums, string][] = [['sha256', 'SHA-256'], ['sha1', 'SHA-1'], ['md5', 'MD5'], ['sha512', 'SHA-512']]

export function HashRows({ sums }: { sums: FileSums }) {
  return (
    <>
      {hashes.filter(([k]) => sums[k]).map(([k, label]) => (
        <Group key={k} gap={6} wrap="nowrap" align="flex-start">
          <Text size="xs" c="dimmed" w={58} style={{ flexShrink: 0 }}>{label}</Text>
          <Text size="xs" ff="monospace" style={{ wordBreak: 'break-all' }}>{sums[k] as string}</Text>
          <Copy value={sums[k] as string} label={`Copy ${label}`} />
        </Group>
      ))}
      <Group gap={6}>
        <Text size="xs" c="dimmed" w={58}>Signature</Text>
        {sums.signed ? <Badge size="xs" color="teal" variant="light">GPG signed (.asc stored)</Badge> : <Badge size="xs" color="gray" variant="light">not signed</Badge>}
      </Group>
    </>
  )
}

/** The commands that verify a download against the published checksum (and signature). */
export function verifyCommand(url: string, name: string, sums: FileSums) {
  return `curl -fLO ${url}\necho "${sums.sha256}  ${name}" | sha256sum -c -` + (sums.signed ? `\ncurl -fLO ${url}.asc\ngpg --verify ${name}.asc ${name}` : '')
}

export function Checksums({ asset, repo }: { asset: Asset; repo: Repo }) {
  const base = useBaseUrl()
  const { data, error } = useApi<FileSums>(`/repositories/${repo.name}/checksums?path=${encodeURIComponent(asset.path)}`)
  if (error) return null
  if (!data) return null
  const url = `${base}/repository/${asset.repo}/${asset.path}`
  return (
    <Stack gap={6}>
      <Group justify="space-between"><Text fw={600} size="sm">Checksums & verification</Text>
        <Tooltip label="Copy commands that download the file and check it"><span><Copy value={verifyCommand(url, asset.path.split('/').pop() ?? '', data)} label="Copy verify commands" /></span></Tooltip></Group>
      <HashRows sums={data} />
    </Stack>
  )
}

// ---------- promotion ----------

export interface PromoteItem { component: string; version: string }

export function PromoteModal({ repo, items, canMove, onClose, onDone }: { repo: Repo; items: PromoteItem[]; canMove: boolean; onClose: () => void; onDone: () => void }) {
  const repos = useApi<Repo[]>('/repositories')
  const targets = (repos.data ?? []).filter((r) => r.type === 'hosted' && r.format === repo.format && r.name !== repo.name && r.access === 'write')
  const [target, setTarget] = useState<string | null>(null)
  const [mode, setMode] = useState<'copy' | 'move'>('copy')
  const [busy, setBusy] = useState(false)
  const go = () => {
    setBusy(true)
    api<{ files: number }>(`/repositories/${repo.name}/promote`, { body: { target, move: mode === 'move', items: items.map((i) => ({ component: i.component, version: i.version })) } })
      .then((r) => { notifyOk(`${mode === 'move' ? 'Moved' : 'Copied'} ${plural(r.files, 'file')} to ${target}`); onClose(); onDone() })
      .catch(notifyError).finally(() => setBusy(false))
  }
  return (
    <Modal opened onClose={onClose} title={<Text fw={600}>Promote {items.length === 1 ? `${items[0].component} ${items[0].version}` : plural(items.length, 'package')}</Text>} size="md">
      <Stack>
        <Text size="sm" c="dimmed">Publish these versions to another hosted repository (for example from staging to releases). The content is shared, so nothing is uploaded again.</Text>
        {repos.data && targets.length === 0
          ? <Alert variant="light" icon={<IconInfoCircle />}>There is no other hosted {repo.format} repository you can write to. Create one first.</Alert>
          : <Select label="Target repository" placeholder="Choose where to publish" data={targets.map((r) => r.name)} value={target} onChange={setTarget} searchable />}
        {canMove && <SegmentedControl value={mode} onChange={(v) => setMode(v as 'copy' | 'move')} data={[{ value: 'copy', label: 'Copy (keep here)' }, { value: 'move', label: 'Move (remove from here)' }]} />}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Cancel</Button>
          <Button onClick={go} loading={busy} disabled={!target}>{mode === 'move' ? 'Move' : 'Promote'}</Button>
        </Group>
      </Stack>
    </Modal>
  )
}

export const PROMOTABLE = ['maven', 'raw', 'go', 'apt', 'yum']


export function AssetDownloads({ id }: { id: number }) {
  const { data } = useApi<DayCount[]>(`/assets/${id}/downloads`)
  if (!data) return null
  return <Stack gap={4}><Text fw={600} size="sm">Downloads</Text><DayChart days={data} /></Stack>
}

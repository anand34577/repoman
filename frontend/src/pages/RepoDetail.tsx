import {
  ActionIcon, Alert, Anchor, Badge, Breadcrumbs, Button, Card, Checkbox, Code, Drawer, Group, Loader, Menu, Modal, Progress, Select, SimpleGrid, Skeleton,
  Stack, Switch, Table, Tabs, Text, TextInput, ThemeIcon, Title, Tooltip,
} from '@mantine/core'
import { Dropzone } from '@mantine/dropzone'
import { useDebouncedValue, useDocumentTitle } from '@mantine/hooks'
import {
  IconAlertTriangle, IconArrowLeft, IconBolt, IconChevronDown, IconChevronRight, IconDots, IconDownload, IconFile, IconFolder,
  IconFolderOpen, IconInfoCircle, IconKey, IconLock, IconPackage, IconPlayerPause, IconPlayerPlay, IconPlugConnected, IconRefresh,
  IconSearch, IconTrash, IconUpload, IconWand, IconX,
} from '@tabler/icons-react'
import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useAuth, useBaseUrl, useIsAdmin } from '../App'
import {
  api, useApi, type Access, type Asset, type BrowseEntry, type Component, type Permission, type RemoteTest, type Repo, type User,
} from '../api'
import { formatInfo } from '../formats'
import { clientGuides, repoUrl } from '../setup'
import { AssetDownloads, Checksums, PROMOTABLE, PromoteModal, UsageTabs, useRepoToken } from '../usage'
import {
  confirmAction, confirmTyped, Copy, EmptyState, formatBytes, FormatBadge, fullDate, notifyError, notifyOk, Pager, plural, StatusBadge, timeAgo, TypeBadge, usePaging, useUnsavedGuard, When,
} from '../util'
import { RepoSettingsFields } from './RepoConfig'
import { downloadUrl, packageLink } from '../links'

export default function RepoDetail() {
  const { name = '', '*': sub } = useParams()
  const tab = sub || 'browse'
  const navigate = useNavigate()
  const isAdmin = useIsAdmin()
  const base = useBaseUrl()
  const repo = useApi<Repo>(`/repositories/${name}`)
  useDocumentTitle(`${name} · RepoMan`)

  if (repo.error) return (
    <EmptyState icon={<IconLock />} title="Repository not found" action={<Button component={Link} to="/repositories" variant="light">Back to repositories</Button>}>
      It doesn't exist, or you don't have access to it.
    </EmptyState>
  )
  if (!repo.data) return (
    <Stack gap="md" aria-busy="true">
      <Skeleton h={14} w={120} />
      <Group><Skeleton h={48} w={48} radius="md" /><Stack gap={8} flex={1}><Skeleton h={24} w="40%" /><Skeleton h={14} w="60%" /></Stack></Group>
      <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm">{Array.from({ length: 4 }, (_, i) => <Skeleton key={i} h={62} radius="lg" />)}</SimpleGrid>
      <Skeleton h={320} radius="lg" />
    </Stack>
  )
  const r = repo.data
  const url = repoUrl(base, r)
  const canWrite = r.access === 'write'
  const quotaPct = r.quotaMb ? Math.min(100, ((r.size ?? 0) / (r.quotaMb * 1048576)) * 100) : 0
  const FIcon = formatInfo(r.format).icon

  return (
    <>
      <Anchor component={Link} to="/repositories" size="sm" c="dimmed">
        <Group gap={4}><IconArrowLeft size={14} />Repositories</Group>
      </Anchor>
      <Group justify="space-between" mt="xs" mb="md" align="flex-start">
        <Group align="flex-start" wrap="nowrap" style={{ minWidth: 0 }}>
          <ThemeIcon size={48} radius="md" variant="light" color={formatInfo(r.format).color} style={{ flexShrink: 0 }}><FIcon size={28} /></ThemeIcon>
          <div style={{ minWidth: 0 }}>
            <Group gap="xs">
              <Title order={2} style={{ wordBreak: 'break-word' }}>{r.name}</Title>
              <FormatBadge format={r.format} />
              <TypeBadge type={r.type} />
              <StatusBadge repo={r} />
              {r.private && <Badge variant="outline" color="gray" leftSection={<IconLock size={12} />}>private</Badge>}
              {r.access === 'read' && <Badge variant="outline" color="gray">read-only for you</Badge>}
            </Group>
            {r.description && <Text c="dimmed" size="sm">{r.description}</Text>}
            <Group gap={4} mt={4} wrap="nowrap">
              <Text ff="monospace" size="sm" style={{ wordBreak: 'break-all' }}>{url}</Text>
              <Copy value={url} label="Copy URL" />
            </Group>
          </div>
        </Group>
        {isAdmin && <RepoActions repo={r} onChanged={repo.reload} onDeleted={() => navigate('/repositories')} />}
      </Group>

      <SimpleGrid cols={{ base: 2, sm: 4 }} mb="md" spacing="sm">
        <Card padding="sm"><Text size="xs" c="dimmed">Artifacts</Text><Text fw={600}>{(r.assetCount ?? 0).toLocaleString()}</Text></Card>
        <Card padding="sm"><Text size="xs" c="dimmed">Size</Text><Text fw={600}>{formatBytes(r.size)}</Text></Card>
        <Card padding="sm"><Text size="xs" c="dimmed">Downloads</Text><Text fw={600}>{(r.downloads ?? 0).toLocaleString()}</Text></Card>
        <Card padding="sm">
          {r.quotaMb ? <>
            <Text size="xs" c="dimmed">Quota · {quotaPct.toFixed(0)}% of {r.quotaMb} MB</Text>
            <Progress value={quotaPct} color={quotaPct > 90 ? 'red' : quotaPct > 75 ? 'orange' : 'teal'} mt={8} />
          </> : <>
            <Text size="xs" c="dimmed">{r.type === 'group' ? 'Members' : 'Cleanup'}</Text>
            <Text fw={600} size="sm" truncate="end">{r.type === 'group' ? r.members?.join(', ') : [r.cleanupDays && `unused ${r.cleanupDays}d`, r.retainVersions && `keep ${r.retainVersions}`].filter(Boolean).join(' · ') || 'Off'}</Text>
          </>}
        </Card>
      </SimpleGrid>

      {r.status === 'degraded' && (
        <Alert color="orange" icon={<IconAlertTriangle />} mb="md" title="The remote is failing">
          {r.health?.lastError} — cached artifacts are still served. Last success {timeAgo(r.health?.lastOk ?? 0)}.
        </Alert>
      )}
      {r.offline && <Alert color="gray" icon={<IconPlayerPause />} mb="md">This repository is offline: clients receive “503 Service Unavailable”.</Alert>}

      <Tabs value={tab} onChange={(t) => navigate(`/repositories/${name}/${t === 'browse' ? '' : t}`)} keepMounted={false}>
        <Tabs.List mb="md" className="tabs-scroll">
          <Tabs.Tab value="browse" leftSection={<IconFolderOpen size={16} />}>Browse</Tabs.Tab>
          <Tabs.Tab value="components" leftSection={<IconPackage size={16} />}>Components</Tabs.Tab>
          <Tabs.Tab value="setup" leftSection={<IconWand size={16} />}>Setup</Tabs.Tab>
          {isAdmin && <Tabs.Tab value="access" leftSection={<IconKey size={16} />}>Access</Tabs.Tab>}
          {isAdmin && <Tabs.Tab value="settings" leftSection={<IconInfoCircle size={16} />}>Configuration</Tabs.Tab>}
        </Tabs.List>
        <Tabs.Panel value="browse"><Browse repo={r} canWrite={canWrite} onChanged={repo.reload} /></Tabs.Panel>
        <Tabs.Panel value="components"><Components repo={r} canWrite={canWrite} onChanged={repo.reload} /></Tabs.Panel>
        <Tabs.Panel value="setup"><Setup repo={r} base={base} /></Tabs.Panel>
        {isAdmin && <Tabs.Panel value="access"><AccessTab repo={r} /></Tabs.Panel>}
        {isAdmin && <Tabs.Panel value="settings"><Configuration repo={r} onSaved={repo.reload} /></Tabs.Panel>}
      </Tabs>
    </>
  )
}

// ---------- actions ----------

function RepoActions({ repo, onChanged, onDeleted }: { repo: Repo; onChanged: () => void; onDeleted: () => void }) {
  const { reloadRepos } = useAuth()
  const [preview, setPreview] = useState<{ count: number; size: number; sample: Asset[] }>()
  const [cleaning, setCleaning] = useState(false)
  const run = (path: string, ok: string) => api(path, { method: 'POST' }).then(() => { notifyOk(ok); onChanged() }).catch(notifyError)

  const testConnection = () => api<RemoteTest>('/repositories/test-remote', { body: repo })
    .then((t) => { if (t.ok) notifyOk(`${t.message} (${t.latencyMs} ms)`); else notifyError(new Error(t.message)); onChanged() })
    .catch(notifyError)
  const previewCleanup = () => api<{ count: number; size: number; sample: Asset[] }>(`/repositories/${repo.name}/cleanup?dryRun=1`, { method: 'POST' }).then(setPreview).catch(notifyError)
  const toggleOnline = () => api(`/repositories/${repo.name}`, { method: 'PUT', body: { ...repo, offline: !repo.offline } })
    .then(() => { notifyOk(repo.offline ? 'Repository is back online' : 'Repository is now offline'); onChanged() }).catch(notifyError)

  return (
    <>
      <Menu position="bottom-end" withArrow width={240}>
        <Menu.Target><Button variant="default" rightSection={<IconDots size={16} />}>Actions</Button></Menu.Target>
        <Menu.Dropdown>
          {repo.type === 'proxy' && <>
            <Menu.Label>Proxy</Menu.Label>
            <Menu.Item leftSection={<IconPlugConnected size={16} />} onClick={testConnection}>Test remote connection</Menu.Item>
            <Menu.Item leftSection={<IconRefresh size={16} />} onClick={() => run(`/repositories/${repo.name}/invalidate`, 'Cache invalidated — metadata will be re-fetched on next request')}>
              Invalidate cache
            </Menu.Item>
          </>}
          {repo.type === 'hosted' && ['apt', 'yum', 'npm'].includes(repo.format) && (
            <Menu.Item leftSection={<IconBolt size={16} />} onClick={() => run(`/repositories/${repo.name}/reindex`, 'Index rebuilt')}>Rebuild index</Menu.Item>
          )}
          {(repo.cleanupDays > 0 || repo.retainVersions > 0) && (
            <Menu.Item leftSection={<IconTrash size={16} />} onClick={previewCleanup}>Preview cleanup…</Menu.Item>
          )}
          <Menu.Label>Availability</Menu.Label>
          <Menu.Item leftSection={repo.offline ? <IconPlayerPlay size={16} /> : <IconPlayerPause size={16} />} onClick={toggleOnline}>
            {repo.offline ? 'Bring online' : 'Take offline'}
          </Menu.Item>
          <Menu.Divider />
          <Menu.Item color="red" leftSection={<IconTrash size={16} />} onClick={() => confirmTyped({
            title: `Delete ${repo.name}?`, name: repo.name, label: 'Delete repository',
            body: <Text size="sm">This permanently deletes the repository, its configuration and all <b>{(repo.assetCount ?? 0).toLocaleString()}</b> artifacts ({formatBytes(repo.size)}). It is also removed from any groups. This can't be undone.</Text>,
            onConfirm: () => api(`/repositories/${repo.name}`, { method: 'DELETE' }).then(() => { notifyOk('Repository deleted'); reloadRepos(); onDeleted() }),
          })}>Delete repository…</Menu.Item>
        </Menu.Dropdown>
      </Menu>

      <Modal opened={!!preview} onClose={() => setPreview(undefined)} size="lg" title={<Text fw={600}>Cleanup preview</Text>}>
        {preview && (preview.count === 0 ? (
          <Text size="sm">Nothing matches the cleanup policy right now.</Text>
        ) : (
          <Stack>
            <Text size="sm">The policy would delete <b>{preview.count.toLocaleString()}</b> artifacts, freeing up to <b>{formatBytes(preview.size)}</b>.</Text>
            <Table striped fz="xs">
              <Table.Tbody>
                {preview.sample.map((a) => <Table.Tr key={a.id}><Table.Td ff="monospace" style={{ wordBreak: 'break-all' }}>{a.path}</Table.Td><Table.Td>{formatBytes(a.size)}</Table.Td><Table.Td>{timeAgo(Math.max(a.lastDownloaded, a.updatedAt))}</Table.Td></Table.Tr>)}
              </Table.Tbody>
            </Table>
            {preview.count > preview.sample.length && <Text size="xs" c="dimmed">…and {(preview.count - preview.sample.length).toLocaleString()} more.</Text>}
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setPreview(undefined)}>Cancel</Button>
              <Button color="red" loading={cleaning} onClick={() => { setCleaning(true); api<{ deleted: number }>(`/repositories/${repo.name}/cleanup`, { method: 'POST' })
                .then((res) => { notifyOk(`Deleted ${plural(res.deleted, 'artifact')}`); setPreview(undefined); onChanged() }).catch(notifyError).finally(() => setCleaning(false)) }}>
                Delete {preview.count.toLocaleString()} artifacts
              </Button>
            </Group>
          </Stack>
        ))}
      </Modal>
    </>
  )
}

// ---------- browse ----------

function Browse({ repo, canWrite, onChanged }: { repo: Repo; canWrite: boolean; onChanged: () => void }) {
  const [params, setParams] = useSearchParams()
  const path = params.get('path') ?? ''
  const selected = params.get('select')
  const paging = usePaging(100)
  const [filter, setFilter] = useState('')
  const [dfilter] = useDebouncedValue(filter, 250)
  useEffect(() => setFilter(''), [path])
  const { data, total, loading, reload } = useApi<BrowseEntry[]>(
    `/repositories/${repo.name}/browse?path=${encodeURIComponent(path)}&q=${encodeURIComponent(dfilter)}&${paging.query}`)
  const [uploading, setUploading] = useState(false)
  const upload = formatInfo(repo.format).upload
  const canUpload = canWrite && repo.type === 'hosted' && !!upload
  const bulk = canWrite && repo.type !== 'group'
  // what is ticked, kept across pages of one folder: key "f:<id>" for a file, "d:<path>" for a folder
  const [sel, setSel] = useState<Record<string, true>>({})
  useEffect(() => setSel({}), [path, repo.name])
  const keyOf = (e: BrowseEntry) => (e.folder ? 'd:' + e.path : 'f:' + e.asset!.id)
  const picked = Object.keys(sel)
  const toggleSel = (k: string) => setSel((cur) => { const n = { ...cur }; if (n[k]) delete n[k]; else n[k] = true; return n })
  const deleteSelected = () => confirmAction({
    title: `Delete ${plural(picked.length, 'selected item')}?`, danger: true, label: 'Delete',
    body: <Text size="sm">{picked.some((k) => k[0] === 'd') ? 'Selected folders are deleted with everything inside them. ' : ''}This cannot be undone.</Text>,
    onConfirm: () => api<{ deleted: number }>(`/repositories/${repo.name}/bulk-delete`, { body: {
      ids: picked.filter((k) => k[0] === 'f').map((k) => Number(k.slice(2))), prefixes: picked.filter((k) => k[0] === 'd').map((k) => k.slice(2)),
    } }).then((r) => { notifyOk(`Deleted ${plural(r.deleted, 'file')}`); setSel({}); reload(); onChanged() }).catch(notifyError),
  })

  // opening a file keeps the page; changing folder starts at its first page
  const go = (p: string, select?: string) => setParams((cur) => {
    const next = new URLSearchParams()
    if (p) next.set('path', p)
    if (select) next.set('select', select)
    if (p === path) for (const k of ['page', 'size']) { const v = cur.get(k); if (v) next.set(k, v) }
    return next
  })
  const parts = path ? path.split('/') : []
  const onPage = data?.find((e) => e.asset?.path === selected)?.asset
  // deep links (e.g. from search) can point at a file on another page of a large folder
  const fetched = useApi<Asset>(selected && data && !onPage ? `/repositories/${repo.name}/asset?path=${encodeURIComponent(selected)}` : null)
  const asset = selected ? onPage ?? (fetched.data?.path === selected ? fetched.data : undefined) : undefined
  const rows = data

  return (
    <Card padding={0}>
      <Group justify="space-between" p="md" gap="sm">
        <Breadcrumbs separatorMargin={6}>
          <Anchor component="button" type="button" size="sm" onClick={() => go('')}>{repo.name}</Anchor>
          {parts.map((p, i) => i === parts.length - 1
            ? <Text size="sm" key={i} fw={500} aria-current="page">{p}</Text>
            : <Anchor component="button" type="button" size="sm" key={i} onClick={() => go(parts.slice(0, i + 1).join('/'))}>{p}</Anchor>)}
        </Breadcrumbs>
        <Group gap="xs" wrap="nowrap" style={{ flex: '1 1 220px', justifyContent: 'flex-end' }} maw={{ sm: 360 }}>
          <TextInput size="xs" placeholder="Filter this folder" aria-label="Filter this folder" leftSection={<IconSearch size={14} />} value={filter} onChange={(e) => { setFilter(e.currentTarget.value); paging.setPage(1, true) }} style={{ flex: 1 }} miw={140} />
          {canUpload && <Button size="xs" leftSection={<IconUpload size={14} />} onClick={() => setUploading(true)}>Upload</Button>}
        </Group>
      </Group>
      {picked.length > 0 && (
        <Group px="md" pb="xs" gap="sm">
          <Text size="sm" fw={500}>{plural(picked.length, 'item')} selected</Text>
          <Button size="compact-xs" color="red" variant="light" leftSection={<IconTrash size={14} />} onClick={deleteSelected}>Delete selected</Button>
          <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setSel({})}>Clear</Button>
        </Group>
      )}
      <Table.ScrollContainer minWidth={300}>
        <Table highlightOnHover verticalSpacing="xs" horizontalSpacing="md" style={{ opacity: loading && data ? 0.6 : 1 }} aria-busy={loading}>
          <Table.Thead>
            <Table.Tr>
              {bulk && <Table.Th w={36}><Checkbox size="xs" aria-label="Select all on this page" checked={!!rows?.length && rows.every((e) => sel[keyOf(e)])}
                indeterminate={picked.length > 0 && !rows?.every((e) => sel[keyOf(e)])}
                onChange={(ev) => { const on = ev.currentTarget.checked; setSel((cur) => { const n = { ...cur }; rows?.forEach((e) => { if (on) n[keyOf(e)] = true; else delete n[keyOf(e)] }); return n }) }} /></Table.Th>}
              <Table.Th>Name</Table.Th>{repo.type === 'group' && <Table.Th visibleFrom="sm">Served from</Table.Th>}<Table.Th ta="right">Size</Table.Th><Table.Th visibleFrom="sm">Updated</Table.Th><Table.Th ta="right" visibleFrom="sm">Downloads</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {path && (
              <Table.Tr className="row-link" tabIndex={0} aria-label="Up one folder" onKeyDown={(ev) => (ev.key === 'Enter' || ev.key === ' ') && (ev.preventDefault(), (ev.currentTarget as HTMLElement).click())} onClick={() => go(parts.slice(0, -1).join('/'))}>
                <Table.Td colSpan={4 + (bulk ? 1 : 0) + (repo.type === 'group' ? 1 : 0)}><Group gap="xs"><IconArrowLeft size={16} /><Text size="sm">..</Text></Group></Table.Td>
              </Table.Tr>
            )}
            {rows?.map((e) => (
              <Table.Tr key={e.path + e.folder} className="row-link" tabIndex={0} onKeyDown={(ev) => (ev.key === 'Enter' || ev.key === ' ') && (ev.preventDefault(), (ev.currentTarget as HTMLElement).click())} onClick={() => (e.folder ? go(e.path) : go(path, e.path))}>
                {bulk && <Table.Td onClick={(ev) => ev.stopPropagation()}><Checkbox size="xs" aria-label={`Select ${e.name}`} checked={!!sel[keyOf(e)]} onChange={() => toggleSel(keyOf(e))} /></Table.Td>}
                <Table.Td>
                  <Group gap="xs" wrap="nowrap">
                    {e.folder ? <IconFolder size={18} color="var(--mantine-color-yellow-6)" /> : <IconFile size={18} color="var(--mantine-color-dimmed)" />}
                    <Text size="sm" fw={e.folder ? 500 : 400} style={{ wordBreak: 'break-all' }}>{e.name}</Text>
                    {e.asset?.uploader === 'system' && <Tooltip label="Generated automatically"><Badge size="xs" variant="light" color="gray">index</Badge></Tooltip>}
                  </Group>
                </Table.Td>
                {repo.type === 'group' && <Table.Td visibleFrom="sm">{e.asset && <Anchor component={Link} to={`/repositories/${e.asset.repo}`} size="sm" onClick={(ev) => ev.stopPropagation()}>{e.asset.repo}</Anchor>}</Table.Td>}
                <Table.Td ta="right"><Text size="sm" c="dimmed" style={{ whiteSpace: 'nowrap' }}>{e.folder ? `${plural(e.count ?? 0, 'file')} · ${formatBytes(e.size)}` : formatBytes(e.asset!.size)}</Text></Table.Td>
                <Table.Td visibleFrom="sm">{e.asset && <When unix={e.asset.updatedAt} never="—" />}</Table.Td>
                <Table.Td ta="right" visibleFrom="sm"><Text size="sm" c="dimmed">{e.asset?.downloads.toLocaleString()}</Text></Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {loading && !data && <Group justify="center" p="xl"><Loader size="sm" /></Group>}
      <Pager paging={paging} total={total} noun="entries" />
      {!loading && data?.length === 0 && !total && dfilter && <EmptyState icon={<IconSearch />} title="Nothing in this folder matches your filter" />}
      {!loading && data?.length === 0 && !total && !dfilter && (
        <EmptyState icon={<IconFolderOpen />} title={path ? 'This folder is empty' : 'This repository is empty'}
          action={canUpload ? <Button onClick={() => setUploading(true)} leftSection={<IconUpload size={16} />}>Upload files</Button>
            : <Button component={Link} to={`/repositories/${repo.name}/setup`} variant="light">Show setup instructions</Button>}>
          {repo.type === 'proxy' ? 'Artifacts appear here as soon as a client requests them through this proxy.'
            : repo.type === 'group' ? 'None of its member repositories holds any artifacts yet.'
              : 'Publish with your usual tools (see Setup) or upload files directly.'}
        </EmptyState>
      )}

      <AssetDrawer asset={asset} repo={repo} canWrite={canWrite} onClose={() => go(path)} onDeleted={() => { go(path); reload(); onChanged() }} />
      {canUpload && <UploadModal opened={uploading} repo={repo} dir={path} onClose={() => setUploading(false)} onDone={() => { reload(); onChanged() }} />}
    </Card>
  )
}

function attrRows(attrs: Record<string, unknown>): [string, string][] {
  const skip = new Set(['control', 'md5', 'sha1', 'headerStart', 'headerEnd', 'files', 'dirs', 'description'])
  const label = (k: string) => k.replace(/([A-Z])/g, ' $1').replace(/^./, (c) => c.toUpperCase())
  return Object.entries(attrs).filter(([k, v]) => !skip.has(k) && v !== '' && v !== null && v !== 0).map(([k, v]) => {
    if (Array.isArray(v)) {
      const names = v.map((d) => (typeof d === 'object' && d ? (d as { name: string }).name : String(d)))
      return [label(k), names.slice(0, 12).join(', ') + (names.length > 12 ? ` … (+${names.length - 12})` : '')]
    }
    return [label(k), k === 'buildtime' ? fullDate(Number(v)) : k === 'installedSize' || k === 'archiveSize' ? formatBytes(Number(v)) : String(v)]
  })
}

function AssetDrawer({ asset, repo, canWrite, onClose, onDeleted }: { asset?: Asset; repo: Repo; canWrite: boolean; onClose: () => void; onDeleted: () => void }) {
  const base = useBaseUrl()
  const del = (a: Asset) => confirmAction({
    title: 'Delete this artifact?', danger: true, label: 'Delete',
    body: <Text size="sm" style={{ wordBreak: 'break-all' }}>{a.path}{['apt', 'yum', 'npm', 'conda'].includes(repo.format) && ' — the repository index is updated automatically.'}</Text>,
    onConfirm: () => api(`/assets/${a.id}`, { method: 'DELETE' }).then(() => { notifyOk('Artifact deleted'); onDeleted() }).catch(notifyError),
  })
  const rows: [string, React.ReactNode][] = asset ? [
    ['Path', <Text size="sm" ff="monospace" style={{ wordBreak: 'break-all' }}>{asset.path}</Text>],
    ['Component', asset.component || '—'],
    ['Version', asset.version || '—'],
    ['Size', `${formatBytes(asset.size)} (${asset.size.toLocaleString()} bytes)`],
    ['Content type', asset.contentType],
    ['SHA-256', <Group gap={4} wrap="nowrap"><Text size="xs" ff="monospace" style={{ wordBreak: 'break-all' }}>{asset.sha256}</Text><Copy value={asset.sha256} /></Group>],
    ['Uploaded by', asset.uploader === 'proxy' ? 'Cached from remote' : asset.uploader === 'system' ? 'Generated by RepoMan' : asset.uploader || '—'],
    ['Created', fullDate(asset.createdAt)],
    ['Updated', fullDate(asset.updatedAt)],
    ['Downloads', asset.downloads.toLocaleString()],
    ['Last downloaded', asset.lastDownloaded ? fullDate(asset.lastDownloaded) : 'never'],
  ] : []
  const pkg = asset?.attrs ? attrRows(asset.attrs) : []
  const url = asset ? base + downloadUrl(asset, repo.format) : ''

  return (
    <Drawer opened={!!asset} onClose={onClose} position="right" size="lg" title={<Text fw={600}>{asset?.path.split('/').pop()}</Text>}>
      {asset && (
        <Stack>
          <Group>
            <Button component="a" href={downloadUrl(asset, repo.format)} target="_blank" leftSection={<IconDownload size={16} />}>Download</Button>
            <Group gap={4}><Text size="sm" c="dimmed">Copy URL</Text><Copy value={url} label="Copy download URL" /></Group>
            {asset.component && <Button component={Link} to={packageLink(repo.name, asset.component, asset.version)} variant="light" leftSection={<IconPackage size={16} />}>Package page</Button>}
            {canWrite && repo.type !== 'group' && <Button variant="light" color="red" leftSection={<IconTrash size={16} />} onClick={() => del(asset)}>Delete</Button>}
          </Group>
          <Table variant="vertical" layout="fixed" withTableBorder>
            <Table.Tbody>
              {rows.map(([k, v]) => <Table.Tr key={k}><Table.Th w={140}>{k}</Table.Th><Table.Td>{v}</Table.Td></Table.Tr>)}
            </Table.Tbody>
          </Table>
          {asset.component && <UsageTabs repo={repo} component={asset.component} version={asset.version} file={asset.path} />}
          <Checksums asset={asset} repo={repo} />
          <AssetDownloads id={asset.id} />
          {pkg.length > 0 && <>
            <Text fw={600} size="sm">Package metadata</Text>
            <Table variant="vertical" layout="fixed" withTableBorder>
              <Table.Tbody>
                {pkg.map(([k, v]) => <Table.Tr key={k}><Table.Th w={140}>{k}</Table.Th><Table.Td style={{ wordBreak: 'break-word' }}>{v}</Table.Td></Table.Tr>)}
              </Table.Tbody>
            </Table>
          </>}
        </Stack>
      )}
    </Drawer>
  )
}

function UploadModal({ opened, repo, dir, onClose, onDone }: { opened: boolean; repo: Repo; dir: string; onClose: () => void; onDone: () => void }) {
  const upload = formatInfo(repo.format).upload!
  const [files, setFiles] = useState<File[]>([])
  const [target, setTarget] = useState(dir)
  const [progress, setProgress] = useState<number>() // 0–100 while uploading
  const xhr = useRef<XMLHttpRequest>()
  const busy = progress !== undefined
  useEffect(() => { if (opened) { setTarget(dir); setFiles([]) } }, [opened, dir])
  const totalBytes = files.reduce((n, f) => n + f.size, 0)

  // XHR rather than fetch: fetch can't report upload progress, which matters for multi-GB artifacts
  const submit = () => {
    const fd = new FormData()
    fd.set('path', target)
    files.forEach((f) => fd.append('file', f))
    const req = new XMLHttpRequest()
    xhr.current = req
    req.open('POST', `/api/repositories/${repo.name}/upload`)
    req.upload.onprogress = (e) => e.lengthComputable && setProgress((e.loaded / e.total) * 100)
    req.onload = () => {
      setProgress(undefined)
      let body: { uploaded?: string[]; error?: string } = {}
      try { body = JSON.parse(req.responseText) } catch { /* non-JSON error page */ }
      if (req.status >= 200 && req.status < 300) { notifyOk(`Uploaded ${plural(body.uploaded?.length ?? 0, 'file')}`); onClose(); onDone(); return }
      if (req.status === 401) window.dispatchEvent(new Event('rm:unauthorized'))
      notifyError(new Error(body.error ?? (req.status === 413 ? 'The upload is larger than the server or a proxy in front of it accepts' : `Upload failed (${req.status} ${req.statusText})`)))
      if (files.length > 1) onDone() // files before the rejected one were stored
    }
    req.onerror = () => { setProgress(undefined); notifyError(new Error('Upload failed — check your connection and try again')) }
    req.onabort = () => setProgress(undefined)
    setProgress(0)
    req.send(fd)
  }
  const close = () => { if (busy) xhr.current?.abort(); onClose() }

  return (
    <Modal opened={opened} onClose={close} closeOnClickOutside={!busy} title={<Text fw={600}>Upload to {repo.name}</Text>} size="lg">
      <Stack>
        <Alert variant="light" icon={<IconInfoCircle />} py="xs">{upload.hint}</Alert>
        {upload.dir && (
          <TextInput label={upload.dir.label} placeholder={upload.dir.placeholder} description={upload.dir.description} value={target}
            onChange={(e) => setTarget(e.currentTarget.value)} />
        )}
        <Dropzone onDrop={(f) => setFiles((cur) => [...cur, ...f.filter((x) => !cur.some((c) => c.name === x.name && c.size === x.size))])} multiple disabled={busy}
          validator={(f) => upload.accept && !upload.accept.some((ext) => f.name.endsWith(ext)) ? { code: 'type', message: `Only ${upload.accept.join(', ')} files` } : null}
          onReject={(r) => notifyError(new Error(`${r.length} file(s) skipped: ${r[0]?.errors[0]?.message}`))}>
          <Stack align="center" gap={4} py="lg" style={{ pointerEvents: 'none' }}>
            <Dropzone.Accept><IconUpload size={40} color="var(--mantine-color-brand-6)" /></Dropzone.Accept>
            <Dropzone.Reject><IconX size={40} color="var(--mantine-color-red-6)" /></Dropzone.Reject>
            <Dropzone.Idle><IconUpload size={40} color="var(--mantine-color-dimmed)" /></Dropzone.Idle>
            <Text fw={500}>Drag files here or click to choose</Text>
            <Text size="sm" c="dimmed">{upload.accept ? `Accepted: ${upload.accept.join(', ')}` : 'Any file type'}</Text>
          </Stack>
        </Dropzone>
        {files.length > 0 && (
          <Stack gap={4} mah={240} style={{ overflowY: 'auto' }}>
            {files.map((f, i) => (
              <Group key={i} justify="space-between" wrap="nowrap">
                <Group gap="xs" wrap="nowrap"><IconFile size={16} /><Text size="sm" truncate="end">{f.name}</Text></Group>
                <Group gap="xs" wrap="nowrap"><Text size="xs" c="dimmed">{formatBytes(f.size)}</Text>
                  <ActionIcon size="sm" variant="subtle" color="gray" disabled={busy} onClick={() => setFiles(files.filter((_, j) => j !== i))} aria-label={`Remove ${f.name}`}><IconX size={14} /></ActionIcon></Group>
              </Group>
            ))}
          </Stack>
        )}
        {busy && (
          <Stack gap={4}>
            <Progress value={progress} animated={progress < 100} aria-label="Upload progress" />
            <Text size="xs" c="dimmed" aria-live="polite">
              {progress < 100 ? `${progress.toFixed(0)}% of ${formatBytes(totalBytes)} sent` : 'Processing on the server…'}
            </Text>
          </Stack>
        )}
        <Group justify="space-between">
          <Text size="xs" c="dimmed">{files.length > 0 && `${plural(files.length, 'file')} · ${formatBytes(totalBytes)}`}</Text>
          <Group gap="xs">
            <Button variant="default" onClick={close}>{busy ? 'Cancel upload' : 'Cancel'}</Button>
            <Button onClick={submit} loading={busy} disabled={!files.length}>{files.length ? `Upload ${plural(files.length, 'file')}` : 'Upload'}</Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}

// ---------- components ----------

/** Versions listed under an expanded component before "Show all". */
const VERSIONS_SHOWN = 20

function Components({ repo, canWrite, onChanged }: { repo: Repo; canWrite: boolean; onChanged: () => void }) {
  const [params, setParams] = useSearchParams()
  const [q, setQ] = useState(params.get('q') ?? '')
  const [dq] = useDebouncedValue(q, 250)
  const sort = params.get('sort') ?? 'name'
  const paging = usePaging()
  const set = (key: string, value: string) => setParams((p) => { if (value) p.set(key, value); else p.delete(key); p.delete('page'); return p }, { replace: true })
  const [open, setOpen] = useState<string>()
  const [allVersions, setAllVersions] = useState(false)
  const { data, total, loading, reload } = useApi<Component[]>(`/repositories/${repo.name}/components?q=${encodeURIComponent(dq)}&sort=${sort}&${paging.query}`)
  const toggle = (name: string) => { setOpen(open === name ? undefined : name); setAllVersions(false) }
  const del = (c: string, v?: string) => confirmAction({
    title: v ? `Delete ${c} ${v}?` : `Delete all versions of ${c}?`, danger: true, label: 'Delete',
    body: v ? 'All files of this version are removed.' : 'Every version and file of this component is removed.',
    onConfirm: () => api<{ deleted: number }>(`/repositories/${repo.name}/components?component=${encodeURIComponent(c)}${v ? `&version=${encodeURIComponent(v)}` : ''}`, { method: 'DELETE' })
      .then((r) => { notifyOk(`Deleted ${plural(r.deleted, 'file')}`); reload(); onChanged() }).catch(notifyError),
  })

  const [using, setUsing] = useState<{ name: string; version: string }>()
  const bulk = canWrite && repo.type !== 'group'
  // ticked packages: "<component>\u0000<version>", an empty version meaning every version
  const [sel, setSel] = useState<Record<string, true>>({})
  const [promoting, setPromoting] = useState<{ component: string; version: string }[]>()
  useEffect(() => setSel({}), [repo.name])
  const picked = Object.keys(sel).map((k) => { const [component, version] = k.split('\u0000'); return { component, version } })
  const toggleSel = (c: string, v = '') => setSel((cur) => { const n = { ...cur }; const k = c + '\u0000' + v; if (n[k]) delete n[k]; else n[k] = true; return n })
  const canPromote = PROMOTABLE.includes(repo.format)
  const deleteSelected = () => confirmAction({
    title: `Delete ${plural(picked.length, 'selected package')}?`, danger: true, label: 'Delete',
    body: <Text size="sm">Every file of the selected versions (or of all versions where only the package is ticked) is removed. This cannot be undone.</Text>,
    onConfirm: () => api<{ deleted: number }>(`/repositories/${repo.name}/bulk-delete`, { body: { components: picked } })
      .then((r) => { notifyOk(`Deleted ${plural(r.deleted, 'file')}`); setSel({}); reload(); onChanged() }).catch(notifyError),
  })
  return (
    <Card padding={0}>
      <Group p="md" justify="space-between">
        <Text size="sm" c="dimmed">Packages, artifacts and images grouped by name and version.</Text>
        <Group gap="xs">
          <Select size="xs" w={170} aria-label="Sort components" allowDeselect={false} value={sort} onChange={(v) => set('sort', v === 'name' ? '' : v ?? '')}
            data={[{ value: 'name', label: 'Name' }, { value: 'updated', label: 'Recently updated' }, { value: 'downloads', label: 'Most downloaded' }, { value: 'size', label: 'Largest' }]} />
          <TextInput size="xs" placeholder="Filter components" aria-label="Filter components" leftSection={<IconSearch size={14} />} value={q}
            onChange={(e) => { setQ(e.currentTarget.value); set('q', e.currentTarget.value) }} />
        </Group>
      </Group>
      {picked.length > 0 && (
        <Group px="md" pb="xs" gap="sm">
          <Text size="sm" fw={500}>{plural(picked.length, 'package')} selected</Text>
          {canPromote && <Button size="compact-xs" variant="light" leftSection={<IconBolt size={14} />} onClick={() => setPromoting(picked)}>Promote…</Button>}
          <Button size="compact-xs" color="red" variant="light" leftSection={<IconTrash size={14} />} onClick={deleteSelected}>Delete selected</Button>
          <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setSel({})}>Clear</Button>
        </Group>
      )}
      <Table.ScrollContainer minWidth={700}>
        <Table verticalSpacing="xs" horizontalSpacing="md" highlightOnHover style={{ opacity: loading && data ? 0.6 : 1 }} aria-busy={loading}>
          <Table.Thead>
            <Table.Tr>{bulk && <Table.Th w={36} />}<Table.Th w={30} /><Table.Th>Component</Table.Th><Table.Th>Latest</Table.Th><Table.Th ta="right">Versions</Table.Th><Table.Th ta="right">Size</Table.Th><Table.Th ta="right">Downloads</Table.Th><Table.Th>Updated</Table.Th><Table.Th /></Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {data?.map((c) => (
              <Fragment key={c.name}>
                <Table.Tr className="row-link" tabIndex={0} aria-expanded={c.versions.length > 0 ? open === c.name : undefined} onKeyDown={(ev) => (ev.key === 'Enter' || ev.key === ' ') && (ev.preventDefault(), (ev.currentTarget as HTMLElement).click())} onClick={() => toggle(c.name)}>
                  {bulk && <Table.Td onClick={(e) => e.stopPropagation()}><Checkbox size="xs" aria-label={`Select ${c.name}`} checked={!!sel[c.name + '\u0000']} onChange={() => toggleSel(c.name)} /></Table.Td>}
                  <Table.Td>{c.versions.length > 0 && (open === c.name ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />)}</Table.Td>
                  <Table.Td><Anchor component={Link} to={packageLink(repo.name, c.name, c.versions[0]?.version)} size="sm" fw={500} style={{ wordBreak: 'break-all' }} onClick={(e) => e.stopPropagation()}>{c.name}</Anchor></Table.Td>
                  <Table.Td>{c.versions[0] ? <Badge variant="light" radius="sm">{c.versions[0].version}</Badge> : '—'}</Table.Td>
                  <Table.Td ta="right">{c.versions.length}</Table.Td>
                  <Table.Td ta="right">{formatBytes(c.size)}</Table.Td>
                  <Table.Td ta="right">{c.downloads.toLocaleString()}</Table.Td>
                  <Table.Td><When unix={c.updatedAt} never="—" /></Table.Td>
                  <Table.Td onClick={(e) => e.stopPropagation()}>
                    {canWrite && repo.type !== 'group' && <Tooltip label="Delete component"><ActionIcon variant="subtle" color="red" onClick={() => del(c.name)} aria-label="Delete component"><IconTrash size={16} /></ActionIcon></Tooltip>}
                  </Table.Td>
                </Table.Tr>
                {open === c.name && (allVersions ? c.versions : c.versions.slice(0, VERSIONS_SHOWN)).map((v) => (
                  <Table.Tr key={v.version} bg="var(--mantine-color-default-hover)">
                    {bulk && <Table.Td><Checkbox size="xs" aria-label={`Select ${c.name} ${v.version}`} checked={!!sel[c.name + '\u0000' + v.version]} onChange={() => toggleSel(c.name, v.version)} /></Table.Td>}
                    <Table.Td />
                    <Table.Td colSpan={2}><Group gap="xs"><Anchor component={Link} to={packageLink(repo.name, c.name, v.version)} size="sm" ff="monospace">{v.version}</Anchor><Text size="xs" c="dimmed">{plural(v.assets, 'file')}</Text>
                      <Button size="compact-xs" variant="light" onClick={() => setUsing({ name: c.name, version: v.version })}>Use</Button></Group></Table.Td>
                    <Table.Td />
                    <Table.Td ta="right">{formatBytes(v.size)}</Table.Td>
                    <Table.Td ta="right">{v.downloads.toLocaleString()}</Table.Td>
                    <Table.Td><When unix={v.updatedAt} never="—" /></Table.Td>
                    <Table.Td>{canWrite && repo.type !== 'group' && <Tooltip label="Delete version"><ActionIcon variant="subtle" color="red" onClick={() => del(c.name, v.version)} aria-label="Delete version"><IconTrash size={16} /></ActionIcon></Tooltip>}</Table.Td>
                  </Table.Tr>
                ))}
                {open === c.name && !allVersions && c.versions.length > VERSIONS_SHOWN && (
                  <Table.Tr bg="var(--mantine-color-default-hover)">
                    <Table.Td />
                    <Table.Td colSpan={7 + (bulk ? 1 : 0)}>
                      <Button size="compact-xs" variant="subtle" onClick={() => setAllVersions(true)}>Show all {c.versions.length.toLocaleString()} versions</Button>
                    </Table.Td>
                  </Table.Tr>
                )}
              </Fragment>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {loading && !data && <Group justify="center" p="xl"><Loader size="sm" /></Group>}
      <Pager paging={paging} total={total} noun="components" />
      {promoting && <PromoteModal repo={repo} items={promoting} canMove={repo.type === 'hosted' && canWrite} onClose={() => setPromoting(undefined)} onDone={() => { setSel({}); reload(); onChanged() }} />}
      <Modal opened={!!using} onClose={() => setUsing(undefined)} size="lg" title={<Text fw={600}>{using?.name} {using?.version}</Text>}>
        {using && <UsageTabs repo={repo} component={using.name} version={using.version} />}
      </Modal>
      {!loading && data?.length === 0 && !total && <EmptyState icon={<IconPackage />} title={q ? 'No matching components' : 'No components yet'}>
        {repo.format === 'raw' ? 'Raw repositories store plain files — use the Browse tab.' : 'Components appear once packages are published or cached.'}
      </EmptyState>}
    </Card>
  )
}

// ---------- setup ----------

function Setup({ repo, base }: { repo: Repo; base: string }) {
  const { user } = useAuth()
  const { token, busy, generate } = useRepoToken(repo)
  const guides = useMemo(() => clientGuides(base, repo, user.username, token || '<YOUR_API_TOKEN>'), [base, repo, user.username, token])
  const [tab, setTab] = useState<string | null>(null)
  const active = guides.find((g) => g.id === tab) ?? guides[0]

  return (
    <Stack maw={920}>
      <Card padding="md">
        <Group justify="space-between" wrap="nowrap" gap="md">
          <Group gap="sm" wrap="nowrap">
            <ThemeIcon variant="light" size="lg"><IconKey size={18} /></ThemeIcon>
            <div>
              <Text fw={600} size="sm">{token ? 'Snippets are personalized with a new token' : 'Personalize these snippets'}</Text>
              <Text size="xs" c="dimmed">{token
                ? 'This token expires in a year and can be revoked under Account & tokens.'
                : <>Snippets use your username <b>{user.username}</b>. Generate an API token to fill in the password too.</>}</Text>
            </div>
          </Group>
          {!token && <Button size="xs" variant="light" onClick={generate} loading={busy}>Generate token</Button>}
        </Group>
      </Card>
      {repo.type === 'group' && (
        <Card padding="md">
          <Text fw={600} size="sm" mb={4}>Resolution order</Text>
          <Text size="xs" c="dimmed" mb="xs">Clients ask the members in this order and get the first answer, so put private repositories before public proxies. Change it under Configuration.</Text>
          <Group gap="xs">{repo.members?.map((m, i) => (
            <Badge key={m} variant="light" color="gray" size="lg" radius="sm" leftSection={<Text span size="xs" fw={700}>{i + 1}</Text>}><Anchor component={Link} to={`/repositories/${m}`} size="xs" c="inherit">{m}</Anchor></Badge>
          ))}</Group>
        </Card>
      )}
      {repo.type !== 'hosted' && (
        <Alert variant="light" icon={<IconInfoCircle />} py="xs">
          {repo.type === 'group' ? 'Groups are read-only: publish to a hosted member and consume through this group.' : 'Proxy repositories are read-only and fill their cache on first request.'}
        </Alert>
      )}
      <Tabs value={active.id} onChange={setTab} variant="pills">
        <Tabs.List>{guides.map((g) => <Tabs.Tab key={g.id} value={g.id}>{g.label}</Tabs.Tab>)}</Tabs.List>
      </Tabs>
      {active.steps.map((s, i) => (
        <Card key={active.id + i} padding="md">
          <Group justify="space-between" mb="xs" wrap="nowrap" align="flex-start">
            <Group gap="sm" wrap="nowrap" align="flex-start">
              <Badge circle size="lg" variant="light">{i + 1}</Badge>
              <div>
                <Text fw={600} size="sm">{s.title}</Text>
                {s.file && <Text size="xs" c="dimmed" ff="monospace">{s.file}</Text>}
                {s.description && <Text size="xs" c="dimmed" mt={2}>{s.description}</Text>}
              </div>
            </Group>
            <Copy value={s.code} />
          </Group>
          <Code block className="snippet">{s.code}</Code>
        </Card>
      ))}
    </Stack>
  )
}

// ---------- access ----------

const levelOptions = [
  { value: 'inherit', label: 'Inherit from role' },
  { value: 'none', label: 'No access' },
  { value: 'read', label: 'Read' },
  { value: 'write', label: 'Read & write' },
]
const roleDefault: Record<string, string> = { admin: 'Read & write (admin)', developer: 'Read & write', viewer: 'Read' }

function AccessTab({ repo }: { repo: Repo }) {
  const { settings } = useAuth()
  const users = useApi<User[]>('/users')
  const perms = useApi<Permission[]>(`/repositories/${repo.name}/permissions`)
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [q, setQ] = useState('')
  const [onlyOverrides, setOnlyOverrides] = useState(false)
  const paging = usePaging()
  useEffect(() => { if (perms.data) setDraft(Object.fromEntries(perms.data.map((p) => [p.username, p.level]))) }, [perms.data])
  const ql = q.trim().toLowerCase()
  const matching = (users.data ?? []).filter((u) => (u.username.toLowerCase().includes(ql) || u.email.toLowerCase().includes(ql))
    && (!onlyOverrides || (draft[u.username] ?? 'inherit') !== 'inherit'))
  const shown = matching.slice(paging.offset, paging.offset + paging.size)
  const filterBy = (fn: () => void) => { fn(); paging.setPage(1, true) }
  const saved = Object.fromEntries((perms.data ?? []).map((p) => [p.username, p.level]))
  const dirty = Object.keys({ ...saved, ...draft }).some((k) => (draft[k] ?? 'inherit') !== (saved[k] ?? 'inherit'))
  useUnsavedGuard(dirty)

  const save = () => {
    setBusy(true)
    const body = Object.entries(draft).filter(([, l]) => l !== 'inherit').map(([username, level]) => ({ username, level: level as Access }))
    api(`/repositories/${repo.name}/permissions`, { method: 'PUT', body }).then(() => { notifyOk('Access saved'); perms.reload() }).catch(notifyError).finally(() => setBusy(false))
  }
  const anonymous = settings?.anonymousAccess && !repo.private

  return (
    <Stack maw={920}>
      <Alert variant="light" icon={<IconInfoCircle />}>
        By default everyone gets access from their role: <b>viewers</b> can read, <b>developers</b> can also publish and delete,
        and <b>admins</b> can do everything. Override that for individual users here — for example, let a viewer publish to
        just this repository, or hide it from someone entirely.
      </Alert>
      <Card padding="md">
        <Group gap="sm" wrap="nowrap">
          <ThemeIcon variant="light" color={anonymous ? 'orange' : 'teal'}>{anonymous ? <IconAlertTriangle size={18} /> : <IconLock size={18} />}</ThemeIcon>
          <Text size="sm">{anonymous ? <>Anonymous users <b>can read</b> this repository (server-wide anonymous access is on). Mark it private in Configuration to require sign-in.</>
            : <>Anonymous users <b>cannot</b> read this repository.</>}</Text>
        </Group>
      </Card>
      <Card padding={0}>
        <Group p="md" justify="space-between" gap="sm">
          <Switch size="sm" label="Only users with an override" checked={onlyOverrides} onChange={(e) => { const v = e.currentTarget.checked; filterBy(() => setOnlyOverrides(v)) }} />
          <TextInput size="xs" placeholder="Filter by name or email" aria-label="Filter users" leftSection={<IconSearch size={14} />} value={q}
            onChange={(e) => { const v = e.currentTarget.value; filterBy(() => setQ(v)) }} />
        </Group>
        <Table.ScrollContainer minWidth={560}>
        <Table verticalSpacing="sm" horizontalSpacing="md">
          <Table.Thead><Table.Tr><Table.Th>User</Table.Th><Table.Th>From role</Table.Th><Table.Th w={220}>Access to {repo.name}</Table.Th></Table.Tr></Table.Thead>
          <Table.Tbody>
            {shown.map((u) => (
              <Table.Tr key={u.username}>
                <Table.Td><Text size="sm" fw={500} span>{u.username}</Text>{u.disabled && <Badge size="xs" color="gray" ml="xs">disabled</Badge>}</Table.Td>
                <Table.Td><Text size="sm" c="dimmed">{roleDefault[u.role]}</Text></Table.Td>
                <Table.Td>
                  {u.role === 'admin' ? <Text size="sm" c="dimmed">Always full access</Text> : (
                    <Select size="xs" data={levelOptions} value={draft[u.username] ?? 'inherit'} allowDeselect={false}
                      onChange={(v) => setDraft({ ...draft, [u.username]: v ?? 'inherit' })} aria-label={`Access for ${u.username}`} />
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        </Table.ScrollContainer>
        {users.data && matching.length === 0 && <EmptyState icon={<IconSearch />} title="No users match">Clear the filter to see everyone.</EmptyState>}
        <Pager paging={paging} total={matching.length} noun="users" />
      </Card>
      <Group justify="flex-end" gap="sm">
        {dirty && <Text size="sm" c="dimmed">Unsaved changes — they are kept across pages until you save.</Text>}
        <Button onClick={save} loading={busy} disabled={!dirty}>Save access</Button>
      </Group>
    </Stack>
  )
}

// ---------- configuration ----------

function Configuration({ repo, onSaved }: { repo: Repo; onSaved: () => void }) {
  const repos = useApi<Repo[]>('/repositories')
  const [r, setR] = useState<Repo>(repo)
  const [busy, setBusy] = useState(false)
  useEffect(() => setR(repo), [repo])
  const dirty = JSON.stringify(r) !== JSON.stringify(repo)
  useUnsavedGuard(dirty)
  const save = () => {
    setBusy(true)
    api<Repo>(`/repositories/${repo.name}`, { method: 'PUT', body: r })
      .then(() => { notifyOk('Configuration saved'); onSaved() }).catch(notifyError).finally(() => setBusy(false))
  }
  return (
    <Stack maw={820}>
      <RepoSettingsFields repo={r} set={(p) => setR((cur) => ({ ...cur, ...p }))} repos={repos.data ?? []} editing />
      <Card padding="sm" pos="sticky" bottom={16} shadow="md" style={{ zIndex: 2 }}>
        <Group justify="space-between">
          <Text size="sm" c="dimmed">{dirty ? 'You have unsaved changes.' : 'All changes saved.'}</Text>
          <Group gap="xs">
            {dirty && <Button variant="default" onClick={() => setR(repo)}>Discard</Button>}
            <Button onClick={save} loading={busy} disabled={!dirty}>Save changes</Button>
          </Group>
        </Group>
      </Card>
    </Stack>
  )
}

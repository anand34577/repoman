import { ActionIcon, Anchor, Badge, Button, Card, Group, Loader, Popover, Select, Skeleton, Table, Text, TextInput } from '@mantine/core'
import { useDebouncedValue } from '@mantine/hooks'
import { IconBookmark, IconSearch, IconSearchOff, IconX } from '@tabler/icons-react'
import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, useApi, type Asset, type Repo, type SavedSearch } from '../api'
import { FORMATS } from '../formats'
import { EmptyState, formatBytes, FormatBadge, LoadError, MAX_COUNT, notifyError, notifyOk, PageHeader, Pager, usePaging, When } from '../util'
import { assetLink, packageLink } from '../links'

const sorts = [
  { value: 'updated', label: 'Recently updated' }, { value: 'name', label: 'Name' },
  { value: 'downloads', label: 'Most downloaded' }, { value: 'size', label: 'Largest' },
]

export default function Search() {
  // the URL is the source of truth so results can be shared and the header search works from this page too
  const [params, setParams] = useSearchParams()
  const urlQ = params.get('q') ?? ''
  const repo = params.get('repo')
  const format = params.get('format')
  const sort = params.get('sort') ?? 'updated'
  const paging = usePaging()
  const [q, setQ] = useState(urlQ)
  useEffect(() => setQ(urlQ), [urlQ])
  const [dq] = useDebouncedValue(q, 250)
  // any filter change starts again at page 1
  const update = (key: string, value: string | null) => setParams((p) => {
    if (value) p.set(key, value); else p.delete(key)
    p.delete('page')
    return p
  }, { replace: true })

  const repos = useApi<Repo[]>('/repositories')
  const saved = useApi<SavedSearch[]>('/saved-searches')
  const [saving, setSaving] = useState(false)
  const [saveName, setSaveName] = useState('')
  // a saved search is the URL's filters without paging
  const currentQuery = (() => { const c = new URLSearchParams(params); c.delete('page'); c.delete('size'); return c.toString() })()
  const save = () => api('/saved-searches', { body: { name: saveName.trim(), query: currentQuery } })
    .then(() => { notifyOk(`Saved “${saveName.trim()}”`); setSaving(false); setSaveName(''); saved.reload() }).catch(notifyError)
  const forget = (s: SavedSearch) => api(`/saved-searches/${s.id}`, { method: 'DELETE' }).then(saved.reload).catch(notifyError)
  const qs = new URLSearchParams({ q: dq, repo: repo ?? '', format: format ?? '', sort })
  const { data, total, loading, error, reload } = useApi<Asset[]>(`/search?${qs}&${paging.query}`)
  const formatOf = Object.fromEntries((repos.data ?? []).map((r) => [r.name, r.format]))

  return (
    <>
      <PageHeader title="Search" description="Find artifacts by name, path or component across every repository you can read." />
      <Group mb="md" align="flex-end" gap="sm">
        <TextInput flex={1} miw={240} placeholder="e.g. lodash, com.acme:lib, version:1.2 repo:releases, sha256:ab12…" aria-label="Search artifacts" leftSection={<IconSearch size={16} />}
          rightSection={loading && data ? <Loader size="xs" /> : null} value={q} autoFocus
          onChange={(e) => { setQ(e.currentTarget.value); update('q', e.currentTarget.value) }} />
        <Select placeholder="All formats" aria-label="Filter by format" clearable value={format} onChange={(v) => update('format', v)}
          data={FORMATS.map((f) => ({ value: f.id, label: f.label }))} w={{ base: '100%', xs: 160 }} />
        <Select placeholder="All repositories" aria-label="Filter by repository" clearable searchable value={repo} onChange={(v) => update('repo', v)}
          data={repos.data?.map((r) => r.name) ?? []} w={{ base: '100%', xs: 220 }} limit={100} nothingFoundMessage="No repository" />
        <Select aria-label="Sort results" value={sort} allowDeselect={false} onChange={(v) => update('sort', v === 'updated' ? null : v)}
          data={sorts} w={{ base: '100%', xs: 180 }} />
      </Group>
      <Group gap="xs" mb="sm" align="center">
        <Popover opened={saving} onChange={setSaving} width={280} position="bottom-start" withArrow trapFocus>
          <Popover.Target>
            <Button size="compact-sm" variant="default" leftSection={<IconBookmark size={14} />} disabled={!currentQuery} onClick={() => setSaving((o) => !o)}>Save this search</Button>
          </Popover.Target>
          <Popover.Dropdown>
            <form onSubmit={(e) => { e.preventDefault(); if (saveName.trim()) save() }}>
              <TextInput label="Name" placeholder="e.g. Unreleased builds" data-autofocus value={saveName} onChange={(e) => setSaveName(e.currentTarget.value)} maxLength={80} />
              <Button type="submit" size="xs" mt="xs" fullWidth disabled={!saveName.trim()}>Save</Button>
            </form>
          </Popover.Dropdown>
        </Popover>
        {saved.data?.map((s) => (
          <Badge key={s.id} size="lg" variant={s.query === currentQuery ? 'filled' : 'light'} radius="sm" style={{ textTransform: 'none', cursor: 'pointer' }}
            rightSection={<ActionIcon size="xs" variant="transparent" color="inherit" aria-label={`Delete saved search ${s.name}`} onClick={(e) => { e.stopPropagation(); forget(s) }}><IconX size={12} /></ActionIcon>}
            onClick={() => setParams(new URLSearchParams(s.query))} role="button" tabIndex={0}
            onKeyDown={(e) => { if (e.key === 'Enter') setParams(new URLSearchParams(s.query)) }}>{s.name}</Badge>
        ))}
        <Text size="xs" c="dimmed" ml="auto">Operators: <code>version:</code> <code>repo:</code> <code>format:</code> <code>uploader:</code> <code>sha256:</code></Text>
      </Group>
      <LoadError error={error} retry={reload} />
      {total !== undefined && total > 0 && (
        <Text size="sm" c="dimmed" mb="xs" aria-live="polite">
          {total > MAX_COUNT ? `More than ${MAX_COUNT.toLocaleString()}` : total.toLocaleString()} artifact{total === 1 ? '' : 's'} found
        </Text>
      )}
      <Card padding={0}>
        <Table.ScrollContainer minWidth={760}>
          <Table highlightOnHover horizontalSpacing="md" style={{ opacity: loading && data ? 0.6 : 1, transition: 'opacity 150ms' }} aria-busy={loading}>
            <Table.Thead>
              <Table.Tr><Table.Th>Component</Table.Th><Table.Th>Version</Table.Th><Table.Th>Path</Table.Th><Table.Th>Repository</Table.Th><Table.Th ta="right">Size</Table.Th><Table.Th>Updated</Table.Th></Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {!data && loading && Array.from({ length: 5 }, (_, i) => <Table.Tr key={i}><Table.Td colSpan={6}><Skeleton h={20} /></Table.Td></Table.Tr>)}
              {data?.map((a) => (
                <Table.Tr key={a.id}>
                  <Table.Td><Anchor component={Link} to={a.component ? packageLink(a.repo, a.component, a.version) : assetLink(a)} size="sm" fw={500}>{a.component || a.path.split('/').pop()}</Anchor></Table.Td>
                  <Table.Td><Text size="sm">{a.version || '—'}</Text></Table.Td>
                  <Table.Td><Anchor component={Link} to={assetLink(a)} size="xs" ff="monospace" c="dimmed" style={{ wordBreak: 'break-all' }}>{a.path}</Anchor></Table.Td>
                  <Table.Td>
                    <Group gap={6} wrap="nowrap">
                      {formatOf[a.repo] && <FormatBadge format={formatOf[a.repo]} />}
                      <Anchor component={Link} to={`/repositories/${a.repo}`} size="sm">{a.repo}</Anchor>
                    </Group>
                  </Table.Td>
                  <Table.Td ta="right"><Text size="sm" style={{ whiteSpace: 'nowrap' }}>{formatBytes(a.size)}</Text></Table.Td>
                  <Table.Td><When unix={a.updatedAt} never="—" /></Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {!loading && data?.length === 0 && !total && (
          <EmptyState icon={<IconSearchOff />} title="No artifacts found">
            {q || repo || format ? 'Try a shorter search term or clear the filters.' : 'Nothing has been published or cached yet.'}
          </EmptyState>
        )}
        <Pager paging={paging} total={total} noun="artifacts" />
      </Card>
    </>
  )
}

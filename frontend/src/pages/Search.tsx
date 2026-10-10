import { Anchor, Card, Group, Loader, Select, Skeleton, Table, Text, TextInput } from '@mantine/core'
import { useDebouncedValue } from '@mantine/hooks'
import { IconSearch, IconSearchOff } from '@tabler/icons-react'
import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useApi, type Asset, type Repo } from '../api'
import { FORMATS } from '../formats'
import { EmptyState, formatBytes, FormatBadge, LoadError, PageHeader, timeAgo } from '../util'
import { assetLink } from '../links'

export default function Search() {
  // the URL is the source of truth so results can be shared and the header search works from this page too
  const [params, setParams] = useSearchParams()
  const urlQ = params.get('q') ?? ''
  const repo = params.get('repo')
  const format = params.get('format')
  const [q, setQ] = useState(urlQ)
  useEffect(() => setQ(urlQ), [urlQ])
  const [dq] = useDebouncedValue(q, 250)
  const update = (key: string, value: string | null) => setParams((p) => {
    if (value) p.set(key, value); else p.delete(key)
    return p
  }, { replace: true })

  const repos = useApi<Repo[]>('/repositories')
  const qs = new URLSearchParams({ q: dq, repo: repo ?? '', format: format ?? '' })
  const { data, loading, error, reload } = useApi<Asset[]>(`/search?${qs}`)
  const formatOf = Object.fromEntries((repos.data ?? []).map((r) => [r.name, r.format]))

  return (
    <>
      <PageHeader title="Search" description="Find artifacts by name, path or component across every repository you can read." />
      <Group mb="md" align="flex-end" gap="sm">
        <TextInput flex={1} miw={240} placeholder="e.g. lodash, com.acme, nginx" aria-label="Search artifacts" leftSection={<IconSearch size={16} />}
          rightSection={loading && data ? <Loader size="xs" /> : null} value={q} autoFocus
          onChange={(e) => { setQ(e.currentTarget.value); update('q', e.currentTarget.value) }} />
        <Select placeholder="All formats" aria-label="Filter by format" clearable value={format} onChange={(v) => update('format', v)}
          data={FORMATS.map((f) => ({ value: f.id, label: f.label }))} w={{ base: '100%', xs: 160 }} />
        <Select placeholder="All repositories" aria-label="Filter by repository" clearable searchable value={repo} onChange={(v) => update('repo', v)}
          data={repos.data?.map((r) => r.name) ?? []} w={{ base: '100%', xs: 220 }} />
      </Group>
      <LoadError error={error} retry={reload} />
      <Card padding={0}>
        <Table.ScrollContainer minWidth={760}>
          <Table highlightOnHover horizontalSpacing="md">
            <Table.Thead>
              <Table.Tr><Table.Th>Component</Table.Th><Table.Th>Version</Table.Th><Table.Th>Path</Table.Th><Table.Th>Repository</Table.Th><Table.Th ta="right">Size</Table.Th><Table.Th>Updated</Table.Th></Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {!data && loading && Array.from({ length: 5 }, (_, i) => <Table.Tr key={i}><Table.Td colSpan={6}><Skeleton h={20} /></Table.Td></Table.Tr>)}
              {data?.map((a) => (
                <Table.Tr key={a.id}>
                  <Table.Td><Anchor component={Link} to={assetLink(a)} size="sm" fw={500}>{a.component || a.path.split('/').pop()}</Anchor></Table.Td>
                  <Table.Td><Text size="sm">{a.version || '—'}</Text></Table.Td>
                  <Table.Td><Text size="xs" ff="monospace" c="dimmed" style={{ wordBreak: 'break-all' }}>{a.path}</Text></Table.Td>
                  <Table.Td>
                    <Group gap={6} wrap="nowrap">
                      {formatOf[a.repo] && <FormatBadge format={formatOf[a.repo]} />}
                      <Anchor component={Link} to={`/repositories/${a.repo}`} size="sm">{a.repo}</Anchor>
                    </Group>
                  </Table.Td>
                  <Table.Td ta="right"><Text size="sm">{formatBytes(a.size)}</Text></Table.Td>
                  <Table.Td><Text size="sm" c="dimmed">{timeAgo(a.updatedAt)}</Text></Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {!loading && data?.length === 0 && (
          <EmptyState icon={<IconSearchOff />} title="No artifacts found">
            {q || repo || format ? 'Try a shorter search term or clear the filters.' : 'Nothing has been published or cached yet.'}
          </EmptyState>
        )}
        {data?.length === 500 && <Text c="dimmed" size="xs" ta="center" py="md">Showing the first 500 results — refine your search.</Text>}
      </Card>
    </>
  )
}

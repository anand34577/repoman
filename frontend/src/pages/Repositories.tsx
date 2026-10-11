import { Anchor, Badge, Box, Button, Card, Chip, Group, SegmentedControl, Select, Skeleton, Stack, Table, Text, TextInput, ThemeIcon, Tooltip } from '@mantine/core'
import { IconDatabaseOff, IconLock, IconPlus, IconSearch } from '@tabler/icons-react'
import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useBaseUrl, useIsAdmin } from '../App'
import { useApi, type Repo } from '../api'
import { FORMATS, formatInfo, FormatIcon } from '../formats'
import { repoUrl } from '../setup'
import { Copy, EmptyState, formatBytes, FormatBadge, LoadError, PageHeader, Pager, plural, StatusBadge, TypeBadge, usePaging } from '../util'

const sorts: { value: string; label: string; cmp: (a: Repo, b: Repo) => number }[] = [
  { value: 'name', label: 'Name', cmp: (a, b) => a.name.localeCompare(b.name) },
  { value: 'artifacts', label: 'Most artifacts', cmp: (a, b) => (b.assetCount ?? 0) - (a.assetCount ?? 0) },
  { value: 'size', label: 'Largest', cmp: (a, b) => (b.size ?? 0) - (a.size ?? 0) },
  { value: 'downloads', label: 'Most downloaded', cmp: (a, b) => (b.downloads ?? 0) - (a.downloads ?? 0) },
  { value: 'newest', label: 'Newest', cmp: (a, b) => b.createdAt - a.createdAt },
]

export default function Repositories() {
  const isAdmin = useIsAdmin()
  const base = useBaseUrl()
  const { data, error, reload } = useApi<Repo[]>('/repositories')
  const [formats, setFormats] = useState<string[]>([])
  const [type, setType] = useState('all')
  const [q, setQ] = useState('')
  const [sort, setSort] = useState('name')
  const paging = usePaging()
  const navigate = useNavigate()
  const filterBy = <T,>(set: (v: T) => void) => (v: T) => { set(v); paging.setPage(1, true) }

  const present = FORMATS.filter((f) => data?.some((r) => r.format === f.id))
  const rows = useMemo(() => (data ?? []).filter((r) =>
    (formats.length === 0 || formats.includes(r.format)) && (type === 'all' || r.type === type) &&
    (r.name.includes(q.trim().toLowerCase()) || r.description?.toLowerCase().includes(q.trim().toLowerCase())))
    .sort(sorts.find((s) => s.value === sort)!.cmp), [data, formats, type, q, sort])
  const shown = rows.slice(paging.offset, paging.offset + paging.size)

  return (
    <>
      <PageHeader
        title="Repositories"
        description="Hosted repositories store your artifacts, proxies cache public registries, and groups combine both behind one URL."
        action={isAdmin && <Button component={Link} to="/repositories/new" leftSection={<IconPlus size={16} />}>Create repository</Button>}
      />
      <LoadError error={error} retry={reload} />
      <Group mb="md" justify="space-between" gap="sm">
        <Chip.Group multiple value={formats} onChange={filterBy(setFormats)}>
          <Group gap={6}>
            {present.map((f) => (
              <Chip key={f.id} value={f.id} size="sm" variant="outline" icon={<FormatIcon format={f.id} size={14} />}>{f.label}</Chip>
            ))}
          </Group>
        </Chip.Group>
        <Group gap="sm" w={{ base: '100%', md: 'auto' }}>
          <SegmentedControl size="sm" value={type} onChange={filterBy(setType)} data={['all', 'hosted', 'proxy', 'group']} aria-label="Repository type" w={{ base: '100%', xs: 'auto' }} />
          <Select size="sm" w={{ base: '100%', xs: 170 }} aria-label="Sort repositories" allowDeselect={false} value={sort} onChange={(v) => setSort(v ?? 'name')} data={sorts} />
          <TextInput placeholder="Filter by name" aria-label="Filter repositories by name" leftSection={<IconSearch size={16} />} value={q} onChange={(e) => filterBy(setQ)(e.currentTarget.value)}
            style={{ flex: '1 1 180px' }} />
        </Group>
      </Group>

      {/* phones: one card per repository instead of a table that scrolls sideways */}
      <Stack gap="sm" hiddenFrom="sm" mb="sm">
        {!data && !error && Array.from({ length: 4 }, (_, i) => <Skeleton key={i} h={96} radius="lg" />)}
        {shown.map((r) => {
          const f = formatInfo(r.format)
          return (
            <Card key={r.name} component={Link} to={`/repositories/${r.name}`} padding="md" className="select-card card-link">
              <Group justify="space-between" wrap="nowrap" align="flex-start" gap="sm">
                <Group gap="sm" wrap="nowrap" style={{ minWidth: 0 }}>
                  <ThemeIcon variant="light" color={f.color} size={40} radius="md"><f.icon size={22} /></ThemeIcon>
                  <div style={{ minWidth: 0 }}>
                    <Group gap={6} wrap="nowrap">
                      <Text fw={600} truncate="end">{r.name}</Text>
                      {r.private && <IconLock size={14} color="var(--mantine-color-dimmed)" aria-label="Private" style={{ flexShrink: 0 }} />}
                    </Group>
                    <Text size="xs" c="dimmed">{f.label} · {r.type}{r.access === 'read' && ' · read-only'}</Text>
                  </div>
                </Group>
                <StatusBadge repo={r} />
              </Group>
              {r.description && <Text size="sm" c="dimmed" lineClamp={2} mt="xs">{r.description}</Text>}
              <Text size="xs" c="dimmed" mt="xs">{plural(r.assetCount ?? 0, 'artifact')} · {formatBytes(r.size)} · {plural(r.downloads ?? 0, 'download')}</Text>
            </Card>
          )
        })}
      </Stack>

      <Card padding={0}>
        <Box visibleFrom="sm">
        <Table.ScrollContainer minWidth={980}>
          <Table highlightOnHover verticalSpacing="sm" horizontalSpacing="lg">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Name</Table.Th><Table.Th>Format</Table.Th><Table.Th>Type</Table.Th><Table.Th>Status</Table.Th><Table.Th>URL</Table.Th>
                <Table.Th ta="right">Artifacts</Table.Th><Table.Th ta="right">Size</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {!data && !error && Array.from({ length: 5 }, (_, i) => (
                <Table.Tr key={i}><Table.Td colSpan={7}><Skeleton h={20} /></Table.Td></Table.Tr>
              ))}
              {shown.map((r) => {
                const url = repoUrl(base, r)
                return (
                  <Table.Tr key={r.name} className="row-link" onClick={() => navigate(`/repositories/${r.name}`)}>
                    <Table.Td>
                      <Group gap={6} wrap="nowrap">
                        <Anchor component={Link} to={`/repositories/${r.name}`} fw={600} style={{ whiteSpace: 'nowrap' }} onClick={(e) => e.stopPropagation()}>{r.name}</Anchor>
                        {r.private && <Tooltip label="Private: sign-in required"><IconLock size={14} color="var(--mantine-color-dimmed)" /></Tooltip>}
                        {r.access === 'read' && <Badge size="xs" variant="outline" color="gray">read-only</Badge>}
                      </Group>
                      {r.description && <Text size="xs" c="dimmed" lineClamp={1} maw={320}>{r.description}</Text>}
                    </Table.Td>
                    <Table.Td><FormatBadge format={r.format} /></Table.Td>
                    <Table.Td><TypeBadge type={r.type} /></Table.Td>
                    <Table.Td><StatusBadge repo={r} /></Table.Td>
                    <Table.Td onClick={(e) => e.stopPropagation()}>
                      <Group gap={4} wrap="nowrap">
                        <Tooltip label={url} openDelay={500}><Text size="xs" ff="monospace" c="dimmed" truncate="end" maw={240}>{url}</Text></Tooltip>
                        <Copy value={url} label="Copy URL" />
                      </Group>
                    </Table.Td>
                    <Table.Td ta="right">{(r.assetCount ?? 0).toLocaleString()}</Table.Td>
                    <Table.Td ta="right" style={{ whiteSpace: 'nowrap' }}>{formatBytes(r.size)}</Table.Td>
                  </Table.Tr>
                )
              })}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        </Box>
        <Pager paging={paging} total={rows.length} noun="repositories" />
        {data && rows.length === 0 && (
          <EmptyState icon={<IconDatabaseOff />} title={data.length ? 'No repositories match your filters' : 'No repositories yet'}
            action={data.length ? <Button variant="light" onClick={() => { setFormats([]); setType('all'); setQ('') }}>Clear filters</Button>
              : isAdmin && <Button component={Link} to="/repositories/new">Create your first repository</Button>}>
            {data.length ? 'Try clearing the format chips or search text.' : isAdmin ? 'Create one with the guided setup.' : 'Ask an administrator for access.'}
          </EmptyState>
        )}
      </Card>
    </>
  )
}

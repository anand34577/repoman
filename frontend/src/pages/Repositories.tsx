import { Anchor, Badge, Button, Card, Chip, Group, SegmentedControl, Skeleton, Table, Text, TextInput, Tooltip } from '@mantine/core'
import { IconDatabaseOff, IconLock, IconPlus, IconSearch } from '@tabler/icons-react'
import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useBaseUrl, useIsAdmin } from '../App'
import { useApi, type Repo } from '../api'
import { FORMATS, FormatIcon } from '../formats'
import { repoUrl } from '../setup'
import { Copy, EmptyState, formatBytes, FormatBadge, LoadError, PageHeader, StatusBadge, TypeBadge } from '../util'

export default function Repositories() {
  const isAdmin = useIsAdmin()
  const base = useBaseUrl()
  const { data, error, reload } = useApi<Repo[]>('/repositories')
  const [formats, setFormats] = useState<string[]>([])
  const [type, setType] = useState('all')
  const [q, setQ] = useState('')
  const navigate = useNavigate()

  const present = FORMATS.filter((f) => data?.some((r) => r.format === f.id))
  const rows = useMemo(() => (data ?? []).filter((r) =>
    (formats.length === 0 || formats.includes(r.format)) && (type === 'all' || r.type === type) &&
    (r.name.includes(q.toLowerCase()) || r.description?.toLowerCase().includes(q.toLowerCase()))), [data, formats, type, q])

  return (
    <>
      <PageHeader
        title="Repositories"
        description="Hosted repositories store your artifacts, proxies cache public registries, and groups combine both behind one URL."
        action={isAdmin && <Button component={Link} to="/repositories/new" leftSection={<IconPlus size={16} />}>Create repository</Button>}
      />
      <LoadError error={error} retry={reload} />
      <Group mb="md" justify="space-between" gap="sm">
        <Chip.Group multiple value={formats} onChange={setFormats}>
          <Group gap={6}>
            {present.map((f) => (
              <Chip key={f.id} value={f.id} size="sm" variant="outline" icon={<FormatIcon format={f.id} size={14} />}>{f.label}</Chip>
            ))}
          </Group>
        </Chip.Group>
        <Group gap="sm">
          <SegmentedControl size="sm" value={type} onChange={setType} data={['all', 'hosted', 'proxy', 'group']} />
          <TextInput placeholder="Filter by name" aria-label="Filter repositories by name" leftSection={<IconSearch size={16} />} value={q} onChange={(e) => setQ(e.currentTarget.value)} />
        </Group>
      </Group>
      <Card padding={0}>
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
              {rows.map((r) => {
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
                        <Text size="xs" ff="monospace" c="dimmed" truncate="end" maw={240} title={url}>{url}</Text>
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
        {data && rows.length === 0 && (
          <EmptyState icon={<IconDatabaseOff />} title={data.length ? 'No repositories match your filters' : 'No repositories yet'}
            action={!data.length && isAdmin && <Button component={Link} to="/repositories/new">Create your first repository</Button>}>
            {data.length ? 'Try clearing the format chips or search text.' : isAdmin ? 'Create one with the guided setup.' : 'Ask an administrator for access.'}
          </EmptyState>
        )}
      </Card>
    </>
  )
}

import { Anchor, Badge, Button, Card, Group, Progress, SegmentedControl, SimpleGrid, Table, Text } from '@mantine/core'
import { IconRecycle, IconCamera } from '@tabler/icons-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, useApi, type StorageReport } from '../api'
import { FormatBadge, formatBytes, LoadError, notifyError, notifyOk, PageHeader } from '../util'

/** Line chart of bytes per day: solid = distinct bytes on disk, dashed = the sum of every repository (before sharing identical files). */
function GrowthChart({ series }: { series: StorageReport['series'] }) {
  const w = 640, h = 140, pad = 4
  if (series.length < 2) return <Text size="sm" c="dimmed">The chart appears after RepoMan has recorded usage on two different days. It records once a day; use “Record now” to add today.</Text>
  const max = Math.max(1, ...series.flatMap((s) => [s.bytes, s.logical]))
  const x = (i: number) => pad + (i / (series.length - 1)) * (w - 2 * pad)
  const y = (v: number) => h - pad - (v / max) * (h - 2 * pad)
  const line = (f: (s: StorageReport['series'][number]) => number) => series.map((s, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(f(s)).toFixed(1)}`).join(' ')
  return (
    <div>
      <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} role="img" aria-label={`Storage grew from ${formatBytes(series[0].bytes)} to ${formatBytes(series[series.length - 1].bytes)}`} preserveAspectRatio="none">
        <path d={line((s) => s.logical)} fill="none" stroke="var(--mantine-color-dimmed)" strokeWidth={1.5} strokeDasharray="4 3" vectorEffect="non-scaling-stroke" />
        <path d={line((s) => s.bytes)} fill="none" stroke="var(--mantine-color-brand-6)" strokeWidth={2.5} vectorEffect="non-scaling-stroke" />
        {series.map((s, i) => <circle key={s.day} cx={x(i)} cy={y(s.bytes)} r={6} fill="transparent"><title>{`${s.day}: ${formatBytes(s.bytes)} on disk, ${formatBytes(s.logical)} counting every copy`}</title></circle>)}
      </svg>
      <Group justify="space-between"><Text size="xs" c="dimmed">{series[0].day}</Text><Text size="xs" c="dimmed">peak {formatBytes(max)}</Text><Text size="xs" c="dimmed">{series[series.length - 1].day}</Text></Group>
    </div>
  )
}

export default function Storage() {
  const [days, setDays] = useState('90')
  const [scan, setScan] = useState(false)
  const { data, error, loading, reload } = useApi<StorageReport>(`/storage?days=${days}${scan ? '&scan=1' : ''}`)
  const run = (id: string, msg: string) => api(`/tasks/${id}/run`, { method: 'POST' }).then(() => { notifyOk(msg); setTimeout(reload, 1200) }).catch(notifyError)
  const total = Math.max(1, data?.logical ?? 1)
  const saved = data ? Math.max(0, data.logical - data.physical) : 0

  return (
    <>
      <PageHeader title="Storage" description="Where the space goes, how it grows, and what could be freed. Identical files are stored once, however many repositories hold them."
        action={<Group gap="xs">
          <Button variant="default" leftSection={<IconCamera size={16} />} onClick={() => run('storage-snapshot', 'Recorded today’s usage')}>Record now</Button>
          <Button variant="default" leftSection={<IconRecycle size={16} />} onClick={() => run('blob-compact', 'Compaction started — see Scheduled tasks for the result')}>Compact now</Button>
        </Group>} />
      <LoadError error={error} retry={reload} />
      <SimpleGrid cols={{ base: 2, md: 4 }} mb="md" spacing="sm">
        <Card padding="sm"><Text size="xs" c="dimmed">On disk</Text><Text fw={600}>{formatBytes(data?.physical)}</Text></Card>
        <Card padding="sm"><Text size="xs" c="dimmed">Counting every copy</Text><Text fw={600}>{formatBytes(data?.logical)}</Text></Card>
        <Card padding="sm"><Text size="xs" c="dimmed">Saved by sharing identical files</Text><Text fw={600}>{formatBytes(saved)}</Text></Card>
        <Card padding="sm">
          <Text size="xs" c="dimmed">Reclaimable now</Text>
          {data?.reclaimable === undefined
            ? <Button size="compact-xs" variant="light" mt={2} loading={loading && scan} onClick={() => setScan(true)}>Scan for unused files</Button>
            : <Text fw={600}>{formatBytes(data.reclaimable)} <Text span size="xs" c="dimmed">in {data.orphans?.toLocaleString()} files</Text></Text>}
        </Card>
      </SimpleGrid>

      <Card padding="md" mb="md">
        <Group justify="space-between" mb="xs">
          <Text fw={600}>Growth</Text>
          <SegmentedControl size="xs" value={days} onChange={setDays} data={[{ value: '30', label: '30 days' }, { value: '90', label: '90 days' }, { value: '365', label: '1 year' }]} aria-label="Period" />
        </Group>
        <GrowthChart series={data?.series ?? []} />
      </Card>

      <Card padding={0}>
        <Table.ScrollContainer minWidth={720}>
          <Table highlightOnHover verticalSpacing="xs" horizontalSpacing="md">
            <Table.Thead><Table.Tr><Table.Th>Repository</Table.Th><Table.Th w={200}>Share</Table.Th><Table.Th ta="right">Size</Table.Th><Table.Th ta="right">Artifacts</Table.Th><Table.Th ta="right">Change ({days}d)</Table.Th><Table.Th ta="right">Quota</Table.Th></Table.Tr></Table.Thead>
            <Table.Tbody>
              {data?.repos.map((r) => {
                const pct = r.quotaMb ? (r.bytes / (r.quotaMb * 1048576)) * 100 : 0
                return (
                  <Table.Tr key={r.name}>
                    <Table.Td><Group gap="xs" wrap="nowrap"><FormatBadge format={r.format} /><Anchor component={Link} to={`/repositories/${r.name}`} size="sm">{r.name}</Anchor><Badge size="xs" variant="light" color="gray">{r.type}</Badge></Group></Table.Td>
                    <Table.Td><Progress value={(r.bytes / total) * 100} size="sm" aria-label={`${r.name} share of storage`} /></Table.Td>
                    <Table.Td ta="right"><Text size="sm" style={{ whiteSpace: 'nowrap' }}>{formatBytes(r.bytes)}</Text></Table.Td>
                    <Table.Td ta="right">{r.assets.toLocaleString()}</Table.Td>
                    <Table.Td ta="right"><Text size="sm" c={r.growth > 0 ? 'orange' : 'dimmed'}>{r.growth ? `${r.growth > 0 ? '+' : '−'}${formatBytes(Math.abs(r.growth))}` : '—'}</Text></Table.Td>
                    <Table.Td ta="right">{r.quotaMb ? <Badge variant="light" color={pct > 90 ? 'red' : pct > 75 ? 'orange' : 'teal'}>{pct.toFixed(0)}% of {r.quotaMb} MB</Badge> : <Text size="sm" c="dimmed">none</Text>}</Table.Td>
                  </Table.Tr>
                )
              })}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Card>
      <Text size="xs" c="dimmed" mt="sm">Unused files are removed daily by the “Compact blob store” task. Groups store nothing themselves and are not listed. <Anchor component={Link} to="/tasks" size="xs">Scheduled tasks</Anchor></Text>
    </>
  )
}

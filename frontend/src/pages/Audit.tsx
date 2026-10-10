import { Badge, Button, Card, Group, SegmentedControl, Skeleton, Table, Text, TextInput, Tooltip } from '@mantine/core'
import { useDebouncedValue } from '@mantine/hooks'
import { IconActivity, IconDownload, IconSearch } from '@tabler/icons-react'
import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useApi, type AuditEvent } from '../api'
import { EmptyState, fullDate, LoadError, PageHeader, Pager, timeAgo, usePaging } from '../util'

const actionColor = (a: string) =>
  a.includes('failed') ? 'red' : a.includes('delete') ? 'orange' : /create|publish|push|upload/.test(a) ? 'teal' : a.startsWith('login') ? 'blue' : 'gray'

const categories = [
  { value: '', label: 'All' }, { value: 'login', label: 'Sign-ins' }, { value: 'repository', label: 'Repositories' },
  { value: 'user', label: 'Users' }, { value: 'token', label: 'Tokens' },
]

export default function AuditLog() {
  const [params, setParams] = useSearchParams()
  const cat = params.get('action') ?? ''
  const [q, setQ] = useState(params.get('q') ?? '')
  const [dq] = useDebouncedValue(q, 250)
  const paging = usePaging()
  const set = (key: string, value: string) => setParams((p) => {
    if (value) p.set(key, value); else p.delete(key)
    p.delete('page')
    return p
  }, { replace: true })

  const filter = new URLSearchParams({ q: dq, action: cat })
  const { data, total, loading, error, reload } = useApi<AuditEvent[]>(`/audit?${filter}&${paging.query}`)

  return (
    <>
      <PageHeader title="Audit log" description="Who did what, and when. Newest first; the export contains every matching event."
        action={<Button component="a" href={`/api/audit/export?${filter}`} download variant="default" leftSection={<IconDownload size={16} />}
          data-disabled={!total || undefined} onClick={(e) => !total && e.preventDefault()}>Export CSV</Button>} />
      <Group mb="md" justify="space-between">
        <SegmentedControl value={cat} onChange={(v) => set('action', v)} data={categories} aria-label="Event category" />
        <TextInput placeholder="Search user, action or target" leftSection={<IconSearch size={16} />} value={q} onChange={(e) => { setQ(e.currentTarget.value); set('q', e.currentTarget.value) }} w={{ base: '100%', sm: 300 }} aria-label="Search audit events" />
      </Group>
      <LoadError error={error} retry={reload} />
      <Card padding={0}>
        <Table.ScrollContainer minWidth={760}>
          <Table verticalSpacing="xs" horizontalSpacing="md" striped highlightOnHover style={{ opacity: loading && data ? 0.6 : 1 }} aria-busy={loading}>
            <Table.Thead><Table.Tr><Table.Th w={140}>When</Table.Th><Table.Th>User</Table.Th><Table.Th>Action</Table.Th><Table.Th>Target</Table.Th><Table.Th>Detail</Table.Th></Table.Tr></Table.Thead>
            <Table.Tbody>
              {!data && loading && Array.from({ length: 8 }, (_, i) => <Table.Tr key={i}><Table.Td colSpan={5}><Skeleton h={18} /></Table.Td></Table.Tr>)}
              {data?.map((e) => (
                <Table.Tr key={e.id}>
                  <Table.Td><Tooltip label={fullDate(e.ts)}><Text size="sm" c="dimmed">{timeAgo(e.ts)}</Text></Tooltip></Table.Td>
                  <Table.Td><Text size="sm">{e.username}</Text></Table.Td>
                  <Table.Td><Badge variant="light" color={actionColor(e.action)} radius="sm">{e.action}</Badge></Table.Td>
                  <Table.Td><Text size="sm" ff="monospace" style={{ wordBreak: 'break-all' }}>{e.target}</Text></Table.Td>
                  <Table.Td><Text size="sm" c="dimmed">{e.detail}</Text></Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {!loading && data?.length === 0 && !total && <EmptyState icon={<IconActivity />} title="No matching events" />}
        <Pager paging={paging} total={total} noun="events" />
      </Card>
    </>
  )
}

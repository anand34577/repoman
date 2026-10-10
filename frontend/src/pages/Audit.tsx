import { Badge, Button, Card, Group, SegmentedControl, Table, Text, TextInput, Tooltip } from '@mantine/core'
import { useDebouncedValue } from '@mantine/hooks'
import { IconActivity, IconDownload, IconSearch } from '@tabler/icons-react'
import { useState } from 'react'
import { useApi, type AuditEvent } from '../api'
import { EmptyState, fullDate, LoadError, PageHeader, timeAgo } from '../util'

const actionColor = (a: string) =>
  a.includes('failed') ? 'red' : a.includes('delete') ? 'orange' : /create|publish|push|upload/.test(a) ? 'teal' : a.startsWith('login') ? 'blue' : 'gray'

const categories = [
  { value: '', label: 'All' }, { value: 'login', label: 'Sign-ins' }, { value: 'repository', label: 'Repositories' },
  { value: 'user', label: 'Users' }, { value: 'token', label: 'Tokens' },
]

export default function AuditLog() {
  const [q, setQ] = useState('')
  const [cat, setCat] = useState('')
  const [dq] = useDebouncedValue(q, 250)
  const { data, loading, error, reload } = useApi<AuditEvent[]>(`/audit?q=${encodeURIComponent(dq)}&action=${cat}`)

  const exportCsv = () => {
    const csv = ['time,user,action,target,detail', ...(data ?? []).map((e) =>
      [new Date(e.ts * 1000).toISOString(), e.username, e.action, e.target, e.detail].map((v) => `"${String(v).replace(/"/g, '""')}"`).join(','))].join('\n')
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv' }))
    a.download = `repoman-audit-${new Date().toISOString().slice(0, 10)}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  return (
    <>
      <PageHeader title="Audit log" description="Who did what, and when. The latest 1,000 matching events are shown."
        action={<Button variant="default" leftSection={<IconDownload size={16} />} onClick={exportCsv} disabled={!data?.length}>Export CSV</Button>} />
      <Group mb="md" justify="space-between">
        <SegmentedControl value={cat} onChange={setCat} data={categories} aria-label="Event category" />
        <TextInput placeholder="Search user, action or target" leftSection={<IconSearch size={16} />} value={q} onChange={(e) => setQ(e.currentTarget.value)} w={{ base: '100%', sm: 300 }} aria-label="Search audit events" />
      </Group>
      <LoadError error={error} retry={reload} />
      <Card padding={0}>
        <Table.ScrollContainer minWidth={760}>
          <Table verticalSpacing="xs" horizontalSpacing="md" striped highlightOnHover>
            <Table.Thead><Table.Tr><Table.Th w={140}>When</Table.Th><Table.Th>User</Table.Th><Table.Th>Action</Table.Th><Table.Th>Target</Table.Th><Table.Th>Detail</Table.Th></Table.Tr></Table.Thead>
            <Table.Tbody>
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
        {!loading && data?.length === 0 && <EmptyState icon={<IconActivity />} title="No matching events" />}
      </Card>
    </>
  )
}

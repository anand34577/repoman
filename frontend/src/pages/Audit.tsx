import '@mantine/dates/styles.css'
import { Badge, Button, Card, Group, SegmentedControl, Select, Skeleton, Table, Text, TextInput } from '@mantine/core'
import { DatePickerInput, type DatesRangeValue } from '@mantine/dates'
import { useDebouncedValue } from '@mantine/hooks'
import { IconActivity, IconCalendar, IconDownload, IconSearch } from '@tabler/icons-react'
import dayjs from 'dayjs'
import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useApi, type AuditEvent } from '../api'
import { EmptyState, LoadError, PageHeader, Pager, usePaging, When } from '../util'

const actionColor = (a: string) =>
  a.includes('failed') ? 'red' : a.includes('delete') ? 'orange' : /create|publish|push|upload/.test(a) ? 'teal' : a.startsWith('login') ? 'blue' : 'gray'

const categories = [
  { value: '', label: 'All' }, { value: 'login', label: 'Sign-ins' }, { value: 'repository', label: 'Repositories' },
  { value: 'user', label: 'Users' }, { value: 'token', label: 'Tokens' },
]

// Periods end today and are kept in the URL as plain dates (?from=2026-01-01&to=2026-01-31), in the viewer's time zone.
const DAY = 'YYYY-MM-DD'
const periods = [
  { value: 'any', label: 'Any time' }, { value: '1', label: 'Today' }, { value: '7', label: 'Last 7 days' },
  { value: '30', label: 'Last 30 days' }, { value: '90', label: 'Last 90 days' }, { value: 'custom', label: 'Custom range…' },
]
const validDay = (s: string | null) => (s && dayjs(s, DAY).isValid() ? dayjs(s, DAY).startOf('day') : null)

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

  const from = validDay(params.get('from')), to = validDay(params.get('to'))
  const [picking, setPicking] = useState<DatesRangeValue>() // a range whose second day is not chosen yet
  const [custom, setCustom] = useState(false)
  const today = dayjs().startOf('day')
  const days = from && to && to.isSame(today) ? to.diff(from, 'day') + 1 : 0
  const period = custom ? 'custom' : !from && !to ? 'any' : periods.some((p) => p.value === String(days)) ? String(days) : 'custom'
  const setRange = (a: dayjs.Dayjs | null, b: dayjs.Dayjs | null) => setParams((p) => {
    if (a) p.set('from', a.format(DAY)); else p.delete('from')
    if (b) p.set('to', b.format(DAY)); else p.delete('to')
    p.delete('page')
    return p
  }, { replace: true })
  const choosePeriod = (v: string | null) => {
    setPicking(undefined)
    setCustom(v === 'custom')
    if (v === 'any') setRange(null, null)
    else if (v && v !== 'custom') setRange(today.subtract(Number(v) - 1, 'day'), today)
  }
  const pick = (range: DatesRangeValue) => {
    const [a, b] = range
    if (a && b) { setPicking(undefined); setRange(dayjs(a), dayjs(b)) }
    else if (!a && !b) { setPicking(undefined); setRange(null, null) }
    else setPicking(range)
  }

  // the server takes Unix seconds: from the first day's midnight up to (not including) the midnight after the last day
  const filter = new URLSearchParams({ q: dq, action: cat, from: from ? String(from.unix()) : '', to: to ? String(to.add(1, 'day').unix()) : '' })
  const { data, total, loading, error, reload } = useApi<AuditEvent[]>(`/audit?${filter}&${paging.query}`)

  return (
    <>
      <PageHeader title="Audit log" description="Who did what, and when. Newest first; the export contains every matching event."
        action={<Button component="a" href={`/api/audit/export?${filter}`} download variant="default" leftSection={<IconDownload size={16} />}
          data-disabled={!total || undefined} onClick={(e) => !total && e.preventDefault()}>Export CSV</Button>} />
      <Group mb="md" justify="space-between">
        <SegmentedControl value={cat} onChange={(v) => set('action', v)} data={categories} aria-label="Event category" />
        <Group gap="sm" w={{ base: '100%', md: 'auto' }}>
          <Select aria-label="Period" data={periods} value={period} onChange={choosePeriod} allowDeselect={false} w={{ base: '100%', xs: 190 }}
            leftSection={<IconCalendar size={16} />} comboboxProps={{ withinPortal: true }} />
          {period === 'custom' && (
            <DatePickerInput type="range" aria-label="Date range" placeholder="Pick the first and last day" value={picking ?? [from?.toDate() ?? null, to?.toDate() ?? null]}
              onChange={pick} maxDate={new Date()} allowSingleDateInRange clearable valueFormat="D MMM YYYY" w={{ base: '100%', xs: 250 }}
              popoverProps={{ withinPortal: true, shadow: 'lg' }} />
          )}
          <TextInput placeholder="Search user, action or target" leftSection={<IconSearch size={16} />} value={q} onChange={(e) => { setQ(e.currentTarget.value); set('q', e.currentTarget.value) }}
            style={{ flex: '1 1 220px' }} aria-label="Search audit events" />
        </Group>
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
                  <Table.Td><When unix={e.ts} never="—" /></Table.Td>
                  <Table.Td><Text size="sm">{e.username}</Text></Table.Td>
                  <Table.Td><Badge variant="light" color={actionColor(e.action)} radius="sm">{e.action}</Badge></Table.Td>
                  <Table.Td><Text size="sm" ff="monospace" style={{ wordBreak: 'break-all' }}>{e.target}</Text></Table.Td>
                  <Table.Td><Text size="sm" c="dimmed">{e.detail}</Text></Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {!loading && data?.length === 0 && !total && <EmptyState icon={<IconActivity />} title="No matching events">
          {q || cat || from || to ? 'Try another category, a longer period or a shorter search term.' : 'Sign-ins, changes and deletions will be listed here.'}
        </EmptyState>}
        <Pager paging={paging} total={total} noun="events" />
      </Card>
    </>
  )
}

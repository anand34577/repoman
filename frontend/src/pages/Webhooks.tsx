import {
  ActionIcon, Badge, Button, Card, Code, Group, Modal, MultiSelect, PasswordInput, SimpleGrid, Stack, Switch, Table, Text, TextInput, Tooltip,
} from '@mantine/core'
import { IconPencil, IconPlus, IconRefresh, IconSend, IconTrash, IconWebhook } from '@tabler/icons-react'
import { useState } from 'react'
import { api, useApi, type Repo, type Webhook } from '../api'
import { EVENT_GROUPS } from '../events'
import { confirmAction, EmptyState, LoadError, notifyError, notifyOk, PageHeader, timeAgo } from '../util'


const verifyExample = `// Node.js: verify a delivery
const crypto = require('crypto')
const expected = 'sha256=' + crypto.createHmac('sha256', SECRET).update(rawBody).digest('hex')
if (!crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(req.headers['x-repoman-signature']))) {
  return res.status(401).end()
}`

const payloadExample = `{
  "event": "docker.push",
  "actor": "alice",
  "target": "docker-hosted/team/api:1.4.0",
  "detail": "",
  "timestamp": 1760000000
}`

export default function Webhooks() {
  const { data, error, reload } = useApi<Webhook[]>('/webhooks')
  const [editing, setEditing] = useState<Webhook | 'new' | null>(null)
  const [testing, setTesting] = useState<number>()

  const test = (h: Webhook) => {
    setTesting(h.id)
    api<{ status: string }>(`/webhooks/${h.id}/test`, { method: 'POST' })
      .then((r) => { (r.status.startsWith('2') ? notifyOk : (m: string) => notifyError(new Error(m)))(`Test delivery: ${r.status}`); reload() })
      .catch(notifyError).finally(() => setTesting(undefined))
  }
  const remove = (h: Webhook) => confirmAction({
    title: `Delete webhook ${h.name}?`, danger: true, label: 'Delete', body: 'No further events will be sent to this URL.',
    onConfirm: () => api(`/webhooks/${h.id}`, { method: 'DELETE' }).then(() => { notifyOk('Webhook deleted'); reload() }).catch(notifyError),
  })

  return (
    <>
      <PageHeader title="Webhooks" description="Tell other systems when things happen — e.g. trigger a deployment when an image is pushed. For messages to people, use Notifications."
        action={<Button leftSection={<IconPlus size={16} />} onClick={() => setEditing('new')}>Add webhook</Button>} />
      <LoadError error={error} retry={reload} />
      <Card padding={0} mb="lg">
        {data?.length === 0 ? (
          <EmptyState icon={<IconWebhook />} title="No webhooks yet" action={<Button onClick={() => setEditing('new')}>Add your first webhook</Button>}>
            RepoMan POSTs a JSON event to your URL, signed with HMAC-SHA256 so you can verify it came from here.
          </EmptyState>
        ) : (
          <Table.ScrollContainer minWidth={800}>
            <Table verticalSpacing="sm" horizontalSpacing="lg">
              <Table.Thead><Table.Tr><Table.Th>Name</Table.Th><Table.Th>Events</Table.Th><Table.Th>Repositories</Table.Th><Table.Th>Last delivery</Table.Th><Table.Th /></Table.Tr></Table.Thead>
              <Table.Tbody>
                {data?.map((h) => (
                  <Table.Tr key={h.id} opacity={h.enabled ? 1 : 0.6}>
                    <Table.Td>
                      <Group gap="xs"><Text size="sm" fw={500}>{h.name}</Text>{!h.enabled && <Badge size="xs" color="gray">paused</Badge>}</Group>
                      <Text size="xs" c="dimmed" ff="monospace" truncate="end" maw={320}>{h.url}</Text>
                    </Table.Td>
                    <Table.Td><Text size="sm">{h.events.length ? h.events.join(', ') : 'All events'}</Text></Table.Td>
                    <Table.Td><Text size="sm">{h.repos.length ? h.repos.join(', ') : 'All repositories'}</Text></Table.Td>
                    <Table.Td>
                      {h.lastDelivery ? <>
                        <Badge variant="light" color={h.lastStatus.startsWith('2') ? 'teal' : 'red'} radius="sm">{h.lastStatus}</Badge>
                        <Text size="xs" c="dimmed">{timeAgo(h.lastDelivery)}</Text>
                      </> : <Text size="sm" c="dimmed">Never</Text>}
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4} justify="flex-end" wrap="nowrap">
                        <Tooltip label="Send test event"><ActionIcon variant="subtle" loading={testing === h.id} onClick={() => test(h)} aria-label="Send test event"><IconSend size={16} /></ActionIcon></Tooltip>
                        <Tooltip label="Edit"><ActionIcon variant="subtle" color="gray" onClick={() => setEditing(h)} aria-label="Edit webhook"><IconPencil size={16} /></ActionIcon></Tooltip>
                        <Tooltip label="Delete"><ActionIcon variant="subtle" color="red" onClick={() => remove(h)} aria-label="Delete webhook"><IconTrash size={16} /></ActionIcon></Tooltip>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Card>
      <SimpleGrid cols={{ base: 1, md: 2 }}>
        <Card padding="md"><Text fw={600} size="sm" mb="xs">Example payload</Text><Code block>{payloadExample}</Code>
          <Text size="xs" c="dimmed" mt="xs">Headers: X-RepoMan-Event, X-RepoMan-Signature. Failed deliveries are retried twice.</Text></Card>
        <Card padding="md"><Text fw={600} size="sm" mb="xs">Verifying the signature</Text><Code block>{verifyExample}</Code></Card>
      </SimpleGrid>
      <Modal opened={!!editing} onClose={() => setEditing(null)} size="lg" title={<Text fw={600}>{editing === 'new' ? 'Add webhook' : 'Edit webhook'}</Text>}>
        {editing && <WebhookForm hook={editing === 'new' ? undefined : editing} onSaved={() => { setEditing(null); reload() }} />}
      </Modal>
    </>
  )
}

function WebhookForm({ hook, onSaved }: { hook?: Webhook; onSaved: () => void }) {
  const repos = useApi<Repo[]>('/repositories')
  const [h, setH] = useState({ name: hook?.name ?? '', url: hook?.url ?? '', secret: '', events: hook?.events ?? [], repos: hook?.repos ?? [], enabled: hook?.enabled ?? true })
  const [busy, setBusy] = useState(false)
  const save = () => {
    setBusy(true)
    const req = hook ? api(`/webhooks/${hook.id}`, { method: 'PUT', body: h }) : api('/webhooks', { body: h })
    req.then(() => { notifyOk('Webhook saved'); onSaved() }).catch(notifyError).finally(() => setBusy(false))
  }
  const genSecret = () => setH({ ...h, secret: Array.from(crypto.getRandomValues(new Uint8Array(24)), (b) => b.toString(16).padStart(2, '0')).join('') })
  return (
    <form onSubmit={(e) => { e.preventDefault(); save() }}>
      <Stack>
        <TextInput label="Name" required placeholder="e.g. Deploy pipeline" value={h.name} onChange={(e) => setH({ ...h, name: e.currentTarget.value })} />
        <TextInput label="Payload URL" required placeholder="https://ci.example.com/hooks/repoman" value={h.url} onChange={(e) => setH({ ...h, url: e.currentTarget.value })} />
        <Group align="flex-end" wrap="nowrap">
          <PasswordInput flex={1} label="Signing secret" placeholder={hook?.hasSecret ? 'Leave blank to keep the current secret' : 'Recommended'}
            description="Used to compute the X-RepoMan-Signature header." value={h.secret} onChange={(e) => setH({ ...h, secret: e.currentTarget.value })} visible={h.secret.length > 0 && h.secret.length === 48} />
          <Button variant="default" leftSection={<IconRefresh size={16} />} onClick={genSecret}>Generate</Button>
        </Group>
        <MultiSelect label="Events" placeholder={h.events.length ? '' : 'All events'} data={EVENT_GROUPS} value={h.events} onChange={(events) => setH({ ...h, events })} searchable clearable
          description="Leave empty to receive every event." />
        <MultiSelect label="Repositories" placeholder={h.repos.length ? '' : 'All repositories'} data={repos.data?.map((r) => r.name) ?? []} value={h.repos}
          onChange={(rs) => setH({ ...h, repos: rs })} searchable clearable description="Only events about these repositories. Leave empty for all." />
        <Switch label="Active" checked={h.enabled} onChange={(e) => setH({ ...h, enabled: e.currentTarget.checked })} />
        <Group justify="flex-end"><Button type="submit" loading={busy}>Save webhook</Button></Group>
      </Stack>
    </form>
  )
}

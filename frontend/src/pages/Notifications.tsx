import {
  ActionIcon, Alert, Badge, Button, Card, Code, Group, List, Modal, MultiSelect, PasswordInput, Radio, SimpleGrid, Slider, Stack, Switch, Table, Text, TextInput,
  ThemeIcon, Tooltip,
} from '@mantine/core'
import { IconBell, IconBellRinging, IconInfoCircle, IconPencil, IconPlus, IconSend, IconTrash } from '@tabler/icons-react'
import { useState } from 'react'
import { api, useApi, type Notifier, type Repo } from '../api'
import { EVENT_GROUPS, NOTIFY_PRESETS, presetOf } from '../events'
import { confirmAction, EmptyState, HelpLabel, LoadError, notifyError, notifyOk, PageHeader, timeAgo } from '../util'

const delivered = (status: string) => status.startsWith('2')

/** How Gotify's apps treat a priority, so people can pick one without reading its docs. */
const priorityHint = (p: number) => (p === 0 ? 'stored, no notification' : p <= 3 ? 'silent notification' : p <= 7 ? 'notification with sound' : 'urgent: sound and pop-up')
const marks = [{ value: 0, label: '0' }, { value: 4, label: '4' }, { value: 8, label: '8' }, { value: 10, label: '10' }]

const exampleMessage = `Upstream unreachable
docker-hub
upstream request failed; cached content is still served: dial tcp: i/o timeout`

export default function Notifications() {
  const { data, error, reload } = useApi<Notifier[]>('/notifiers')
  const [editing, setEditing] = useState<Notifier | 'new' | null>(null)
  const [testing, setTesting] = useState<number>()

  const test = (id: number) => {
    setTesting(id)
    return api<{ ok: boolean; status: string }>(`/notifiers/${id}/test`, { method: 'POST' })
      .then((r) => { if (r.ok) notifyOk('Check your Gotify app: the test message was accepted.', 'Test notification sent'); else notifyError(new Error(r.status)); reload() })
      .catch(notifyError).finally(() => setTesting(undefined))
  }
  const toggle = (n: Notifier) => api(`/notifiers/${n.id}`, { method: 'PUT', body: { ...n, token: '', enabled: !n.enabled } })
    .then(() => { notifyOk(n.enabled ? 'Channel paused' : 'Channel resumed'); reload() }).catch(notifyError)
  const remove = (n: Notifier) => confirmAction({
    title: `Delete channel ${n.name}?`, danger: true, label: 'Delete', body: 'No further notifications will be sent to this server.',
    onConfirm: () => api(`/notifiers/${n.id}`, { method: 'DELETE' }).then(() => { notifyOk('Channel deleted'); reload() }).catch(notifyError),
  })

  return (
    <>
      <PageHeader title="Notifications" description="Get a push message on your phone or desktop when something needs attention — an upstream goes down, a quota fills up, a release is published."
        action={<Button leftSection={<IconPlus size={16} />} onClick={() => setEditing('new')}>Add channel</Button>} />
      <LoadError error={error} retry={reload} />
      <Card padding={0} mb="lg">
        {data?.length === 0 ? (
          <EmptyState icon={<IconBell />} title="No notification channels yet" action={<Button onClick={() => setEditing('new')}>Connect a Gotify server</Button>}>
            RepoMan sends messages to your own Gotify server, so nothing leaves your network. You choose which events are worth a notification.
          </EmptyState>
        ) : (
          <Table.ScrollContainer minWidth={820}>
            <Table verticalSpacing="sm" horizontalSpacing="lg">
              <Table.Thead><Table.Tr><Table.Th>Channel</Table.Th><Table.Th>Sends</Table.Th><Table.Th>Repositories</Table.Th><Table.Th>Last delivery</Table.Th><Table.Th>Active</Table.Th><Table.Th /></Table.Tr></Table.Thead>
              <Table.Tbody>
                {data?.map((n) => {
                  const preset = NOTIFY_PRESETS.find((p) => p.value === presetOf(n.events))
                  return (
                    <Table.Tr key={n.id}>
                      <Table.Td>
                        <Group gap="xs"><Text size="sm" fw={500}>{n.name}</Text><Badge size="xs" variant="light">Gotify</Badge>
                          {!n.hasToken && <Tooltip label="Imported without its token. Edit the channel and enter the application token."><Badge size="xs" color="orange">token missing</Badge></Tooltip>}
                        </Group>
                        <Text size="xs" c="dimmed" ff="monospace" truncate="end" maw={300}>{n.url}</Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm">{preset ? preset.label : `${n.events.length} selected events`}</Text>
                        <Text size="xs" c="dimmed">priority {n.priority}, problems {n.alertPriority}</Text>
                      </Table.Td>
                      <Table.Td><Text size="sm">{n.repos.length ? n.repos.join(', ') : 'All'}</Text></Table.Td>
                      <Table.Td>
                        {n.lastDelivery ? <>
                          <Tooltip label={n.lastStatus} multiline maw={360} disabled={delivered(n.lastStatus)}>
                            <Badge variant="light" color={delivered(n.lastStatus) ? 'teal' : 'red'} radius="sm">{delivered(n.lastStatus) ? 'Delivered' : 'Failed'}</Badge>
                          </Tooltip>
                          <Text size="xs" c="dimmed">{timeAgo(n.lastDelivery)}</Text>
                        </> : <Text size="sm" c="dimmed">Never</Text>}
                      </Table.Td>
                      <Table.Td><Switch checked={n.enabled} onChange={() => toggle(n)} aria-label={`${n.enabled ? 'Pause' : 'Resume'} ${n.name}`} /></Table.Td>
                      <Table.Td>
                        <Group gap={4} justify="flex-end" wrap="nowrap">
                          <Tooltip label="Send a test notification"><ActionIcon variant="subtle" loading={testing === n.id} onClick={() => test(n.id)} aria-label={`Send a test notification to ${n.name}`}><IconSend size={16} /></ActionIcon></Tooltip>
                          <Tooltip label="Edit"><ActionIcon variant="subtle" color="gray" onClick={() => setEditing(n)} aria-label={`Edit ${n.name}`}><IconPencil size={16} /></ActionIcon></Tooltip>
                          <Tooltip label="Delete"><ActionIcon variant="subtle" color="red" onClick={() => remove(n)} aria-label={`Delete ${n.name}`}><IconTrash size={16} /></ActionIcon></Tooltip>
                        </Group>
                      </Table.Td>
                    </Table.Tr>
                  )
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Card>

      <SimpleGrid cols={{ base: 1, md: 2 }}>
        <Card padding="lg">
          <Group gap="sm" mb="sm"><ThemeIcon variant="light"><IconBellRinging size={18} /></ThemeIcon><Text fw={600}>Connect Gotify in three steps</Text></Group>
          <List type="ordered" size="sm" spacing="xs" pr="xl">
            <List.Item>In Gotify, open <b>Apps</b>, choose <b>Create application</b> (name it “RepoMan”) and copy its token.</List.Item>
            <List.Item>Here, choose <b>Add channel</b>, paste the server address and the token, and pick what you want to hear about.</List.Item>
            <List.Item>Save. RepoMan sends a test message straight away so you know it works.</List.Item>
          </List>
          <Text size="xs" c="dimmed" mt="sm">RepoMan must be able to reach the Gotify server over the network. Messages are retried twice, then dropped; the audit log always has the full history.</Text>
        </Card>
        <Card padding="lg">
          <Text fw={600} mb="xs">What a message looks like</Text>
          <Code block>{exampleMessage}</Code>
          <Text size="xs" c="dimmed" mt="xs">Title, what it is about, and who did it. With a base URL set under Settings, tapping the notification opens the repository.</Text>
        </Card>
      </SimpleGrid>

      <Modal opened={!!editing} onClose={() => setEditing(null)} size="lg" title={<Text fw={600}>{editing === 'new' ? 'Add notification channel' : 'Edit notification channel'}</Text>}>
        {editing && <ChannelForm channel={editing === 'new' ? undefined : editing}
          onSaved={(newId) => { setEditing(null); if (newId) test(newId); else reload() }} />}
      </Modal>
    </>
  )
}

function ChannelForm({ channel, onSaved }: { channel?: Notifier; onSaved: (newId?: number) => void }) {
  const repos = useApi<Repo[]>('/repositories')
  const [n, setN] = useState({
    name: channel?.name ?? '', kind: 'gotify', url: channel?.url ?? '', token: '', priority: channel?.priority ?? 4, alertPriority: channel?.alertPriority ?? 8,
    events: channel?.events ?? [...NOTIFY_PRESETS[0].events], repos: channel?.repos ?? [], enabled: channel?.enabled ?? true,
  })
  const [preset, setPreset] = useState(() => (channel ? presetOf(channel.events) : 'problems'))
  const [busy, setBusy] = useState(false)
  const urlError = n.url && !/^https?:\/\/[^/\s]+/i.test(n.url) ? 'Start with http:// or https://' : n.url.replace(/\/+$/, '').endsWith('/message') ? 'Use the server address only, without /message' : undefined

  const choose = (value: string) => {
    setPreset(value)
    const p = NOTIFY_PRESETS.find((x) => x.value === value)
    if (p) setN({ ...n, events: [...p.events] })
  }
  const save = () => {
    setBusy(true)
    const req = channel ? api<void>(`/notifiers/${channel.id}`, { method: 'PUT', body: n }).then(() => undefined) : api<{ id: number }>('/notifiers', { body: n }).then((r) => r.id)
    req.then((id) => { notifyOk('Channel saved'); onSaved(id) }).catch(notifyError).finally(() => setBusy(false))
  }

  return (
    <form onSubmit={(e) => { e.preventDefault(); save() }}>
      <Stack>
        <TextInput label="Name" required maxLength={100} placeholder="e.g. Ops phone" data-autofocus value={n.name} onChange={(e) => setN({ ...n, name: e.currentTarget.value })} />
        <TextInput label="Gotify server URL" required placeholder="https://gotify.example.com" description="The address you open Gotify at. A sub-path such as https://example.com/gotify works too."
          value={n.url} error={urlError} onChange={(e) => setN({ ...n, url: e.currentTarget.value })} />
        <PasswordInput label={<HelpLabel label="Application token" help="In Gotify: Apps → Create application → copy the token. This is not your Gotify password and not a client token." />}
          required={!channel?.hasToken} placeholder={channel?.hasToken ? 'Leave blank to keep the current token' : 'A…'} autoComplete="off"
          value={n.token} onChange={(e) => setN({ ...n, token: e.currentTarget.value })} />

        <Radio.Group label="What to send" value={preset} onChange={choose}>
          <Stack gap="xs" mt="xs">
            {NOTIFY_PRESETS.map((p) => <Radio key={p.value} value={p.value} label={p.label} description={p.description} />)}
            <Radio value="custom" label="Custom" description="Pick individual events." />
          </Stack>
        </Radio.Group>
        {preset === 'custom' && (
          <MultiSelect label="Events" data={EVENT_GROUPS} value={n.events} onChange={(events) => setN({ ...n, events })} searchable clearable
            placeholder={n.events.length ? '' : 'Choose at least one event'} description="An empty selection sends every event." />
        )}
        <MultiSelect label="Repositories" placeholder={n.repos.length ? '' : 'All repositories'} data={repos.data?.map((r) => r.name) ?? []} value={n.repos}
          onChange={(rs) => setN({ ...n, repos: rs })} searchable clearable
          description="Limit to events about these repositories. Server-wide events (sign-ins, tasks) are then not sent." />

        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xl" pb="md">
          <div>
            <Text size="sm" fw={500}>Priority for routine events</Text>
            <Text size="xs" c="dimmed" mb={6}>{n.priority}: {priorityHint(n.priority)}</Text>
            <Slider min={0} max={10} step={1} marks={marks} value={n.priority} onChange={(v) => setN({ ...n, priority: v })} aria-label="Priority for routine events" />
          </div>
          <div>
            <Text size="sm" fw={500}>Priority for problems</Text>
            <Text size="xs" c="dimmed" mb={6}>{n.alertPriority}: {priorityHint(n.alertPriority)}</Text>
            <Slider min={0} max={10} step={1} marks={marks} color="orange" value={n.alertPriority} onChange={(v) => setN({ ...n, alertPriority: v })} aria-label="Priority for problems" />
          </div>
        </SimpleGrid>
        <Alert variant="light" icon={<IconInfoCircle />} py="xs">
          Problems are: upstream unreachable, quota reached, task failed, failed and blocked sign-ins.
        </Alert>
        <Switch label="Active" description="Paused channels keep their settings but send nothing." checked={n.enabled} onChange={(e) => setN({ ...n, enabled: e.currentTarget.checked })} />
        <Group justify="flex-end">
          <Button type="submit" loading={busy} disabled={!!urlError}>{channel ? 'Save channel' : 'Save and send test'}</Button>
        </Group>
      </Stack>
    </form>
  )
}

import {
  ActionIcon, Avatar, Badge, Button, Card, Group, Modal, PasswordInput, Radio, SimpleGrid, Skeleton, Stack, Switch, Table, Text, TextInput, Tooltip,
} from '@mantine/core'
import { IconPencil, IconPlus, IconSearch, IconTrash, IconUserOff } from '@tabler/icons-react'
import { useState } from 'react'
import { useAuth } from '../App'
import { api, useApi, type Role, type User } from '../api'
import { confirmAction, EmptyState, LoadError, notifyError, notifyOk, PageHeader, Pager, plural, usePaging, useValidation, When } from '../util'

const roles: { value: Role; label: string; description: string; color: string }[] = [
  { value: 'viewer', label: 'Viewer', description: 'Browse, search and download from repositories they can read.', color: 'gray' },
  { value: 'developer', label: 'Developer', description: 'Everything a viewer can do, plus publish and delete artifacts.', color: 'blue' },
  { value: 'admin', label: 'Admin', description: 'Full control: repositories, users, settings, webhooks and tasks.', color: 'brand' },
]
const roleColor = Object.fromEntries(roles.map((r) => [r.value, r.color])) as Record<Role, string>

export default function Users() {
  const { user: me } = useAuth()
  const { data, error, reload } = useApi<User[]>('/users')
  const [editing, setEditing] = useState<User | 'new' | null>(null)
  const [q, setQ] = useState('')
  const paging = usePaging()

  const remove = (u: User) => confirmAction({
    title: `Delete user ${u.username}?`, danger: true, label: 'Delete user',
    body: 'Their sessions and API tokens stop working immediately. Artifacts they published are kept.',
    onConfirm: () => api(`/users/${encodeURIComponent(u.username)}`, { method: 'DELETE' }).then(() => { notifyOk('User deleted'); reload() }).catch(notifyError),
  })
  const ql = q.trim().toLowerCase()
  const rows = data?.filter((u) => u.username.toLowerCase().includes(ql) || u.email.toLowerCase().includes(ql))
  const shown = rows?.slice(paging.offset, paging.offset + paging.size)

  return (
    <>
      <PageHeader title="Users & roles" description="Roles set what someone can do everywhere. Fine-tune access per repository on each repository's Access tab."
        action={<Button leftSection={<IconPlus size={16} />} onClick={() => setEditing('new')}>Add user</Button>} />
      <LoadError error={error} retry={reload} />
      <SimpleGrid cols={{ base: 1, sm: 3 }} mb="lg">
        {roles.map((r) => (
          <Card key={r.value} padding="md">
            <Group justify="space-between"><Badge variant="light" color={r.color}>{r.label}</Badge>
              <Text size="sm" c="dimmed">{plural(data?.filter((u) => u.role === r.value).length ?? 0, 'user')}</Text></Group>
            <Text size="sm" c="dimmed" mt="xs">{r.description}</Text>
          </Card>
        ))}
      </SimpleGrid>
      <Card padding={0}>
        <Group p="md" justify="flex-end">
          <TextInput size="xs" placeholder="Filter by name or email" aria-label="Filter users" leftSection={<IconSearch size={14} />} value={q} onChange={(e) => { setQ(e.currentTarget.value); paging.setPage(1, true) }} />
        </Group>
        <Table.ScrollContainer minWidth={420}>
          <Table verticalSpacing="sm" horizontalSpacing="md" highlightOnHover>
            <Table.Thead><Table.Tr><Table.Th>User</Table.Th><Table.Th>Role</Table.Th><Table.Th visibleFrom="sm">Status</Table.Th><Table.Th visibleFrom="md">Last sign-in</Table.Th><Table.Th visibleFrom="md">Created</Table.Th><Table.Th /></Table.Tr></Table.Thead>
            <Table.Tbody>
              {!data && !error && Array.from({ length: 3 }, (_, i) => <Table.Tr key={i}><Table.Td colSpan={6}><Skeleton h={36} /></Table.Td></Table.Tr>)}
              {shown?.map((u) => (
                <Table.Tr key={u.username} opacity={u.disabled ? 0.6 : 1}>
                  <Table.Td>
                    <Group gap="sm" wrap="nowrap">
                      <Avatar radius="xl" color={roleColor[u.role]} aria-hidden>{u.username[0].toUpperCase()}</Avatar>
                      <div>
                        <Text size="sm" fw={500}>{u.username}{u.username === me.username && <Text span c="dimmed" size="xs"> (you)</Text>}</Text>
                        <Text size="xs" c="dimmed" style={{ wordBreak: 'break-all' }}>{u.email || 'no email'}{u.disabled && <Text span hiddenFrom="sm"> · disabled</Text>}</Text>
                      </div>
                    </Group>
                  </Table.Td>
                  <Table.Td><Badge variant="light" color={roleColor[u.role]}>{u.role}</Badge></Table.Td>
                  <Table.Td visibleFrom="sm">{u.disabled ? <Badge color="gray" variant="outline">Disabled</Badge> : <Badge color="teal" variant="dot">Active</Badge>}</Table.Td>
                  <Table.Td visibleFrom="md"><When unix={u.lastLogin} /></Table.Td>
                  <Table.Td visibleFrom="md"><When unix={u.createdAt} never="—" /></Table.Td>
                  <Table.Td>
                    <Group gap={4} justify="flex-end" wrap="nowrap">
                      <Tooltip label="Edit"><ActionIcon variant="subtle" color="gray" onClick={() => setEditing(u)} aria-label={`Edit ${u.username}`}><IconPencil size={16} /></ActionIcon></Tooltip>
                      {u.username !== me.username && (
                        <Tooltip label="Delete"><ActionIcon variant="subtle" color="red" onClick={() => remove(u)} aria-label={`Delete ${u.username}`}><IconTrash size={16} /></ActionIcon></Tooltip>
                      )}
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        <Pager paging={paging} total={rows?.length} noun="users" />
        {rows?.length === 0 && <EmptyState icon={<IconUserOff />} title="No users match your filter"
          action={<Button variant="light" onClick={() => setQ('')}>Clear filter</Button>}>Try a different name or email.</EmptyState>}
      </Card>
      <Modal opened={!!editing} onClose={() => setEditing(null)} title={<Text fw={600}>{editing === 'new' ? 'Add user' : `Edit ${editing?.username ?? ''}`}</Text>} size="lg">
        {editing && <UserForm user={editing === 'new' ? undefined : editing} self={editing !== 'new' && editing.username === me.username} onSaved={() => { setEditing(null); reload() }} />}
      </Modal>
    </>
  )
}

function UserForm({ user, self, onSaved }: { user?: User; self: boolean; onSaved: () => void }) {
  const [f, setF] = useState({ username: user?.username ?? '', email: user?.email ?? '', role: user?.role ?? 'developer', password: '', disabled: user?.disabled ?? false })
  const [busy, setBusy] = useState(false)
  const v = useValidation({
    username: !user && (!f.username ? 'Choose a username' : !/^[a-zA-Z0-9][a-zA-Z0-9._@-]{1,63}$/.test(f.username) && 'Use 2–64 letters, digits, ".", "_", "@" or "-", starting with a letter or digit'),
    email: !!f.email && !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(f.email) && 'This does not look like an email address',
    password: (!user || f.password !== '') && (f.password.length < 8 ? 'Use at least 8 characters' : new TextEncoder().encode(f.password).length > 72 && 'Use at most 72 bytes'),
  })
  const save = () => {
    if (!v.check()) return
    setBusy(true)
    const req = user ? api(`/users/${encodeURIComponent(user.username)}`, { method: 'PUT', body: f }) : api('/users', { body: f })
    req.then(() => { notifyOk(user ? 'User updated' : `User ${f.username} created`); onSaved() }).catch(notifyError).finally(() => setBusy(false))
  }
  return (
    <form noValidate onSubmit={(e) => { e.preventDefault(); save() }}>
      <Stack>
        <Group grow align="flex-start">
          <TextInput label="Username" withAsterisk={!user} disabled={!!user} data-autofocus={!user || undefined} value={f.username} onChange={(e) => setF({ ...f, username: e.currentTarget.value.trim() })}
            description={!user ? 'Letters, digits, ".", "_", "@" or "-"' : undefined} error={v.error('username')} autoComplete="off" autoCapitalize="none" spellCheck={false} />
          <TextInput label="Email" inputMode="email" description="Optional" value={f.email} onChange={(e) => setF({ ...f, email: e.currentTarget.value.trim() })} error={v.error('email')} autoComplete="off" />
        </Group>
        <Radio.Group label="Role" value={f.role} onChange={(v) => setF({ ...f, role: v as Role })}>
          <Stack gap="xs" mt="xs">
            {roles.map((r) => (
              <Radio.Card key={r.value} value={r.value} p="sm" radius="md" disabled={self && r.value !== 'admin'}>
                <Group wrap="nowrap" align="flex-start">
                  <Radio.Indicator />
                  <div><Text size="sm" fw={600}>{r.label}</Text><Text size="xs" c="dimmed">{r.description}</Text></div>
                </Group>
              </Radio.Card>
            ))}
          </Stack>
        </Radio.Group>
        <PasswordInput label={user ? 'Set a new password' : 'Password'} withAsterisk={!user} autoComplete="new-password"
          description={user ? 'Leave blank to keep the current password. Setting one signs the user out everywhere.' : 'At least 8 characters. Share it securely; they can change it under Account.'}
          value={f.password} onChange={(e) => setF({ ...f, password: e.currentTarget.value })} error={v.error('password')} />
        {user && !self && (
          <Switch label="Disable account" description="Blocks sign-in and all API tokens without deleting the user." checked={f.disabled}
            onChange={(e) => setF({ ...f, disabled: e.currentTarget.checked })} />
        )}
        <Group justify="flex-end"><Button type="submit" loading={busy}>{user ? 'Save changes' : 'Create user'}</Button></Group>
      </Stack>
    </form>
  )
}

import { ActionIcon, Alert, Badge, Button, Card, Code, Group, Modal, PasswordInput, Radio, SegmentedControl, SimpleGrid, Stack, Table, Text, TextInput, Tooltip } from '@mantine/core'
import { modals } from '@mantine/modals'
import { IconAlertTriangle, IconLink, IconLinkOff, IconPlus, IconTrash } from '@tabler/icons-react'
import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useAuth, useBaseUrl } from '../App'
import { api, useApi, type OidcIdentity, type OidcPublic, type Token, type TokenScope } from '../api'
import { Copy, fullDate, notifyError, notifyOk, PageHeader, timeAgo, useValidation, When } from '../util'

export default function Account() {
  const { user } = useAuth()
  return (
    <>
      <PageHeader title="Account & tokens" description={`Signed in as ${user.username} (${user.role})`} />
      <SimpleGrid cols={{ base: 1, lg: 2 }}>
        <Tokens />
        <Stack>
          <SingleSignOn />
          <ChangePassword />
        </Stack>
      </SimpleGrid>
    </>
  )
}

const scopes: { value: TokenScope; label: string; color: string; description: string }[] = [
  { value: 'read', label: 'Read-only', color: 'gray', description: 'Browse, search and download. For build machines and servers that only install packages.' },
  { value: 'write', label: 'Read & publish', color: 'blue', description: 'Also publish and delete artifacts. For CI pipelines that release packages or images.' },
  { value: 'full', label: 'Full access', color: 'orange', description: 'Everything your account can do, including the management API. Only for trusted automation.' },
]
const scopeInfo = (s: TokenScope) => scopes.find((x) => x.value === s) ?? scopes[2]

function Tokens() {
  const { user } = useAuth()
  const base = useBaseUrl()
  const [scope, setScope] = useState<TokenScope>('write')
  const { data, reload } = useApi<Token[]>('/tokens')
  const [params, setParams] = useSearchParams()
  const [open, setOpen] = useState(params.has('new'))
  const [name, setName] = useState('')
  const [days, setDays] = useState<number>(90)
  const [created, setCreated] = useState<string>()
  const [busy, setBusy] = useState(false)
  const v = useValidation({ name: !name.trim() && 'Give the token a name, so you can tell later what uses it' })

  const close = () => { setOpen(false); v.reset(); if (params.has('new')) setParams({}, { replace: true }) }
  const create = () => {
    if (!v.check()) return
    setBusy(true)
    api<{ token: string }>('/tokens', { body: { name: name.trim(), scope, expiresDays: days } })
      .then((r) => { setCreated(r.token); setName(''); v.reset(); reload() })
      .catch(notifyError).finally(() => setBusy(false))
  }

  const revoke = (t: Token) => modals.openConfirmModal({
    title: `Revoke "${t.name}"?`,
    children: <Text size="sm">Clients using this token will lose access immediately.</Text>,
    labels: { confirm: 'Revoke', cancel: 'Cancel' },
    confirmProps: { color: 'red' },
    onConfirm: () => api(`/tokens/${t.id}`, { method: 'DELETE' }).then(() => { notifyOk('Token revoked'); reload() }).catch(notifyError),
  })

  return (
    <Card padding="lg">
      <Group justify="space-between" mb="xs" wrap="nowrap" align="flex-start">
        <div>
          <Text fw={600}>API tokens</Text>
          <Text size="sm" c="dimmed">Use as the password for Maven, npm, pip, twine, Docker and curl, or as a bearer token for the REST API.</Text>
        </div>
        <Button size="xs" style={{ flexShrink: 0 }} leftSection={<IconPlus size={14} />} onClick={() => { setCreated(undefined); setOpen(true) }}>New token</Button>
      </Group>
      {!!data?.length && <Table.ScrollContainer minWidth={520}>
      <Table verticalSpacing="xs">
        <Table.Thead><Table.Tr><Table.Th>Name</Table.Th><Table.Th>Access</Table.Th><Table.Th>Last used</Table.Th><Table.Th>Expires</Table.Th><Table.Th /></Table.Tr></Table.Thead>
        <Table.Tbody>
          {data?.map((t) => (
            <Table.Tr key={t.id}>
              <Table.Td><Text size="sm" fw={500}>{t.name}</Text><Text size="xs" c="dimmed">created {timeAgo(t.createdAt)}</Text></Table.Td>
              <Table.Td><Tooltip label={scopeInfo(t.scope).description} multiline maw={300}><Badge variant="light" color={scopeInfo(t.scope).color}>{scopeInfo(t.scope).label}</Badge></Tooltip></Table.Td>
              <Table.Td><When unix={t.lastUsed} /></Table.Td>
              <Table.Td>{t.expiresAt && t.expiresAt * 1000 < Date.now()
                ? <Badge variant="light" color="red">expired</Badge>
                : <When unix={t.expiresAt} />}</Table.Td>
              <Table.Td><Tooltip label="Revoke"><ActionIcon variant="subtle" color="red" onClick={() => revoke(t)} aria-label={`Revoke ${t.name}`}><IconTrash size={16} /></ActionIcon></Tooltip></Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      </Table.ScrollContainer>}
      {data?.length === 0 && <Text size="sm" c="dimmed" ta="center" py="md">No tokens yet. Create one per machine or CI pipeline instead of using your password.</Text>}

      <Modal opened={open} onClose={close} closeOnClickOutside={!created} title={<Text fw={600}>{created ? 'Your new API token' : 'New API token'}</Text>}>
        {created ? (
          <Stack>
            <Alert color="yellow" icon={<IconAlertTriangle />}>Copy this token now — it won't be shown again.</Alert>
            <Group wrap="nowrap" gap="xs"><Code style={{ wordBreak: 'break-all' }} flex={1}>{created}</Code><Copy value={created} label="Copy token" /></Group>
            <div>
              <Text size="sm" fw={500} mb={4}>Try it</Text>
              <Code block>{`curl -H "Authorization: Bearer ${created}" ${base}/api/v1/repositories`}</Code>
              <Text size="xs" c="dimmed" mt={4}>In package clients, use <b>{user.username}</b> as the username and the token as the password. Each repository's Setup tab has ready-made snippets.</Text>
            </div>
            <Button onClick={close}>Done</Button>
          </Stack>
        ) : (
          <form noValidate onSubmit={(e) => { e.preventDefault(); create() }}>
            <Stack>
              <TextInput label="Name" placeholder="e.g. CI pipeline, laptop" withAsterisk maxLength={100} data-autofocus value={name} onChange={(e) => setName(e.currentTarget.value)} error={v.error('name')} />
              <Radio.Group label="Access" value={scope} onChange={(v) => setScope(v as TokenScope)}>
                <Stack gap="xs" mt="xs">
                  {scopes.map((s) => <Radio key={s.value} value={s.value} label={s.label} description={s.description} />)}
                </Stack>
              </Radio.Group>
              <Text size="xs" c="dimmed" mt={-8}>A token can never do more than your own role ({user.role}) allows.</Text>
              <div>
                <Text size="sm" fw={500} mb={4}>Expires</Text>
                <SegmentedControl fullWidth value={String(days)} onChange={(v) => setDays(Number(v))}
                  data={[{ value: '30', label: '30 days' }, { value: '90', label: '90 days' }, { value: '365', label: '1 year' }, { value: '0', label: 'Never' }]} />
                <Text size="xs" c="dimmed" mt={4}>Short-lived tokens limit the damage if one leaks. Use one token per machine or pipeline.</Text>
              </div>
              <Group justify="flex-end"><Button variant="default" onClick={close}>Cancel</Button><Button type="submit" loading={busy}>Create token</Button></Group>
            </Stack>
          </form>
        )}
      </Modal>
    </Card>
  )
}

function SingleSignOn() {
  const cfg = useApi<OidcPublic>('/auth/oidc/config')
  const id = useApi<OidcIdentity>('/auth/oidc/identity')
  const [params, setParams] = useSearchParams()
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (params.get('oidc') === 'linked') { notifyOk('SSO account linked'); setParams({}, { replace: true }); id.reload() }
  }, [params]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!cfg.data?.enabled || !id.data) return null
  const { linked, email, linkedAt, hasPassword } = id.data

  const link = () => {
    setBusy(true)
    api<{ url: string }>('/auth/oidc/link', { method: 'POST', body: {} }).then((r) => { window.location.href = r.url }).catch((e) => { notifyError(e); setBusy(false) })
  }
  const unlink = () => modals.openConfirmModal({
    title: 'Unlink SSO account?',
    children: <Text size="sm">You will sign in with your RepoMan username and password only. You can link again later.</Text>,
    labels: { confirm: 'Unlink', cancel: 'Cancel' },
    confirmProps: { color: 'red' },
    onConfirm: () => api('/auth/oidc/link', { method: 'DELETE' }).then(() => { notifyOk('SSO account unlinked'); id.reload() }).catch(notifyError),
  })
  return (
    <Card padding="lg">
      <Group justify="space-between" wrap="nowrap" align="flex-start">
        <div>
          <Text fw={600}>Single sign-on</Text>
          {linked ? (
            <Text size="sm" c="dimmed">Linked to {cfg.data.label}{email ? ` as ${email}` : ''}{linkedAt ? ` · since ${fullDate(linkedAt)}` : ''}.</Text>
          ) : cfg.data.mergeEnabled ? (
            <Text size="sm" c="dimmed">Link your {cfg.data.label} identity to this account to sign in with one click. Your role, permissions and tokens stay the same.</Text>
          ) : (
            <Text size="sm" c="dimmed">Linking an SSO identity to existing accounts is turned off by your administrator.</Text>
          )}
          {linked && !hasPassword && <Text size="xs" c="orange" mt={4}>Set a password below before you can unlink; otherwise you would lose access.</Text>}
        </div>
        {linked
          ? <Button size="xs" variant="default" color="red" leftSection={<IconLinkOff size={14} />} disabled={!hasPassword} onClick={unlink}>Unlink</Button>
          : cfg.data.mergeEnabled && <Button size="xs" leftSection={<IconLink size={14} />} loading={busy} onClick={link}>Link {cfg.data.label}</Button>}
      </Group>
    </Card>
  )
}

function ChangePassword() {
  const id = useApi<OidcIdentity>('/auth/oidc/identity')
  const needsCurrent = id.data?.hasPassword !== false
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const mismatch = confirm !== '' && confirm !== next
  const v = useValidation({
    current: needsCurrent && !current && 'Enter your current password',
    next: next.length < 8 ? 'Use at least 8 characters' : new TextEncoder().encode(next).length > 72 ? 'Use at most 72 bytes' : needsCurrent && next === current && 'Choose a password different from the current one',
    confirm: confirm !== next && 'Passwords do not match',
  })

  const save = () => {
    if (!v.check()) return
    setBusy(true)
    api('/auth/password', { method: 'PUT', body: { current, new: next } })
      .then(() => { notifyOk('Other browsers signed in to this account were signed out.', 'Password changed'); setCurrent(''); setNext(''); setConfirm(''); v.reset(); id.reload() })
      .catch(notifyError)
      .finally(() => setBusy(false))
  }
  return (
    <Card padding="lg">
      <Text fw={600} mb="sm">{needsCurrent ? 'Change password' : 'Set a password'}</Text>
      {!needsCurrent && <Text size="sm" c="dimmed" mb="sm">Your account was created through single sign-on and has no password yet. Set one to use package clients with a password or to unlink SSO.</Text>}
      <form noValidate onSubmit={(e) => { e.preventDefault(); save() }}>
        <Stack>
          {needsCurrent && <PasswordInput label="Current password" withAsterisk value={current} onChange={(e) => setCurrent(e.currentTarget.value)} autoComplete="current-password" error={v.error('current')} />}
          <PasswordInput label="New password" withAsterisk description="At least 8 characters" value={next} onChange={(e) => setNext(e.currentTarget.value)} autoComplete="new-password" error={v.error('next')} />
          <PasswordInput label="Confirm new password" withAsterisk value={confirm} error={(mismatch && 'Passwords do not match') || v.error('confirm')} onChange={(e) => setConfirm(e.currentTarget.value)} autoComplete="new-password" />
          <Group justify="flex-end"><Button type="submit" loading={busy}>Update password</Button></Group>
        </Stack>
      </form>
    </Card>
  )
}

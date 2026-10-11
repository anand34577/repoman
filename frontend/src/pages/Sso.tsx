import { Alert, Button, Code, Group, PasswordInput, Select, Stack, Switch, Text, TextInput } from '@mantine/core'
import { IconCircleCheck, IconInfoCircle, IconPlugConnected } from '@tabler/icons-react'
import { useEffect, useState } from 'react'
import { useBaseUrl } from '../App'
import { api, useApi, type OidcConfig } from '../api'
import { Copy, HelpLabel, LoadError, notifyError, notifyOk, Section, useUnsavedGuard } from '../util'
import { FormSkeleton } from './Settings'

export default function Sso() {
  const { data, error, reload } = useApi<OidcConfig>('/settings/oidc')
  const base = useBaseUrl()
  const [c, setC] = useState<OidcConfig>()
  const [busy, setBusy] = useState(false)
  const [test, setTest] = useState<{ ok: boolean; message: string }>()
  const [testing, setTesting] = useState(false)
  useEffect(() => setC(data), [data])
  const dirty = !!c && JSON.stringify(c) !== JSON.stringify(data)
  useUnsavedGuard(dirty)
  if (!c) return error ? <LoadError error={error} retry={reload} /> : <FormSkeleton />
  const set = (p: Partial<OidcConfig>) => { setC({ ...c, ...p }); setTest(undefined) }
  const redirect = `${base}/api/auth/oidc/callback`

  const save = () => {
    setBusy(true)
    api<OidcConfig>('/settings/oidc', { method: 'PUT', body: c })
      .then(() => { notifyOk('Single sign-on settings saved'); reload() }).catch(notifyError).finally(() => setBusy(false))
  }
  const check = () => {
    setTesting(true)
    api<{ ok: boolean; message: string }>('/settings/oidc/test', { body: { issuer: c.issuer } }).then(setTest).catch(notifyError).finally(() => setTesting(false))
  }

  return (
    <Stack maw={760}>
      <Section title="OpenID Connect" description="Let people sign in with Keycloak, Microsoft Entra ID, Okta, Authentik, Google or any OpenID Connect provider. Password sign-in keeps working.">
        <Switch label="Enable single sign-on" checked={c.enabled} onChange={(e) => set({ enabled: e.currentTarget.checked })} />
        <TextInput label={<HelpLabel label="Issuer URL" help="The provider's base URL; RepoMan appends /.well-known/openid-configuration. Keycloak: https://host/realms/<realm>. Entra ID: https://login.microsoftonline.com/<tenant>/v2.0" />}
          placeholder="https://keycloak.example.com/realms/corp" value={c.issuer} onChange={(e) => set({ issuer: e.currentTarget.value })} />
        <Group grow align="flex-start">
          <TextInput label="Client ID" value={c.clientId} onChange={(e) => set({ clientId: e.currentTarget.value })} />
          <PasswordInput label="Client secret" placeholder={c.hasSecret ? '•••••••• (unchanged)' : 'Leave empty for a public client'} value={c.clientSecret ?? ''}
            onChange={(e) => set({ clientSecret: e.currentTarget.value })} autoComplete="new-password" />
        </Group>
        <Group gap="xs">
          <Button variant="light" size="xs" leftSection={<IconPlugConnected size={14} />} disabled={!c.issuer} loading={testing} onClick={check}>Test connection</Button>
          {test && <Text size="sm" c={test.ok ? 'teal' : 'red'}>{test.ok && <IconCircleCheck size={14} style={{ verticalAlign: -2 }} />} {test.message}</Text>}
        </Group>
        <div>
          <Text size="sm" fw={500} mb={4}>Redirect URI to register at your provider</Text>
          <Group gap="xs" wrap="nowrap"><Code style={{ wordBreak: 'break-all' }}>{redirect}</Code><Copy value={redirect} /></Group>
          <Text size="xs" c="dimmed" mt={4}>Uses Settings → General → Base URL when set. Behind a reverse proxy, set the Base URL first. Authorization code flow with PKCE is used.</Text>
        </div>
      </Section>
      <Section title="Accounts">
        <Switch label="Allow existing users to merge their SSO account"
          description="Users can link their SSO identity to their existing RepoMan account: from Account → Single sign-on while signed in, or by entering their current password after their first SSO sign-in. Accounts are never merged automatically by name or e-mail."
          checked={c.mergeEnabled} onChange={(e) => set({ mergeEnabled: e.currentTarget.checked })} />
        <Switch label="Create accounts for new SSO users" description="Without this (and without merging) only identities that are already linked can sign in."
          checked={c.autoCreate} onChange={(e) => set({ autoCreate: e.currentTarget.checked })} />
        <Select label="Role for new SSO accounts" data={[{ value: 'viewer', label: 'Viewer' }, { value: 'developer', label: 'Developer' }, { value: 'admin', label: 'Admin' }]}
          value={c.defaultRole} onChange={(v) => v && set({ defaultRole: v as OidcConfig['defaultRole'] })} allowDeselect={false} disabled={!c.autoCreate} />
        {c.autoCreate && c.defaultRole === 'admin' && <Alert color="orange" variant="light" icon={<IconInfoCircle />}>Everyone who can authenticate at your provider would become an admin. Prefer Viewer and promote people under Users.</Alert>}
      </Section>
      <Section title="Appearance and claims">
        <Group grow>
          <TextInput label="Button label" value={c.buttonLabel} onChange={(e) => set({ buttonLabel: e.currentTarget.value })} />
          <TextInput label="Username claim" description="Used to name new accounts" value={c.usernameClaim} onChange={(e) => set({ usernameClaim: e.currentTarget.value })} />
        </Group>
        <TextInput label="Scopes" value={c.scopes} onChange={(e) => set({ scopes: e.currentTarget.value })} />
      </Section>
      <Group justify="flex-end" gap="sm">
        {dirty && <Text size="sm" c="dimmed">You have unsaved changes.</Text>}
        {dirty && <Button variant="default" onClick={() => { setC(data); setTest(undefined) }}>Discard</Button>}
        <Button onClick={save} loading={busy} disabled={!dirty}>Save single sign-on</Button>
      </Group>
    </Stack>
  )
}

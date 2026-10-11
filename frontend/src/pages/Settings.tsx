import {
  Alert, Button, Card, Code, Group, List, Modal, NumberInput, SimpleGrid, Skeleton, Stack, Switch, Table, Tabs, Text, TextInput, Textarea,
} from '@mantine/core'
import { Dropzone } from '@mantine/dropzone'
import { IconKey, IconDatabaseExport, IconDatabaseImport, IconDownload, IconInfoCircle, IconServer, IconSettings, IconShieldLock, IconX } from '@tabler/icons-react'
import { lazy, Suspense, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useAuth } from '../App'
import { api, useApi, type Settings } from '../api'
import { Copy, formatBytes, fullDate, HelpLabel, LoadError, notifyError, notifyOk, PageHeader, plural, Section, useUnsavedGuard } from '../util'

const Sso = lazy(() => import('./Sso'))

export default function SettingsPage() {
  const { '*': sub } = useParams()
  const navigate = useNavigate()
  return (
    <>
      <PageHeader title="Settings" description="Server-wide configuration, security, backups and system health." />
      <Tabs value={sub || 'general'} onChange={(t) => navigate(`/settings/${t === 'general' ? '' : t}`)} keepMounted={false}>
        <Tabs.List mb="md" className="tabs-scroll">
          <Tabs.Tab value="general" leftSection={<IconSettings size={16} />}>General</Tabs.Tab>
          <Tabs.Tab value="security" leftSection={<IconShieldLock size={16} />}>Security</Tabs.Tab>
          <Tabs.Tab value="sso" leftSection={<IconKey size={16} />}>Single sign-on</Tabs.Tab>
          <Tabs.Tab value="backup" leftSection={<IconDatabaseExport size={16} />}>Backup & restore</Tabs.Tab>
          <Tabs.Tab value="system" leftSection={<IconServer size={16} />}>System</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="general"><General /></Tabs.Panel>
        <Tabs.Panel value="security"><Security /></Tabs.Panel>
        <Tabs.Panel value="sso"><Suspense fallback={null}><Sso /></Suspense></Tabs.Panel>
        <Tabs.Panel value="backup"><Backup /></Tabs.Panel>
        <Tabs.Panel value="system"><System /></Tabs.Panel>
      </Tabs>
    </>
  )
}

function General() {
  const { reloadSettings } = useAuth()
  const { data, error, reload } = useApi<Settings>('/settings')
  const [s, setS] = useState<Settings>()
  const [busy, setBusy] = useState(false)
  useEffect(() => setS(data), [data])
  const dirty = !!s && JSON.stringify(s) !== JSON.stringify(data)
  useUnsavedGuard(dirty)
  if (!s) return error ? <LoadError error={error} retry={reload} /> : <FormSkeleton />
  const urlError = s.baseUrl.trim() && !/^https?:\/\/[^/\s]+/i.test(s.baseUrl.trim()) ? 'Enter a complete address such as https://repo.example.com' : undefined

  const save = () => {
    setBusy(true)
    api<Settings>('/settings', { method: 'PUT', body: s }).then(() => { notifyOk('Settings saved'); reloadSettings(); reload() }).catch(notifyError).finally(() => setBusy(false))
  }
  return (
    <Stack maw={760}>
      <Section title="Server URL" description="How clients reach RepoMan. Leave empty to use the address in each request.">
        <TextInput label={<HelpLabel label="Base URL" help="Set this when RepoMan runs behind a reverse proxy or load balancer, e.g. https://repo.example.com. It is used in npm tarball links and all setup instructions." />}
          placeholder={window.location.origin} inputMode="url" autoCapitalize="none" spellCheck={false} error={urlError}
          value={s.baseUrl} onChange={(e) => setS({ ...s, baseUrl: e.currentTarget.value })} />
      </Section>
      <Section title="Anonymous access">
        <Switch label="Allow anonymous read access" checked={s.anonymousAccess}
          description="Anyone who can reach the server can browse and download from repositories not marked private. Publishing always requires an account."
          onChange={(e) => setS({ ...s, anonymousAccess: e.currentTarget.checked })} />
      </Section>
      <Section title="Announcement banner" description="Shown at the top of every page — e.g. planned maintenance.">
        <Textarea placeholder="e.g. RepoMan will be upgraded Saturday 10:00–10:30 UTC." autosize minRows={2} value={s.banner} onChange={(e) => setS({ ...s, banner: e.currentTarget.value })} />
      </Section>
      <Section title="Audit log retention">
        <NumberInput label="Keep audit events for (days)" description="Older events are purged hourly. 0 keeps them forever." min={0} allowDecimal={false} allowNegative={false}
          value={s.auditRetentionDays} onChange={(v) => setS({ ...s, auditRetentionDays: Number(v) || 0 })} />
      </Section>
      <Card padding="sm" pos="sticky" bottom={16} shadow="md" style={{ zIndex: 2 }}>
        <Group justify="space-between">
          <Text size="sm" c="dimmed">{dirty ? 'You have unsaved changes.' : 'All changes saved.'}</Text>
          <Group gap="xs">
            {dirty && <Button variant="default" onClick={() => setS(data)}>Discard</Button>}
            <Button onClick={save} loading={busy} disabled={!dirty || !!urlError}>Save settings</Button>
          </Group>
        </Group>
      </Card>
    </Stack>
  )
}

/** Placeholder for a settings form while its values load. */
export function FormSkeleton() {
  return <Stack maw={760} aria-busy="true">{Array.from({ length: 3 }, (_, i) => <Skeleton key={i} h={132} radius="lg" />)}</Stack>
}

function Security() {
  const info = useApi<{ signingKeyFingerprint: string; tls: boolean; initialPasswordPending: boolean }>('/system/info')
  return (
    <Stack maw={760}>
      <Section title="Repository signing key" description="apt Release files and yum repomd.xml of hosted repositories are signed with this OpenPGP key. Clients import it once.">
        <Group gap="xs"><Text size="sm" c="dimmed">Fingerprint</Text><Code>{info.data?.signingKeyFingerprint ?? '…'}</Code>{info.data && <Copy value={info.data.signingKeyFingerprint} />}</Group>
        <Group><Button component="a" href="/api/signing-key" download="repoman-signing-key.asc" variant="light" leftSection={<IconDownload size={16} />}>Download public key</Button></Group>
        <Text size="xs" c="dimmed">The key is also served at /repository/&lt;apt-or-yum-repo&gt;/repository.key. Back up the data directory to keep it — losing it means clients must re-import a new key.</Text>
      </Section>
      <Section title="Built-in protections">
        <List size="sm" spacing="xs">
          <List.Item>Passwords are stored with bcrypt; API tokens are stored only as SHA-256 hashes and can be limited to read-only or publish-only.</List.Item>
          <List.Item>Sign-in is blocked for 15 minutes after 10 failed attempts from the same address, and the lockout is recorded as an event you can be notified about.</List.Item>
          <List.Item>Web sessions use HttpOnly, SameSite cookies, expire after 7 days and end when the password changes. Requests triggered by other websites are rejected.</List.Item>
          <List.Item>The interface loads nothing from the internet (enforced by a content security policy), and uploaded files are served sandboxed so they cannot run scripts here.</List.Item>
          <List.Item>Disabling a user immediately revokes their sessions and tokens.</List.Item>
          <List.Item>Every sign-in, change and deletion is recorded in the audit log.</List.Item>
        </List>
        {info.data && !info.data.tls && window.location.protocol !== 'https:' && (
          <Alert variant="light" color="orange" icon={<IconInfoCircle />} title="This connection is not encrypted">
            Passwords and tokens travel in clear text. Set RM_TLS_CERT and RM_TLS_KEY, or put RepoMan behind a TLS reverse proxy, before using it beyond this machine.
          </Alert>
        )}
        {info.data?.initialPasswordPending && (
          <Alert variant="light" color="orange" icon={<IconInfoCircle />} title="The first-run admin password is still in use">
            Change it under Account. The copy stored in the data directory is deleted when you do.
          </Alert>
        )}
      </Section>
    </Stack>
  )
}

interface ExportDoc {
  version?: string; exportedAt?: number; settings?: unknown
  repositories?: unknown[]; users?: unknown[]; permissions?: unknown[]; webhooks?: unknown[]; notifiers?: unknown[]
}

function Backup() {
  const { reloadSettings, reloadRepos } = useAuth()
  const [report, setReport] = useState<Record<string, string[]>>()
  const [busy, setBusy] = useState(false)
  const [pending, setPending] = useState<{ name: string; doc: ExportDoc }>()
  // nothing is applied when a file is chosen: its contents are shown first
  const choose = (file?: File) => {
    if (!file) return
    file.text().then((t) => {
      let doc: ExportDoc
      try { doc = JSON.parse(t) } catch { throw new Error(`${file.name} is not a JSON file. Choose a file made with “Download configuration”.`) }
      if (!doc || typeof doc !== 'object' || Array.isArray(doc) || !(doc.repositories || doc.settings || doc.users)) {
        throw new Error(`${file.name} is not a RepoMan configuration export.`)
      }
      setReport(undefined)
      setPending({ name: file.name, doc })
    }).catch(notifyError)
  }
  const apply = () => {
    if (!pending) return
    setBusy(true)
    api<Record<string, string[]>>('/system/import', { body: pending.doc })
      .then((r) => { setReport(r); setPending(undefined); notifyOk('Configuration imported'); reloadSettings(); reloadRepos() })
      .catch(notifyError).finally(() => setBusy(false))
  }
  const contents: [string, number][] = pending ? [
    ['repository', pending.doc.repositories?.length ?? 0], ['user', pending.doc.users?.length ?? 0], ['access override', pending.doc.permissions?.length ?? 0],
    ['webhook', pending.doc.webhooks?.length ?? 0], ['notification channel', pending.doc.notifiers?.length ?? 0],
  ] : []
  return (
    <Stack maw={760}>
      <Section title="Export configuration" description="Download repositories, users, permissions, webhooks, notification channels and settings as JSON — handy for disaster recovery or cloning a setup to another server.">
        <Text size="sm" c="dimmed">Passwords, remote credentials, webhook secrets, notification tokens and artifacts are not included.</Text>
        <Group><Button component="a" href="/api/system/export" leftSection={<IconDatabaseExport size={16} />}>Download configuration</Button></Group>
      </Section>
      <Section title="Import configuration" description="Apply an exported file. Existing repositories are updated, new ones created. Imported users get random passwords that you must reset.">
        <Dropzone onDrop={(f) => choose(f[0])} multiple={false} accept={{ 'application/json': ['.json'] }} maxSize={32 * 1024 * 1024} loading={busy}
          onReject={(r) => notifyError(new Error(r[0]?.errors[0]?.code === 'file-too-large' ? 'Configuration files are at most 32 MB.' : 'Choose a .json file made with “Download configuration”.'))}>
          <Group justify="center" gap="md" py="md" wrap="nowrap" style={{ pointerEvents: 'none' }}>
            <Dropzone.Accept><IconDatabaseImport size={32} color="var(--mantine-color-brand-6)" /></Dropzone.Accept>
            <Dropzone.Reject><IconX size={32} color="var(--mantine-color-red-6)" /></Dropzone.Reject>
            <Dropzone.Idle><IconDatabaseImport size={32} color="var(--mantine-color-dimmed)" /></Dropzone.Idle>
            <div>
              <Text fw={500}>Drop an exported configuration here, or click to choose</Text>
              <Text size="sm" c="dimmed">You'll see what the file contains before anything changes.</Text>
            </div>
          </Group>
        </Dropzone>
        <Modal opened={!!pending} onClose={() => setPending(undefined)} closeOnClickOutside={!busy} title={<Text fw={600}>Import this configuration?</Text>}>
          {pending && (
            <Stack>
              <Text size="sm"><b style={{ wordBreak: 'break-all' }}>{pending.name}</b>
                {pending.doc.exportedAt ? <> was exported on {fullDate(pending.doc.exportedAt)}{pending.doc.version ? ` by RepoMan ${pending.doc.version}` : ''} and</> : ''} contains:</Text>
              <List size="sm" spacing={4}>
                {contents.filter(([, n]) => n > 0).map(([noun, n]) => <List.Item key={noun}>{plural(n, noun, noun === 'repository' ? 'repositories' : undefined)}</List.Item>)}
                {!!pending.doc.settings && !!pending.doc.version && <List.Item>server settings (base URL, anonymous access, banner, audit retention)</List.Item>}
              </List>
              <Alert variant="light" color="orange" icon={<IconInfoCircle />} py="xs">
                Repositories with the same name are overwritten with the file's configuration{pending.doc.settings && pending.doc.version ? ', and the server settings are replaced' : ''}. Nothing is deleted, and artifacts are not touched.
              </Alert>
              <Group justify="flex-end">
                <Button variant="default" onClick={() => setPending(undefined)} disabled={busy}>Cancel</Button>
                <Button onClick={apply} loading={busy} leftSection={<IconDatabaseImport size={16} />}>Import</Button>
              </Group>
            </Stack>
          )}
        </Modal>
        {report && (
          <Alert color={report.errors?.length ? 'orange' : 'teal'} title="Import finished" withCloseButton closeButtonLabel="Dismiss" onClose={() => setReport(undefined)}>
            <Text size="sm">Created: {report.created?.length ? report.created.join(', ') : 'nothing'}</Text>
            <Text size="sm">Updated: {report.updated?.length ? report.updated.join(', ') : 'nothing'}</Text>
            {report.usersNeedingPassword?.length > 0 && <Text size="sm">Reset passwords for: <b>{report.usersNeedingPassword.join(', ')}</b></Text>}
            {report.errors?.length > 0 && <Text size="sm" c="red">Errors: {report.errors.join('; ')}</Text>}
          </Alert>
        )}
      </Section>
      <Section title="Backing up artifacts">
        <Text size="sm">Everything RepoMan stores lives in its data directory (database + blobs). For a consistent backup, stop the server or snapshot the volume, then copy the whole directory.</Text>
      </Section>
    </Stack>
  )
}

interface SysInfo {
  version: string; commit: string; buildDate: string; tls: boolean; goVersion: string; os: string; arch: string; startedAt: number; uptimeSeconds: number; dataDir: string
  databaseBytes: number; blobCount: number; blobBytes: number; assets: number; goroutines: number; heapBytes: number; requests: Record<string, number>
}

function System() {
  const { data: s, error, reload } = useApi<SysInfo>('/system/info')
  if (!s) return error ? <LoadError error={error} retry={reload} /> : <FormSkeleton />
  const up = s.uptimeSeconds
  const uptime = up > 86400 ? `${Math.floor(up / 86400)}d ${Math.floor((up % 86400) / 3600)}h` : up > 3600 ? `${Math.floor(up / 3600)}h ${Math.floor((up % 3600) / 60)}m` : `${Math.floor(up / 60)}m`
  const rows: [string, string][] = [
    ['Version', `RepoMan ${s.version}${s.commit ? ` · ${s.commit}` : ''}${s.buildDate ? ` · built ${s.buildDate}` : ''} (${s.goVersion}, ${s.os}/${s.arch})`], ['Transport', s.tls ? 'HTTPS (built-in TLS)' : window.location.protocol === 'https:' ? 'HTTPS (terminated by a reverse proxy)' : 'Plain HTTP'], ['Started', `${fullDate(s.startedAt)} · up ${uptime}`],
    ['Data directory', s.dataDir], ['Database size', formatBytes(s.databaseBytes)], ['Content files', `${s.blobCount.toLocaleString()} (${formatBytes(s.blobBytes)})`],
    ['Artifacts', s.assets.toLocaleString()], ['Memory (heap)', formatBytes(s.heapBytes)], ['Goroutines', String(s.goroutines)],
    ['HTTP responses', Object.entries(s.requests).filter(([, n]) => n).map(([k, n]) => `${k}: ${n.toLocaleString()}`).join(' · ')],
  ]
  return (
    <SimpleGrid cols={{ base: 1, lg: 2 }}>
      <Card padding="lg">
        <Text fw={600} mb="sm">System information</Text>
        <Table variant="vertical" layout="fixed" withTableBorder>
          <Table.Tbody>{rows.map(([k, v]) => <Table.Tr key={k}><Table.Th w={160}>{k}</Table.Th><Table.Td style={{ wordBreak: 'break-all' }}>{v}</Table.Td></Table.Tr>)}</Table.Tbody>
        </Table>
      </Card>
      <Card padding="lg">
        <Text fw={600} mb="xs">Monitoring</Text>
        <Text size="sm" c="dimmed" mb="sm">Prometheus metrics (per-repository artifacts, bytes, downloads and HTTP responses) are at /metrics. Authenticate with a user and API token:</Text>
        <Code block>{`scrape_configs:
  - job_name: repoman
    metrics_path: /metrics
    basic_auth:
      username: monitoring
      password: <API_TOKEN>
    static_configs:
      - targets: ['${window.location.host}']`}</Code>
        <Text size="sm" c="dimmed" mt="sm">Health probe: <Code>GET /healthz</Code> answers 200 when the server and its database are up. In containers, use <Code>repoman healthcheck</Code>.</Text>
      </Card>
    </SimpleGrid>
  )
}

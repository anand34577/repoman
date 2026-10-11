import { Accordion, Anchor, Badge, Card, Code, Group, Kbd, SimpleGrid, Stack, Table, Tabs, Text, ThemeIcon, Title } from '@mantine/core'
import { IconApi, IconBook, IconHelp, IconKey, IconKeyboard, IconStack2 } from '@tabler/icons-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useBaseUrl } from '../App'
import { FORMATS, TYPES } from '../formats'
import { PageHeader, TypeBadge } from '../util'

const endpoints: [string, string, string][] = [
  ['GET', '/api/repositories', 'List repositories you can read (with size, status and your access level)'],
  ['POST', '/api/repositories', 'Create a repository (admin)'],
  ['PUT', '/api/repositories/{name}', 'Update configuration (admin)'],
  ['DELETE', '/api/repositories/{name}', 'Delete a repository and its artifacts (admin)'],
  ['GET', '/api/repositories/{name}/browse?path=', 'List files and folders'],
  ['GET', '/api/repositories/{name}/components?q=', 'Components and versions'],
  ['DELETE', '/api/repositories/{name}/components?component=&version=', 'Delete a component or one version'],
  ['POST', '/api/repositories/{name}/upload', 'Multipart upload (raw, maven, go, apt, yum, conda, alpine, huggingface)'],
  ['POST', '/api/repositories/{name}/cleanup?dryRun=1', 'Preview or run the cleanup policy (admin)'],
  ['GET/PUT', '/api/repositories/{name}/permissions', 'Per-user access overrides (admin)'],
  ['GET', '/api/search?q=&repo=&format=', 'Search artifacts; q accepts version:, repo:, format:, uploader:, sha256:'],
  ['GET', '/api/repositories/{name}/package?component=&version=', 'One package: files with checksums, metadata, dependencies, downloads'],
  ['GET', '/api/repositories/{name}/dependents?component=', 'Packages that depend on a component'],
  ['POST', '/api/repositories/{name}/promote', 'Copy or move versions to another hosted repository'],
  ['POST', '/api/repositories/{name}/bulk-delete', 'Delete files, folders or packages together'],
  ['GET', '/api/storage', 'Storage per repository and growth (admin)'],
  ['DELETE', '/api/assets/{id}', 'Delete one artifact'],
  ['GET/POST', '/api/tokens', 'List or create your API tokens'],
  ['GET/POST', '/api/users', 'Manage users (admin)'],
  ['GET/POST', '/api/webhooks', 'Manage webhooks (admin)'],
  ['GET/POST', '/api/notifiers', 'Manage notification channels (admin)'],
  ['POST', '/api/notifiers/{id}/test', 'Send a test notification (admin)'],
  ['GET', '/api/audit?q=&action=&offset=&limit=', 'Search the audit log, paged (admin)'],
  ['GET', '/api/audit/export?q=&action=', 'Download matching audit events as CSV (admin)'],
  ['GET', '/api/auth/me', 'Who am I? Shows your role and the token scope'],
  ['POST', '/api/tasks/{id}/run', 'Run a maintenance task (admin)'],
  ['GET', '/api/system/export', 'Export configuration (admin)'],
  ['GET', '/metrics', 'Prometheus metrics'],
  ['GET', '/healthz', 'Health probe (no authentication)'],
]

const faq: [string, React.ReactNode][] = [
  ['I forgot the admin password', <>On the server, run <Code>repoman reset-password admin</Code> (add <Code>--data-dir</Code> if you changed it). It prints a new password, re-enables the account and signs it out everywhere. In Docker: <Code>docker exec -it repoman repoman reset-password</Code>.</>],
  ['Build machines have no internet access. What must the server reach?', <>Only the upstream of each proxy repository, and your own SSO, webhook and Gotify servers if you use them. RepoMan itself never contacts anything else: no update checks, telemetry or CDN. Some registries serve files from a second host (Docker Hub, PyPI); the list is in <Code>docs/firewall.md</Code>.</>],
  ['Go: do build machines need to reach sum.golang.org?', <>No. A Go proxy or group repository relays the checksum database through its upstream, so <Code>GOPROXY</Code> alone is enough. Modules you publish here are never looked up publicly; list them in <Code>GONOSUMDB</Code>.</>],
  ['Docker says “http: server gave HTTP response to HTTPS client”', <>Docker only allows plain HTTP for <Code>localhost</Code>. Put RepoMan behind a TLS reverse proxy (recommended) or add the host to <Code>insecure-registries</Code> in <Code>/etc/docker/daemon.json</Code>.</>],
  ['Docker image names', <>The first path segment after the host is the repository: <Code>host:8081/docker-hosted/team/app:1.0</Code>. Docker Hub official images need <Code>library/</Code>, e.g. <Code>docker-hub/library/nginx</Code>.</>],
  ['apt: “The following signatures couldn’t be verified”', <>Import the key from <Code>…/repository/&lt;repo&gt;/repository.key</Code> into <Code>/etc/apt/keyrings</Code> and reference it with <Code>signed-by=</Code>. The Setup tab has the exact commands.</>],
  ['Go: “verifying module: … 404 Not Found” for a private module', <>Private modules aren't in the public checksum database. Run <Code>go env -w GONOSUMDB=your.company.com</Code>.</>],
  ['npm login fails with a browser prompt', <>Use legacy login: <Code>npm login --auth-type=legacy --registry=…</Code>, or put an API token in <Code>.npmrc</Code> (recommended).</>],
  ['pip can’t find packages', <>Use the <Code>/simple/</Code> URL of the repository (with trailing slash), e.g. <Code>…/repository/pypi-public/simple/</Code>.</>],
  ['dnf: “Cannot download repomd.xml”', <>Proxy repositories need a <b>baseurl</b> that contains <Code>repodata/</Code>, not a mirrorlist or metalink URL.</>],
  ['Uploads fail with “507 Insufficient Storage”', <>The repository reached its storage quota. Raise it under Configuration or run cleanup.</>],
  ['Upstream is down — will my builds break?', <>No: proxies keep serving cached artifacts and stale metadata when the remote fails. The repository shows a “Remote unavailable” badge until it recovers.</>],
  ['Why does a group return an old version?', <>Groups return the first member that has a file. Order members with hosted repositories first, and lower the proxy's metadata cache time.</>],
]

export default function Help() {
  const { '*': sub } = useParams()
  const navigate = useNavigate()
  const base = useBaseUrl()
  return (
    <>
      <PageHeader title="Guides & docs" description="Everything you need to set up and use RepoMan." />
      <Tabs value={sub || 'concepts'} onChange={(t) => navigate(`/help/${t === 'concepts' ? '' : t}`)} keepMounted={false}>
        <Tabs.List mb="lg" className="tabs-scroll">
          <Tabs.Tab value="concepts" leftSection={<IconStack2 size={16} />}>Concepts</Tabs.Tab>
          <Tabs.Tab value="formats" leftSection={<IconBook size={16} />}>Formats</Tabs.Tab>
          <Tabs.Tab value="auth" leftSection={<IconKey size={16} />}>Authentication</Tabs.Tab>
          <Tabs.Tab value="api" leftSection={<IconApi size={16} />}>REST API</Tabs.Tab>
          <Tabs.Tab value="faq" leftSection={<IconHelp size={16} />}>Troubleshooting</Tabs.Tab>
          <Tabs.Tab value="keys" leftSection={<IconKeyboard size={16} />}>Shortcuts</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="concepts">
          <SimpleGrid cols={{ base: 1, md: 3 }} mb="lg">
            {(Object.keys(TYPES) as (keyof typeof TYPES)[]).map((t) => (
              <Card key={t} padding="lg"><TypeBadge type={t} /><Text fw={600} mt="sm">{TYPES[t].summary}</Text><Text size="sm" c="dimmed" mt={4}>{TYPES[t].detail}</Text></Card>
            ))}
          </SimpleGrid>
          <Card padding="lg" maw={900}>
            <Title order={4} mb="xs">A typical setup</Title>
            <Text size="sm" mb="sm">For each format your teams use, create three repositories and point every tool at the group:</Text>
            <Code block>{`npm-hosted   (hosted)  ← CI publishes internal packages here
npm-proxy    (proxy)   ← caches registry.npmjs.org
npm-public   (group)   = npm-hosted → npm-proxy   ← developers and CI install from here`}</Code>
            <Text size="sm" mt="sm">Artifacts are stored once even if several repositories contain the same file. Cleanup policies, quotas and per-repository access keep storage and permissions under control. <Anchor component={Link} to="/repositories/new">Create a repository →</Anchor></Text>
          </Card>
        </Tabs.Panel>

        <Tabs.Panel value="formats">
          <Card padding={0}>
            <Table.ScrollContainer minWidth={800}>
              <Table verticalSpacing="sm" horizontalSpacing="md">
                <Table.Thead><Table.Tr><Table.Th>Format</Table.Th><Table.Th>Clients</Table.Th><Table.Th>Types</Table.Th><Table.Th>Client URL</Table.Th></Table.Tr></Table.Thead>
                <Table.Tbody>
                  {FORMATS.map((f) => (
                    <Table.Tr key={f.id}>
                      <Table.Td><Group gap="sm" wrap="nowrap"><ThemeIcon variant="light" color={f.color}><f.icon size={18} /></ThemeIcon><div><Text size="sm" fw={600}>{f.label}</Text><Text size="xs" c="dimmed">{f.tagline}</Text></div></Group></Table.Td>
                      <Table.Td><Text size="sm">{f.clients}</Text></Table.Td>
                      <Table.Td><Group gap={4}>{f.types.map((t) => <TypeBadge key={t} type={t} />)}</Group></Table.Td>
                      <Table.Td><Code>{f.id === 'docker' ? `${new URL(base).host}/<repo>/<image>` : f.id === 'pypi' ? `${base}/repository/<repo>/simple/` : `${base}/repository/<repo>/`}</Code></Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </Card>
          <Text size="sm" c="dimmed" mt="sm">Open any repository's Setup tab for complete, copy-paste instructions for each client.</Text>
        </Tabs.Panel>

        <Tabs.Panel value="auth">
          <Stack maw={900}>
            <Card padding="lg">
              <Title order={4} mb="xs">API tokens (recommended)</Title>
              <Text size="sm">Create tokens under <Anchor component={Link} to="/account?new=1">Account & tokens</Anchor>. Use them anywhere a password is expected — Maven <Code>settings.xml</Code>, <Code>docker login</Code>, pip URLs, <Code>.netrc</Code> — or as a bearer token. Each token has an access level: <b>read-only</b> for machines that only install, <b>read &amp; publish</b> for CI that releases, <b>full access</b> for automation that manages the server. Tokens can expire and be revoked individually without changing your password.</Text>
              <Code block mt="sm">{`curl -u alice:rm_xxxxxxxx ${base}/api/repositories
curl -H "Authorization: Bearer rm_xxxxxxxx" ${base}/api/v1/repositories`}</Code>
            </Card>
            <Card padding="lg">
              <Title order={4} mb="xs">Roles and repository access</Title>
              <Text size="sm"><Badge variant="light" color="gray">viewer</Badge> reads, <Badge variant="light" color="blue">developer</Badge> also publishes and deletes, <Badge variant="light" color="brand">admin</Badge> manages everything. Admins can override access per user on each repository's Access tab, and mark repositories private to hide them from anonymous users.</Text>
            </Card>
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="api">
          <Card padding={0} maw={1000}>
            <Table.ScrollContainer minWidth={700}>
              <Table verticalSpacing="xs" horizontalSpacing="md" striped>
                <Table.Thead><Table.Tr><Table.Th w={100}>Method</Table.Th><Table.Th>Path</Table.Th><Table.Th>Description</Table.Th></Table.Tr></Table.Thead>
                <Table.Tbody>
                  {endpoints.map(([m, p, d]) => (
                    <Table.Tr key={m + p}><Table.Td><Badge variant="light" radius="sm">{m}</Badge></Table.Td><Table.Td><Code>{p}</Code></Table.Td><Table.Td><Text size="sm">{d}</Text></Table.Td></Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </Card>
          <Text size="sm" c="dimmed" mt="sm" maw={1000}>All endpoints accept and return JSON and authenticate with a bearer token, basic auth (username + API token) or the browser session.
            For integrations, use the versioned prefix <Code>/api/v1/…</Code>: it names the same endpoints and stays stable across releases. Errors look like <Code>{'{"error": "message"}'}</Code> with a matching HTTP status.
            The complete reference with examples is in <Code>docs/api.md</Code>.</Text>
        </Tabs.Panel>

        <Tabs.Panel value="faq">
          <Accordion variant="separated" maw={900}>
            {faq.map(([q, a]) => <Accordion.Item key={q} value={q}><Accordion.Control>{q}</Accordion.Control><Accordion.Panel><Text size="sm">{a}</Text></Accordion.Panel></Accordion.Item>)}
          </Accordion>
        </Tabs.Panel>

        <Tabs.Panel value="keys">
          <Card padding="lg" maw={520}>
            <Table verticalSpacing="sm">
              <Table.Tbody>
                <Table.Tr><Table.Td><Kbd>Ctrl</Kbd> + <Kbd>K</Kbd></Table.Td><Table.Td>Open the command palette: jump to any page, repository or action</Table.Td></Table.Tr>
                <Table.Tr><Table.Td><Kbd>/</Kbd></Table.Td><Table.Td>Focus artifact search</Table.Td></Table.Tr>
                <Table.Tr><Table.Td><Kbd>Esc</Kbd></Table.Td><Table.Td>Close dialogs and drawers</Table.Td></Table.Tr>
              </Table.Tbody>
            </Table>
          </Card>
        </Tabs.Panel>
      </Tabs>
    </>
  )
}

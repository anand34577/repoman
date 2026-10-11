import { Anchor, Badge, Button, Card, Group, Loader, Select, SimpleGrid, Stack, Table, Tabs, Text, ThemeIcon, Title } from '@mantine/core'
import { useDocumentTitle } from '@mantine/hooks'
import { IconArrowLeft, IconBolt, IconDownload, IconExternalLink, IconLock, IconPackage, IconTrash } from '@tabler/icons-react'
import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useBaseUrl } from '../App'
import { api, useApi, type Dependent, type PackageView, type Repo } from '../api'
import { formatInfo } from '../formats'
import { assetLink, downloadUrl, packageLink } from '../links'
import { DayChart, HashRows, PROMOTABLE, PromoteModal, UsageTabs, verifyCommand } from '../usage'
import { confirmAction, Copy, EmptyState, formatBytes, FormatBadge, notifyError, notifyOk, Pager, plural, usePaging, When } from '../util'

/** One package (component) of a repository: description, usage, files with checksums, versions, dependencies and dependents. */
export default function PackagePage() {
  const { name = '' } = useParams()
  const [params] = useSearchParams()
  const component = params.get('component') ?? ''
  const version = params.get('version') ?? ''
  const navigate = useNavigate()
  const base = useBaseUrl()
  const repo = useApi<Repo>(`/repositories/${name}`)
  const pkg = useApi<PackageView>(`/repositories/${name}/package?component=${encodeURIComponent(component)}&version=${encodeURIComponent(version)}`)
  const [tab, setTab] = useState<string | null>('overview')
  const [promoting, setPromoting] = useState(false)
  useDocumentTitle(`${component} ${pkg.data?.version ?? ''} · RepoMan`)

  if (pkg.error || repo.error) return (
    <EmptyState icon={<IconLock />} title="Package not found" action={<Button component={Link} to={`/repositories/${name}/components`} variant="light">Back to components</Button>}>
      It doesn't exist in this repository, or you don't have access to it.
    </EmptyState>
  )
  if (!pkg.data || !repo.data) return <Group justify="center" p="xl"><Loader aria-label="Loading package" /></Group>
  const p = pkg.data, r = repo.data
  const canWrite = r.access === 'write'
  const FIcon = formatInfo(r.format).icon
  const setVersion = (v: string | null) => navigate(packageLink(name, component, v ?? ''), { replace: true })
  const info = p.info
  const del = () => confirmAction({
    title: `Delete ${component} ${p.version}?`, danger: true, label: 'Delete',
    body: 'Every file of this version is removed. This cannot be undone.',
    onConfirm: () => api(`/repositories/${name}/components?component=${encodeURIComponent(component)}&version=${encodeURIComponent(p.version)}`, { method: 'DELETE' })
      .then(() => { notifyOk('Version deleted'); navigate(`/repositories/${name}/components`) }).catch(notifyError),
  })

  return (
    <>
      <Anchor component={Link} to={`/repositories/${name}/components`} size="sm" c="dimmed"><Group gap={4}><IconArrowLeft size={14} />{name} · components</Group></Anchor>
      <Group justify="space-between" mt="xs" mb="md" align="flex-start" wrap="nowrap">
        <Group align="flex-start" wrap="nowrap" style={{ minWidth: 0 }}>
          <ThemeIcon size={48} radius="md" variant="light" color={formatInfo(r.format).color} style={{ flexShrink: 0 }}><FIcon size={28} /></ThemeIcon>
          <div style={{ minWidth: 0 }}>
            <Title order={2} style={{ wordBreak: 'break-all' }}>{component}</Title>
            <Group gap="xs" mt={4}>
              <FormatBadge format={r.format} />
              <Badge variant="light" radius="sm">{p.version || 'no version'}</Badge>
              {p.version && p.version === p.latest && <Badge color="teal" variant="light">latest</Badge>}
              <Anchor component={Link} to={`/repositories/${name}`} size="sm">{name}</Anchor>
            </Group>
            {info.description && <Text size="sm" mt="xs" maw={720}>{info.description}</Text>}
          </div>
        </Group>
        <Group gap="xs" wrap="nowrap">
          {p.versions.length > 1 && <Select size="sm" w={150} aria-label="Version" allowDeselect={false} searchable value={p.version} onChange={setVersion} data={p.versions.map((v) => v.version)} />}
          {PROMOTABLE.includes(r.format) && p.version && <Button variant="light" leftSection={<IconBolt size={16} />} onClick={() => setPromoting(true)}>Promote</Button>}
          {canWrite && r.type !== 'group' && p.version && <Button variant="light" color="red" leftSection={<IconTrash size={16} />} onClick={del}>Delete</Button>}
        </Group>
      </Group>

      <SimpleGrid cols={{ base: 2, sm: 4 }} mb="md" spacing="sm">
        <Card padding="sm"><Text size="xs" c="dimmed">Versions</Text><Text fw={600}>{p.versions.length.toLocaleString()}</Text></Card>
        <Card padding="sm"><Text size="xs" c="dimmed">Downloads (all versions)</Text><Text fw={600}>{p.downloadsTotal.toLocaleString()}</Text></Card>
        <Card padding="sm"><Text size="xs" c="dimmed">Dependencies</Text><Text fw={600}>{info.dependencies.length.toLocaleString()}</Text></Card>
        <Card padding="sm"><Text size="xs" c="dimmed">Used by</Text><Text fw={600}>{p.dependents.toLocaleString()} {p.dependents === 1 ? 'package' : 'packages'}</Text></Card>
      </SimpleGrid>

      <Tabs value={tab} onChange={setTab} keepMounted={false}>
        <Tabs.List mb="md" className="tabs-scroll">
          <Tabs.Tab value="overview">Overview</Tabs.Tab>
          <Tabs.Tab value="files">Files ({p.files.length})</Tabs.Tab>
          <Tabs.Tab value="versions">Versions ({p.versions.length})</Tabs.Tab>
          <Tabs.Tab value="deps">Dependencies ({info.dependencies.length})</Tabs.Tab>
          <Tabs.Tab value="dependents">Used by ({p.dependents})</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="overview">
          <SimpleGrid cols={{ base: 1, md: 3 }} spacing="md">
            <Stack style={{ gridColumn: 'span 2' }} gap="md">
              <Card padding="md"><UsageTabs repo={r} component={component} version={p.version} latest={p.latest} /></Card>
              {info.readme && <Card padding="md"><Text fw={600} size="sm" mb="xs">README</Text><Text size="sm" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{info.readme}</Text></Card>}
            </Stack>
            <Stack gap="md">
              <Card padding="md">
                <Text fw={600} size="sm" mb="xs">Details</Text>
                <Table variant="vertical" layout="fixed" verticalSpacing={4}>
                  <Table.Tbody>
                    <Table.Tr><Table.Th w={96}>Licence</Table.Th><Table.Td>{info.licenses?.join(', ') || '—'}</Table.Td></Table.Tr>
                    <Table.Tr><Table.Th>Homepage</Table.Th><Table.Td style={{ wordBreak: 'break-all' }}>{info.url ? <Anchor href={info.url} target="_blank" rel="noreferrer noopener" size="sm">{info.url} <IconExternalLink size={12} /></Anchor> : '—'}</Table.Td></Table.Tr>
                    <Table.Tr><Table.Th>Source</Table.Th><Table.Td style={{ wordBreak: 'break-all' }}>{info.scm || '—'}</Table.Td></Table.Tr>
                    <Table.Tr><Table.Th>Developers</Table.Th><Table.Td>{info.developers?.join(', ') || '—'}</Table.Td></Table.Tr>
                    <Table.Tr><Table.Th>Updated</Table.Th><Table.Td>{p.versions.find((v) => v.version === p.version) ? <When unix={p.versions.find((v) => v.version === p.version)!.updatedAt} /> : '—'}</Table.Td></Table.Tr>
                  </Table.Tbody>
                </Table>
              </Card>
              <Card padding="md"><Text fw={600} size="sm" mb="xs">Downloads, last 30 days</Text><DayChart days={p.downloadsDaily} /></Card>
            </Stack>
          </SimpleGrid>
        </Tabs.Panel>

        <Tabs.Panel value="files">
          <Stack>
            {p.files.length === 0 && <EmptyState icon={<IconPackage />} title="No files for this version" />}
            {p.files.map(({ asset, sums }) => {
              const file = asset.path.split('/').pop() ?? asset.path
              const url = base + downloadUrl(asset, r.format)
              return (
                <Card key={asset.id} padding="md">
                  <Group justify="space-between" wrap="nowrap" align="flex-start" mb="xs">
                    <div style={{ minWidth: 0 }}>
                      <Anchor component={Link} to={assetLink(asset)} fw={600} size="sm" style={{ wordBreak: 'break-all' }}>{file}</Anchor>
                      <Text size="xs" c="dimmed">{formatBytes(asset.size)} · {plural(asset.downloads, 'download')}{asset.repo !== name ? ` · from ${asset.repo}` : ''}</Text>
                    </div>
                    <Group gap="xs" wrap="nowrap">
                      <Copy value={url} label="Copy download URL" />
                      <Button component="a" href={downloadUrl(asset, r.format)} target="_blank" size="compact-sm" variant="light" leftSection={<IconDownload size={14} />}>Download</Button>
                    </Group>
                  </Group>
                  <Stack gap={4}><HashRows sums={sums} /></Stack>
                  <Group gap="xs" mt="xs"><Text size="xs" c="dimmed">Verify a download</Text><Copy value={verifyCommand(url, file, sums)} label="Copy verify commands" /></Group>
                  {r.format === 'maven' && (
                    <details style={{ marginTop: 8 }}>
                      <summary style={{ cursor: 'pointer', fontSize: 'var(--mantine-font-size-xs)' }}>Depend on this file</summary>
                      <div style={{ marginTop: 8 }}><UsageTabs repo={r} component={component} version={p.version} latest={p.latest} file={asset.path} /></div>
                    </details>
                  )}
                </Card>
              )
            })}
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="versions">
          <Card padding={0}>
            <Table.ScrollContainer minWidth={560}>
              <Table highlightOnHover verticalSpacing="xs" horizontalSpacing="md">
                <Table.Thead><Table.Tr><Table.Th>Version</Table.Th><Table.Th ta="right">Files</Table.Th><Table.Th ta="right">Size</Table.Th><Table.Th ta="right">Downloads</Table.Th><Table.Th>Updated</Table.Th></Table.Tr></Table.Thead>
                <Table.Tbody>
                  {p.versions.map((v) => (
                    <Table.Tr key={v.version}>
                      <Table.Td><Anchor component={Link} to={packageLink(name, component, v.version)} onClick={() => setTab('overview')} ff="monospace" size="sm" fw={v.version === p.version ? 700 : 400}>{v.version}</Anchor> {v.version === p.latest && <Badge size="xs" color="teal" variant="light">latest</Badge>}</Table.Td>
                      <Table.Td ta="right">{v.assets}</Table.Td>
                      <Table.Td ta="right">{formatBytes(v.size)}</Table.Td>
                      <Table.Td ta="right">{v.downloads.toLocaleString()}</Table.Td>
                      <Table.Td><When unix={v.updatedAt} never="—" /></Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </Card>
        </Tabs.Panel>

        <Tabs.Panel value="deps">
          {info.dependencies.length === 0
            ? <EmptyState icon={<IconPackage />} title="No dependencies recorded">{['maven', 'npm', 'go', 'conda'].includes(r.format) ? 'This version declares none.' : `RepoMan does not read dependencies from ${formatInfo(r.format).label} packages.`}</EmptyState>
            : (
              <Card padding={0}>
                <Table.ScrollContainer minWidth={480}>
                  <Table highlightOnHover verticalSpacing="xs" horizontalSpacing="md">
                    <Table.Thead><Table.Tr><Table.Th>Package</Table.Th><Table.Th>Version</Table.Th><Table.Th>Scope</Table.Th></Table.Tr></Table.Thead>
                    <Table.Tbody>
                      {info.dependencies.map((d, i) => (
                        <Table.Tr key={d.name + i}>
                          <Table.Td><Anchor component={Link} to={`/search?q=${encodeURIComponent(d.name)}`} size="sm" style={{ wordBreak: 'break-all' }}>{d.name}</Anchor></Table.Td>
                          <Table.Td><Text size="sm" ff="monospace">{d.version || '—'}</Text></Table.Td>
                          <Table.Td><Badge variant="light" color="gray" size="sm">{d.scope || 'runtime'}</Badge>{d.optional && <Badge ml={4} variant="light" color="orange" size="sm">optional</Badge>}</Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              </Card>
            )}
        </Tabs.Panel>

        <Tabs.Panel value="dependents"><Dependents repo={name} component={component} /></Tabs.Panel>
      </Tabs>
      {promoting && <PromoteModal repo={r} items={[{ component, version: p.version }]} canMove={r.type === 'hosted' && canWrite} onClose={() => setPromoting(false)} onDone={() => pkg.reload()} />}
    </>
  )
}

function Dependents({ repo, component }: { repo: string; component: string }) {
  const paging = usePaging(50)
  const { data, total, loading } = useApi<Dependent[]>(`/repositories/${repo}/dependents?component=${encodeURIComponent(component)}&${paging.query}`)
  if (data?.length === 0 && !total) return (
    <EmptyState icon={<IconPackage />} title="Nothing here depends on this package">
      Dependents come from the pom, go.mod, conda or hosted npm metadata of packages stored in RepoMan. New uploads are indexed automatically; an administrator can re-run “Index package dependencies” under Scheduled tasks.
    </EmptyState>
  )
  return (
    <Card padding={0}>
      <Table.ScrollContainer minWidth={520}>
        <Table highlightOnHover verticalSpacing="xs" horizontalSpacing="md" style={{ opacity: loading && data ? 0.6 : 1 }}>
          <Table.Thead><Table.Tr><Table.Th>Package</Table.Th><Table.Th>Version</Table.Th><Table.Th>Needs</Table.Th><Table.Th>Repository</Table.Th></Table.Tr></Table.Thead>
          <Table.Tbody>
            {data?.map((d) => (
              <Table.Tr key={d.repo + d.component + d.version}>
                <Table.Td><Anchor component={Link} to={packageLink(d.repo, d.component, d.version)} size="sm" style={{ wordBreak: 'break-all' }}>{d.component}</Anchor></Table.Td>
                <Table.Td><Text size="sm" ff="monospace">{d.version}</Text></Table.Td>
                <Table.Td><Text size="sm" ff="monospace">{d.depVersion || 'any'}</Text></Table.Td>
                <Table.Td><Anchor component={Link} to={`/repositories/${d.repo}`} size="sm">{d.repo}</Anchor></Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      <Pager paging={paging} total={total} noun="packages" />
    </Card>
  )
}

import type { ReactNode } from 'react'
import {
  Anchor, Badge, Button, Card, CloseButton, Group, Progress, SimpleGrid, Skeleton, Stack, Text, ThemeIcon,
} from '@mantine/core'
import { useLocalStorage } from '@mantine/hooks'
import {
  IconAlertTriangle, IconArchive, IconArrowRight, IconCircleCheckFilled, IconCircleDashed, IconCloudDownload, IconDatabase, IconPlus, IconServer,
} from '@tabler/icons-react'
import { Link } from 'react-router-dom'
import { useAuth, useIsAdmin } from '../App'
import { useApi, type Asset, type Stats } from '../api'
import { formatInfo, FormatIcon } from '../formats'
import { EmptyState, formatBytes, LoadError, PageHeader, plural, StatusBadge, timeAgo } from '../util'
import { assetLink } from '../links'
import { DayChart } from '../usage'

function StatCard({ label, value, hint, icon: Icon, color }: { label: string; value: string | number; hint?: string; icon: typeof IconServer; color: string }) {
  return (
    <Card padding="lg" className="stat-card">
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <div style={{ minWidth: 0 }}>
          <Text size="xs" c="dimmed" tt="uppercase" fw={600} className="page-eyebrow">{label}</Text>
          <Text fz={28} fw={700} mt={6} lh={1.1}>{value}</Text>
          {hint && <Text size="xs" c="dimmed">{hint}</Text>}
        </div>
        <div className="stat-icon" style={{ background: `var(--mantine-color-${color}-light)`, color: `var(--mantine-color-${color}-6)` }}>
          <Icon size={22} />
        </div>
      </Group>
    </Card>
  )
}

function AssetList({ title, assets, metric, empty }: { title: string; assets?: Asset[]; metric: (a: Asset) => string; empty: string }) {
  return (
    <Card padding="lg">
      <Text fw={600} mb="sm">{title}</Text>
      {!assets ? <Stack gap="sm">{Array.from({ length: 3 }, (_, i) => <Skeleton key={i} h={34} />)}</Stack>
        : assets.length === 0 ? <Text c="dimmed" size="sm">{empty}</Text> : (
        <Stack gap="xs">
          {assets.map((a) => (
            <Group key={a.id} justify="space-between" wrap="nowrap">
              <div style={{ minWidth: 0 }}>
                <Anchor component={Link} to={assetLink(a)} size="sm" fw={500} truncate="end" display="block">
                  {a.component || a.path.split('/').pop()}{a.version && <Text span c="dimmed" size="sm"> {a.version}</Text>}
                </Anchor>
                <Text size="xs" c="dimmed" truncate="end">{a.repo}</Text>
              </div>
              <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }}>{metric(a)}</Text>
            </Group>
          ))}
        </Stack>
      )}
    </Card>
  )
}

const checklist = [
  { key: 'passwordChanged', label: 'Choose your own admin password', hint: 'The admin account still uses its first-run password, which is also stored in a file on the server.', to: '/account', cta: 'Change password' },
  { key: 'baseUrl', label: 'Set the public base URL', hint: 'Needed behind a reverse proxy so clients get correct links.', to: '/settings', cta: 'Open settings' },
  { key: 'token', label: 'Create an API token', hint: 'Tokens are safer than passwords in build tools and CI.', to: '/account?new=1', cta: 'Create token' },
  { key: 'clientConnected', label: 'Connect your first client', hint: 'Each repository has copy-paste setup for Maven, npm, pip, Docker, Go, apt and dnf.', to: '/repositories', cta: 'Pick a repository' },
  { key: 'teamInvited', label: 'Invite your team', hint: 'Add users with viewer, developer or admin roles.', to: '/users', cta: 'Add users' },
  { key: 'notifications', label: 'Get notified about problems', hint: 'Send a push message through your Gotify server when an upstream goes down or a quota fills up.', to: '/notifications', cta: 'Set up notifications' },
] as const

function GettingStarted({ setup }: { setup: Stats['setup'] }) {
  const [hidden, setHidden] = useLocalStorage({ key: 'rm-hide-getting-started', defaultValue: false })
  const done = checklist.filter((c) => setup[c.key]).length
  if (hidden || done === checklist.length) return null
  return (
    <Card padding="lg" mb="lg">
      <Group justify="space-between" mb="xs">
        <div>
          <Text fw={600}>Get started with RepoMan</Text>
          <Text size="sm" c="dimmed">{done} of {checklist.length} steps done</Text>
        </div>
        <CloseButton aria-label="Hide checklist" onClick={() => setHidden(true)} />
      </Group>
      <Progress value={(done / checklist.length) * 100} mb="md" size="sm" />
      <Stack gap="sm">
        {checklist.map((c) => (
          <Group key={c.key} justify="space-between" gap="xs">
            <Group gap="sm" wrap="nowrap" align="flex-start" style={{ flex: '1 1 260px', minWidth: 0 }}>
              {setup[c.key]
                ? <ThemeIcon color="teal" variant="transparent" style={{ flexShrink: 0 }}><IconCircleCheckFilled /></ThemeIcon>
                : <ThemeIcon color="gray" variant="transparent" style={{ flexShrink: 0 }}><IconCircleDashed /></ThemeIcon>}
              <div>
                <Text size="sm" fw={500} td={setup[c.key] ? 'line-through' : undefined} c={setup[c.key] ? 'dimmed' : undefined}>{c.label}</Text>
                {!setup[c.key] && <Text size="xs" c="dimmed">{c.hint}</Text>}
              </div>
            </Group>
            {!setup[c.key] && <Button component={Link} to={c.to} size="xs" variant="light" ml={40} style={{ flexShrink: 0 }}>{c.cta}</Button>}
          </Group>
        ))}
      </Stack>
    </Card>
  )
}

function CardTitle({ title, link }: { title: string; link?: { to: string; label: string } }) {
  return (
    <Group justify="space-between" mb="md" wrap="nowrap">
      <Text fw={600}>{title}</Text>
      {link && <Anchor component={Link} to={link.to} size="sm" style={{ whiteSpace: 'nowrap' }}>{link.label} <IconArrowRight size={14} style={{ verticalAlign: -2 }} /></Anchor>}
    </Group>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section style={{ marginBottom: 'var(--mantine-spacing-xl)' }}>
      <Text size="xs" c="dimmed" tt="uppercase" fw={600} className="page-eyebrow" mb="sm">{title}</Text>
      {children}
    </section>
  )
}

const statuses = [['online', 'teal', 'online'], ['degraded', 'orange', 'remote unavailable'], ['offline', 'gray', 'offline']] as const

/** Hosted (published here) against cached (copied by proxies), as one split bar with a legend. */
function StorageSplit({ s }: { s: Stats }) {
  const total = Math.max(1, s.hostedBytes + s.cachedBytes)
  const parts = [
    { label: 'Hosted', hint: 'published here', bytes: s.hostedBytes, color: 'var(--mantine-color-brand-6)' },
    { label: 'Cached', hint: 'copied by proxies', bytes: s.cachedBytes, color: 'var(--mantine-color-cyan-5)' },
  ]
  return (
    <div>
      <div className="bar-track" style={{ display: 'flex', height: 10, gap: 2, background: 'transparent' }} role="img"
        aria-label={parts.map((p) => `${p.label} ${formatBytes(p.bytes)}`).join(', ')}>
        {parts.filter((p) => p.bytes > 0).map((p) => (
          <div key={p.label} className="bar-fill" style={{ width: `${Math.max(2, (p.bytes / total) * 100)}%`, background: p.color }} />
        ))}
        {s.hostedBytes + s.cachedBytes === 0 && <div className="bar-fill" style={{ width: '100%', background: 'var(--mantine-color-default-border)' }} />}
      </div>
      <Group gap="lg" mt="xs">
        {parts.map((p) => (
          <Group key={p.label} gap={6} wrap="nowrap">
            <span style={{ width: 10, height: 10, borderRadius: 2, background: p.color, flexShrink: 0 }} />
            <Text size="sm"><b>{p.label}</b> {formatBytes(p.bytes)} <Text span size="xs" c="dimmed">{p.hint}</Text></Text>
          </Group>
        ))}
      </Group>
    </div>
  )
}

export default function Dashboard() {
  const { user } = useAuth()
  const isAdmin = useIsAdmin()
  const { data: s, error, reload } = useApi<Stats>('/stats')
  const hour = new Date().getHours()
  const greeting = hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening'
  const used = (s?.formats ?? []).filter((f) => f.repos > 0).sort((a, b) => b.bytes - a.bytes || b.assets - a.assets)
  const filled = used.filter((f) => f.assets > 0)
  const empty = used.filter((f) => f.assets === 0)
  const maxBytes = Math.max(1, ...filled.map((f) => f.bytes))
  const formatTotal = Math.max(1, filled.reduce((n, f) => n + f.bytes, 0))
  const recentDownloads = s?.downloadsDaily?.reduce((n, d) => n + d.n, 0) ?? 0
  const typeMix = (['hosted', 'proxy', 'group'] as const).filter((t) => s?.types?.[t]).map((t) => `${s!.types[t]} ${t}`).join(' · ')

  return (
    <>
      <PageHeader eyebrow="Dashboard" title={`${greeting}, ${user.username}`} description="Here's what's happening across your repositories."
        action={isAdmin && <Button component={Link} to="/repositories/new" leftSection={<IconPlus size={16} />}>Create repository</Button>} />
      <LoadError error={error} retry={reload} />
      {isAdmin && s && <GettingStarted setup={s.setup} />}

      <Section title="Overview">
        <SimpleGrid cols={{ base: 1, xs: 2, lg: 4 }}>
          {s ? <>
            <StatCard label="Repositories" value={s.repositories} icon={IconDatabase} color="brand" hint={typeMix || plural(s.users, 'user')} />
            <StatCard label="Artifacts" value={s.assets.toLocaleString()} icon={IconArchive} color="teal"
              hint={filled.length ? `in ${plural(filled.length, 'format')}` : undefined} />
            <StatCard label="Storage on disk" value={formatBytes(s.storageBytes)} icon={IconServer} color="blue"
              hint={s.logicalBytes > s.storageBytes ? `${formatBytes(s.logicalBytes - s.storageBytes)} saved by deduplication` : undefined} />
            <StatCard label="Downloads" value={s.downloads.toLocaleString()} icon={IconCloudDownload} color="orange"
              hint={`${recentDownloads.toLocaleString()} in the last 30 days`} />
          </> : Array.from({ length: 4 }, (_, i) => <Skeleton key={i} h={96} radius="lg" />)}
        </SimpleGrid>
      </Section>

      <Section title="Health and activity">
      {/* two independent columns: a short card is not stretched to the height of its neighbour */}
      <SimpleGrid cols={{ base: 1, md: 2 }} style={{ alignItems: 'start' }}>
        <Card padding="lg">
          <CardTitle title="Repository health" link={{ to: '/repositories', label: 'All repositories' }} />
          {s && (
            <Group gap="xs" mb="md">
              {statuses.filter(([k]) => s.statuses?.[k] || k === 'online').map(([k, color, label]) => (
                <Badge key={k} variant="light" color={color} radius="sm" size="lg" tt="none" fw={500}>{s.statuses?.[k] ?? 0} {label}</Badge>
              ))}
            </Group>
          )}
          {!s ? <Skeleton h={28} /> : !s.attention?.length ? (
            <Group gap="sm"><ThemeIcon color="teal" variant="light" radius="xl"><IconCircleCheckFilled size={18} /></ThemeIcon>
              <Text size="sm">All repositories are healthy.</Text></Group>
          ) : (
            <Stack gap="sm">
              {s?.attention?.map((r) => (
                <Group key={r.name} justify="space-between" wrap="nowrap">
                  <Group gap="sm" wrap="nowrap" style={{ minWidth: 0 }}>
                    <IconAlertTriangle size={18} color="var(--mantine-color-orange-6)" style={{ flexShrink: 0 }} />
                    <div style={{ minWidth: 0 }}>
                      <Anchor component={Link} to={`/repositories/${r.name}`} size="sm" fw={500}>{r.name}</Anchor>
                      <Text size="xs" c="dimmed" lineClamp={1}>
                        {r.status === 'degraded' ? r.health?.lastError : r.status === 'offline' ? 'Offline — clients get errors' : `Using ${formatBytes(r.size)} of ${r.quotaMb} MB quota`}
                      </Text>
                    </div>
                  </Group>
                  <div style={{ flexShrink: 0 }}><StatusBadge repo={r} /></div>
                </Group>
              ))}
            </Stack>
          )}
        </Card>
        <Card padding="lg">
          <CardTitle title="Downloads, last 30 days" />
          {!s ? <Skeleton h={76} /> : recentDownloads === 0
            ? <Text size="sm" c="dimmed">No downloads in the last 30 days. Point a client at a repository (its Setup tab shows how) and they show up here.</Text>
            : <DayChart days={s.downloadsDaily} />}
        </Card>
      </SimpleGrid>
      </Section>

      <Section title="Storage">
        <Card padding="lg">
          <CardTitle title="Where the space goes" link={isAdmin ? { to: '/storage', label: 'Storage details' } : undefined} />
          {!s ? <Stack gap="md">{Array.from({ length: 4 }, (_, i) => <Skeleton key={i} h={28} />)}</Stack>
            : !used.length ? <EmptyState icon={<IconDatabase />} title="No repositories yet" /> : <>
              <StorageSplit s={s} />
              <Text size="sm" fw={500} mt="lg" mb="sm">By format</Text>
              {!filled.length && <Text size="sm" c="dimmed">Nothing stored yet.</Text>}
              <SimpleGrid cols={{ base: 1, md: 2 }} spacing="xl" verticalSpacing="md">
                {filled.map((f) => (
                  <div key={f.format}>
                    <Group justify="space-between" mb={4} wrap="nowrap">
                      <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                        <FormatIcon format={f.format} size={16} />
                        <Text size="sm" fw={500} truncate="end">{formatInfo(f.format).label}</Text>
                        <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }}>{plural(f.repos, 'repo')} · {plural(f.assets, 'artifact')}</Text>
                      </Group>
                      <Text size="sm" c="dimmed" style={{ whiteSpace: 'nowrap' }}>
                        {formatBytes(f.bytes)} <Text span size="xs">({Math.round((f.bytes / formatTotal) * 100)}%)</Text>
                      </Text>
                    </Group>
                    <div className="bar-track">
                      <div className="bar-fill" style={{ width: f.bytes ? `${Math.max(2, (f.bytes / maxBytes) * 100)}%` : 0, background: `var(--mantine-color-${formatInfo(f.format).color}-5)` }} />
                    </div>
                  </div>
                ))}
              </SimpleGrid>
              {empty.length > 0 && (
                <Group gap={6} mt="md">
                  <Text size="xs" c="dimmed">Set up, still empty:</Text>
                  {empty.map((f) => (
                    <Badge key={f.format} variant="default" radius="sm" tt="none" fw={500} leftSection={<FormatIcon format={f.format} size={12} />}>{formatInfo(f.format).label}</Badge>
                  ))}
                </Group>
              )}
            </>}
        </Card>
      </Section>

      <Section title="Artifacts">
        <SimpleGrid cols={{ base: 1, md: 2 }} style={{ alignItems: 'start' }}>
          <AssetList title="Recently published" assets={s?.recent} metric={(a) => timeAgo(a.createdAt)} empty="Nothing published yet. Open a hosted repository's Setup tab to publish your first artifact." />
          <AssetList title="Most downloaded" assets={s?.popular} metric={(a) => plural(a.downloads, 'download')} empty="No downloads yet." />
        </SimpleGrid>
      </Section>
    </>
  )
}

import {
  ActionIcon, Alert, AppShell, Avatar, Burger, Button, Center, Divider, Group, Kbd, Loader, Menu, NavLink, Paper, PasswordInput,
  ScrollArea, SimpleGrid, Stack, Text, TextInput, ThemeIcon, Title, Tooltip, UnstyledButton, useComputedColorScheme, useMantineColorScheme,
} from '@mantine/core'
import { useDebouncedValue, useDisclosure, useHotkeys } from '@mantine/hooks'
import { Spotlight, spotlight, type SpotlightActionGroupData } from '@mantine/spotlight'
import {
  IconActivity, IconBell, IconBook, IconChartAreaLine, IconPackage, IconLogin2, IconBox, IconChevronDown, IconClockPlay, IconDatabase, IconKey, IconLayoutDashboard, IconLogout,
  IconMoon, IconPlus, IconSearch, IconSettings, IconShieldLock, IconSpeakerphone, IconSun, IconUsers, IconWebhook,
} from '@tabler/icons-react'
import { createContext, lazy, Suspense, useContext, useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { notifications } from '@mantine/notifications'
import { api, clearApiCache, useApi, type Asset, type OidcPublic, type Repo, type Settings, type User } from './api'
import { assetLink, packageLink } from './links'
import { FormatIcon, FORMATS } from './formats'
import Dashboard from './pages/Dashboard'
import { ErrorBoundary, MOD_KEY, useValidation } from './util'

const Account = lazy(() => import('./pages/Account'))
const AuditLog = lazy(() => import('./pages/Audit'))
const Help = lazy(() => import('./pages/Help'))
const Notifications = lazy(() => import('./pages/Notifications'))
const PackagePage = lazy(() => import('./pages/PackagePage'))
const RepoDetail = lazy(() => import('./pages/RepoDetail'))
const Repositories = lazy(() => import('./pages/Repositories'))
const RepoWizard = lazy(() => import('./pages/RepoWizard'))
const Search = lazy(() => import('./pages/Search'))
const SettingsPage = lazy(() => import('./pages/Settings'))
const Storage = lazy(() => import('./pages/Storage'))
const Tasks = lazy(() => import('./pages/Tasks'))
const Users = lazy(() => import('./pages/Users'))
const Webhooks = lazy(() => import('./pages/Webhooks'))

interface AppCtx { user: User; logout: () => void; settings?: Settings; reloadSettings: () => void; reloadRepos: () => void }
const AuthContext = createContext<AppCtx | null>(null)
export const useAuth = () => useContext(AuthContext)!
export const useIsAdmin = () => useAuth().user.role === 'admin'
export const baseUrl = (s?: Settings) => (s?.baseUrl || window.location.origin).replace(/\/$/, '')
export const useBaseUrl = () => baseUrl(useAuth().settings)

export default function App() {
  const [user, setUserState] = useState<User | null>()
  // whatever one account loaded must not be shown to the next one
  const setUser = (u: User | null) => { clearApiCache(); setUserState(u) }

  useEffect(() => {
    api<User>('/auth/me').then(setUser).catch(() => setUser(null))
    const onUnauthorized = () => setUserState((cur) => {
      if (cur) notifications.show({ id: 'signed-out', color: 'orange', title: 'You were signed out', message: 'Your session ended. Sign in again to continue where you left off.' })
      clearApiCache()
      return null
    })
    window.addEventListener('rm:unauthorized', onUnauthorized)
    return () => window.removeEventListener('rm:unauthorized', onUnauthorized)
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  if (user === undefined) return <Center h="100vh"><Loader aria-label="Loading RepoMan" /></Center>
  if (user === null) return <Login onLogin={setUser} />
  return <Shell user={user} logout={() => api('/auth/logout', { method: 'POST' }).catch(() => undefined).finally(() => setUser(null))} />
}

function Brand({ compact }: { compact?: boolean }) {
  return (
    <Group gap="xs" wrap="nowrap">
      <div className="brand-mark"><IconBox size={20} /></div>
      <div>
        <Text fw={700} lh={1.1}>RepoMan</Text>
        <Text size="xs" c="dimmed" lh={1.1} visibleFrom={compact ? 'xs' : undefined}>Repository Manager</Text>
      </div>
    </Group>
  )
}

function Shell({ user, logout }: { user: User; logout: () => void }) {
  const [opened, { toggle, close }] = useDisclosure()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { setColorScheme } = useMantineColorScheme()
  const scheme = useComputedColorScheme('light')
  const settings = useApi<Settings>('/settings')
  const repos = useApi<Repo[]>('/repositories')
  const [q, setQ] = useState('')
  const [bannerHidden, setBannerHidden] = useState<string>()
  const searchRef = useRef<HTMLInputElement>(null)
  const isAdmin = user.role === 'admin'
  // the palette also finds packages: what is typed is searched on the server (debounced), grouped by package and version
  const [paletteQuery, setPaletteQuery] = useState('')
  const [dq] = useDebouncedValue(paletteQuery.trim(), 250)
  const [hits, setHits] = useState<Asset[]>([])
  useEffect(() => {
    if (dq.length < 3) { setHits([]); return }
    let stale = false
    api<Asset[]>(`/search?q=${encodeURIComponent(dq)}&limit=40&sort=downloads`).then((r) => { if (!stale) setHits(r) }).catch(() => { if (!stale) setHits([]) })
    return () => { stale = true }
  }, [dq])
  useHotkeys([['/', () => searchRef.current?.focus()]])

  const sections = [
    {
      label: 'Overview', items: [
        { to: '/', label: 'Dashboard', icon: IconLayoutDashboard },
        { to: '/repositories', label: 'Repositories', icon: IconDatabase },
        { to: '/search', label: 'Search artifacts', icon: IconSearch },
      ],
    },
    ...(isAdmin ? [{
      label: 'Administration', items: [
        { to: '/users', label: 'Users & roles', icon: IconUsers },
        { to: '/notifications', label: 'Notifications', icon: IconBell },
        { to: '/webhooks', label: 'Webhooks', icon: IconWebhook },
        { to: '/tasks', label: 'Scheduled tasks', icon: IconClockPlay },
        { to: '/storage', label: 'Storage', icon: IconChartAreaLine },
        { to: '/audit', label: 'Audit log', icon: IconActivity },
        { to: '/settings', label: 'Settings', icon: IconSettings },
      ],
    }] : []),
    {
      label: 'You', items: [
        { to: '/account', label: 'Account & tokens', icon: IconKey },
        { to: '/help', label: 'Guides & docs', icon: IconBook },
      ],
    },
  ]
  const active = (to: string) => (to === '/' ? pathname === '/' : pathname.startsWith(to))

  const spotlightActions: SpotlightActionGroupData[] = [
    { group: 'Pages', actions: sections.flatMap((s) => s.items).map((i) => ({ id: i.to, label: i.label, leftSection: <i.icon size={18} />, onClick: () => navigate(i.to) })) },
    {
      group: 'Actions', actions: [
        ...(isAdmin ? [{ id: 'new-repo', label: 'Create repository', description: 'Guided setup for a hosted, proxy or group repository', leftSection: <IconPlus size={18} />, onClick: () => navigate('/repositories/new') }] : []),
        { id: 'new-token', label: 'Create API token', description: 'For Maven, npm, pip, Docker, CI…', leftSection: <IconKey size={18} />, onClick: () => navigate('/account?new=1') },
        { id: 'theme', label: `Switch to ${scheme === 'dark' ? 'light' : 'dark'} theme`, leftSection: scheme === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />, onClick: () => setColorScheme(scheme === 'dark' ? 'light' : 'dark') },
      ],
    },
    ...(hits.length ? [{
      group: 'Packages and artifacts', actions: [...new Map(hits.map((h) => [h.component ? `${h.repo}\u0000${h.component}\u0000${h.version}` : h.id, h])).values()].slice(0, 8).map((h) => ({
        id: 'hit-' + h.id, label: h.component ? `${h.component}${h.version ? ' ' + h.version : ''}` : h.path.split('/').pop()!, description: `${h.repo}${h.component ? '' : ' · ' + h.path}`,
        keywords: [paletteQuery], leftSection: <IconPackage size={18} />,
        onClick: () => navigate(h.component ? packageLink(h.repo, h.component, h.version) : assetLink(h)),
      })),
    }] : []),
    {
      group: 'Repositories', actions: (repos.data ?? []).map((r) => ({
        id: 'repo-' + r.name, label: r.name, description: `${FORMATS.find((f) => f.id === r.format)?.label} ${r.type}${r.description ? ' — ' + r.description : ''}`,
        leftSection: <FormatIcon format={r.format} />, onClick: () => navigate(`/repositories/${r.name}`),
      })),
    },
  ]

  const ctx: AppCtx = { user, logout, settings: settings.data, reloadSettings: settings.reload, reloadRepos: repos.reload }

  return (
    <AuthContext.Provider value={ctx}>
      <Spotlight actions={spotlightActions} nothingFound="Nothing found" highlightQuery limit={12} shortcut={['mod + K']}
        onSpotlightOpen={repos.reload} scrollable maxHeight={420} onQueryChange={setPaletteQuery}
        searchProps={{ leftSection: <IconSearch size={18} />, placeholder: 'Jump to a page, repository, package or action…' }} />
      <a href="#main" className="skip-link">Skip to content</a>
      <AppShell header={{ height: 60 }} navbar={{ width: 256, breakpoint: 'sm', collapsed: { mobile: !opened } }} padding={{ base: 'md', sm: 'xl' }}>
        <AppShell.Header px="md" className="app-header">
          <Group h="100%" justify="space-between" wrap="nowrap">
            <Group wrap="nowrap">
              <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" aria-label="Toggle navigation" />
              <Link to="/" aria-label="RepoMan dashboard" style={{ textDecoration: 'none', color: 'inherit' }}><Brand compact /></Link>
            </Group>
            <Group gap="sm" wrap="nowrap">
              <form role="search" onSubmit={(e) => { e.preventDefault(); navigate(`/search?q=${encodeURIComponent(q.trim())}`); setQ(''); searchRef.current?.blur() }}>
                <TextInput ref={searchRef} placeholder="Search artifacts" leftSection={<IconSearch size={16} />} value={q}
                  rightSection={<Kbd size="xs">/</Kbd>} onChange={(e) => setQ(e.currentTarget.value)} visibleFrom="md" w={260} radius="xl" aria-label="Search artifacts" />
              </form>
              <ActionIcon variant="default" size="lg" radius="xl" hiddenFrom="sm" onClick={spotlight.open} aria-label="Search and jump to">
                <IconSearch size={18} />
              </ActionIcon>
              <Tooltip label="Command palette">
                <Button variant="default" radius="xl" size="sm" onClick={spotlight.open} visibleFrom="sm" leftSection={<IconSearch size={14} />}
                  rightSection={<Kbd size="xs">{MOD_KEY} K</Kbd>} c="dimmed" fw={400}>Go to…</Button>
              </Tooltip>
              <Tooltip label={`Switch to ${scheme === 'dark' ? 'light' : 'dark'} theme`}>
                <ActionIcon variant="default" size="lg" radius="xl" aria-label="Toggle theme"
                  onClick={() => setColorScheme(scheme === 'dark' ? 'light' : 'dark')}>
                  {scheme === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
                </ActionIcon>
              </Tooltip>
              <Menu position="bottom-end" withArrow>
                <Menu.Target>
                  <UnstyledButton aria-label="Account menu">
                    <Group gap={8} wrap="nowrap">
                      <Avatar size="sm" color="brand" radius="xl">{user.username[0].toUpperCase()}</Avatar>
                      <Text size="sm" fw={500} visibleFrom="xs">{user.username}</Text>
                      <IconChevronDown size={14} />
                    </Group>
                  </UnstyledButton>
                </Menu.Target>
                <Menu.Dropdown miw={220}>
                  <Group gap="sm" px="sm" py="xs" wrap="nowrap">
                    <Avatar size="md" color="brand" radius="xl">{user.username[0].toUpperCase()}</Avatar>
                    <div style={{ minWidth: 0 }}>
                      <Text size="sm" fw={600} truncate="end">{user.username}</Text>
                      <Text size="xs" c="dimmed" tt="capitalize">{user.role}</Text>
                    </div>
                  </Group>
                  <Menu.Divider />
                  <Menu.Item leftSection={<IconKey size={16} />} onClick={() => navigate('/account')}>Account & tokens</Menu.Item>
                  <Menu.Item leftSection={<IconBook size={16} />} onClick={() => navigate('/help')}>Guides & docs</Menu.Item>
                  <Menu.Divider />
                  <Menu.Item color="red" leftSection={<IconLogout size={16} />} onClick={logout}>Sign out</Menu.Item>
                </Menu.Dropdown>
              </Menu>
            </Group>
          </Group>
        </AppShell.Header>

        <AppShell.Navbar p="sm" className="app-navbar" aria-label="Main navigation">
          <AppShell.Section grow component={ScrollArea}>
            {sections.map((s) => (
              <Stack gap={2} key={s.label} mb="md">
                <Text fw={600} c="dimmed" tt="uppercase" px="sm" mb={4} className="nav-section-label">{s.label}</Text>
                {s.items.map((n) => (
                  <NavLink key={n.to} component={Link} to={n.to} label={n.label} className="nav-link"
                    leftSection={<n.icon size={18} stroke={1.7} />} active={active(n.to)} onClick={close} />
                ))}
              </Stack>
            ))}
          </AppShell.Section>
          <AppShell.Section className="nav-footer">
            <Divider mb="sm" />
            <Text size="xs" c="dimmed" px="sm">Press <Kbd size="xs">{MOD_KEY}</Kbd> + <Kbd size="xs">K</Kbd> to jump anywhere</Text>
          </AppShell.Section>
        </AppShell.Navbar>

        <AppShell.Main id="main" tabIndex={-1} style={{ outline: 'none' }}>
          <div style={{ maxWidth: 1440, marginInline: 'auto' }}>
          {settings.data?.banner && bannerHidden !== settings.data.banner && (
            <Alert icon={<IconSpeakerphone />} color="brand" variant="light" mb="lg" withCloseButton closeButtonLabel="Dismiss announcement"
              onClose={() => setBannerHidden(settings.data?.banner)}>{settings.data.banner}</Alert>
          )}
          <ErrorBoundary resetKey={pathname}>
          <Suspense fallback={<Center py="xl"><Loader aria-label="Loading page" /></Center>}>
          <div className="page" key={pathname.split('/')[1]}>
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/repositories" element={<Repositories />} />
            {isAdmin && <Route path="/repositories/new" element={<RepoWizard />} />}
            <Route path="/repositories/:name/package" element={<PackagePage />} />
            <Route path="/repositories/:name/*" element={<RepoDetail />} />
            <Route path="/search" element={<Search />} />
            <Route path="/account" element={<Account />} />
            <Route path="/help/*" element={<Help />} />
            {isAdmin && <>
              <Route path="/users" element={<Users />} />
              <Route path="/notifications" element={<Notifications />} />
              <Route path="/webhooks" element={<Webhooks />} />
              <Route path="/tasks" element={<Tasks />} />
              <Route path="/storage" element={<Storage />} />
              <Route path="/audit" element={<AuditLog />} />
              <Route path="/settings/*" element={<SettingsPage />} />
            </>}
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
          </div>
          </Suspense>
          </ErrorBoundary>
          </div>
        </AppShell.Main>
      </AppShell>
    </AuthContext.Provider>
  )
}

function Login({ onLogin }: { onLogin: (u: User) => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | undefined>(() => new URLSearchParams(window.location.search).get('oidc_error') || undefined)
  const [busy, setBusy] = useState(false)
  const [sso, setSso] = useState<OidcPublic>()
  const [choosing, setChoosing] = useState(() => new URLSearchParams(window.location.search).get('oidc') === 'choose')

  useEffect(() => {
    api<OidcPublic>('/auth/oidc/config').then(setSso).catch(() => undefined)
    // keep the URL clean after an SSO redirect, but leave other queries alone: a deep link (a package page) must survive signing in
    const sp = new URLSearchParams(window.location.search)
    if (sp.has('oidc') || sp.has('oidc_error')) window.history.replaceState(null, '', window.location.pathname)
  }, [])

  const v = useValidation({ username: !username.trim() && 'Enter your username', password: !password && 'Enter your password' })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!v.check()) return
    setBusy(true)
    api<User>('/auth/login', { body: { username: username.trim(), password } })
      .then(onLogin)
      .catch((err: Error) => setError(err.message))
      .finally(() => setBusy(false))
  }

  return (
    <div className="login-bg">
      <Paper withBorder shadow="xl" w="100%" maw={900} radius="xl" style={{ overflow: 'hidden' }}>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing={0}>
          <Stack p={40} gap="xl" className="login-side" visibleFrom="sm" justify="space-between">
            <Brand />
            <div>
              <Title order={3}>One home for every artifact</Title>
              <Text size="sm" c="dimmed" mt={4}>Host, proxy and group repositories for all your package formats.</Text>
            </div>
            <Group gap="xs">
              {FORMATS.map((f) => (
                <Tooltip key={f.id} label={`${f.label}: ${f.tagline}`} withArrow>
                  <ThemeIcon variant="light" color={f.color} size={44} radius="md" tabIndex={0} aria-label={f.label}><f.icon size={24} /></ThemeIcon>
                </Tooltip>
              ))}
            </Group>
            <Group gap="xs" c="dimmed" wrap="nowrap" align="flex-start"><IconShieldLock size={16} style={{ flexShrink: 0 }} /><Text size="xs">Signed indexes · scoped tokens · audit trail · runs fully offline</Text></Group>
          </Stack>
          {choosing ? <SsoChooser onLogin={onLogin} onCancel={() => setChoosing(false)} /> : (
          <form onSubmit={submit} noValidate>
            <Stack p={{ base: 'lg', xs: 40 }} justify="center" h="100%">
              <Group hiddenFrom="sm" mb="sm"><Brand /></Group>
              <div>
                <Title order={2}>Welcome back</Title>
                <Text c="dimmed" size="sm">Use your RepoMan account</Text>
              </div>
              {error && <Alert color="red" variant="light" py="xs" role="alert">{error}</Alert>}
              <TextInput label="Username" size="md" value={username} onChange={(e) => { setUsername(e.currentTarget.value); setError(undefined) }} autoFocus
                autoComplete="username" autoCapitalize="none" spellCheck={false} error={v.error('username')} />
              <PasswordInput label="Password" size="md" value={password} onChange={(e) => { setPassword(e.currentTarget.value); setError(undefined) }}
                autoComplete="current-password" error={v.error('password') || !!error} />
              <Button type="submit" size="md" loading={busy} fullWidth mt="xs">Sign in</Button>
              {sso?.enabled && (
                <>
                  <Divider label="or" labelPosition="center" />
                  <Button component="a" href="/api/auth/oidc/login" size="md" variant="default" fullWidth leftSection={<IconLogin2 size={18} />}>
                    Sign in with {sso.label}
                  </Button>
                </>
              )}
              <Text size="xs" c="dimmed">First run? Sign in as <b>admin</b>. The password was printed in the server log and saved as <b>initial-admin-password</b> in the data directory. Locked out? Run <b>repoman reset-password</b> on the server.</Text>
            </Stack>
          </form>
          )}
        </SimpleGrid>
      </Paper>
    </div>
  )
}

/** Shown after the first SSO sign-in when account merging is on: link to an existing account, or create a new one. */
function SsoChooser({ onLogin, onCancel }: { onLogin: (u: User) => void; onCancel: () => void }) {
  const { data, error: gone } = useApi<{ email: string; username: string; canLink: boolean; canCreate: boolean }>('/auth/oidc/pending')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const v = useValidation({ username: !username.trim() && 'Enter your RepoMan username', password: !password && 'Enter your RepoMan password' })
  const complete = (body: Record<string, string>) => {
    setBusy(true)
    api<User>('/auth/oidc/complete', { body }).then(onLogin).catch((e: Error) => setError(e.message)).finally(() => setBusy(false))
  }
  if (gone) return (
    <Stack p={40} justify="center" h="100%">
      <Alert color="red" variant="light">Your single sign-on session expired. Please start again.</Alert>
      <Button onClick={onCancel}>Back to sign in</Button>
    </Stack>
  )
  if (!data) return <Center h="100%" mih={240}><Loader aria-label="Loading" /></Center>
  return (
    <form noValidate onSubmit={(e) => { e.preventDefault(); if (v.check()) complete({ action: 'link', username: username.trim(), password }) }}>
      <Stack p={{ base: 'lg', xs: 40 }} justify="center" h="100%">
        <div>
          <Title order={2}>Almost there</Title>
          <Text c="dimmed" size="sm">Signed in with SSO{data.email ? <> as <b>{data.email}</b></> : null}. Do you already have a RepoMan account?</Text>
        </div>
        {error && <Alert color="red" variant="light" py="xs" role="alert">{error}</Alert>}
        {data.canLink && (
          <>
            <TextInput label="Existing username" value={username} onChange={(e) => { setUsername(e.currentTarget.value); setError(undefined) }} autoComplete="username" autoFocus
              autoCapitalize="none" spellCheck={false} error={v.error('username')} />
            <PasswordInput label="Existing password" value={password} onChange={(e) => { setPassword(e.currentTarget.value); setError(undefined) }} autoComplete="current-password" error={v.error('password')} />
            <Button type="submit" loading={busy} fullWidth>Link and sign in</Button>
            <Text size="xs" c="dimmed">Your existing account keeps its role, permissions and tokens. Next time, SSO signs you straight in.</Text>
          </>
        )}
        {data.canCreate && (
          <>
            {data.canLink && <Divider label="or" labelPosition="center" />}
            <Button variant="default" loading={busy} fullWidth onClick={() => complete({ action: 'create' })}>Create a new account</Button>
          </>
        )}
        <Button variant="subtle" color="gray" size="xs" onClick={onCancel}>Cancel</Button>
      </Stack>
    </form>
  )
}

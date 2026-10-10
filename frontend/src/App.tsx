import {
  ActionIcon, Alert, AppShell, Avatar, Burger, Button, Center, Divider, Group, Kbd, Loader, Menu, NavLink, Paper, PasswordInput,
  ScrollArea, SimpleGrid, Stack, Text, TextInput, ThemeIcon, Title, Tooltip, UnstyledButton, useComputedColorScheme, useMantineColorScheme,
} from '@mantine/core'
import { useDisclosure, useHotkeys } from '@mantine/hooks'
import { Spotlight, spotlight, type SpotlightActionGroupData } from '@mantine/spotlight'
import {
  IconActivity, IconBell, IconBook, IconLogin2, IconBox, IconChevronDown, IconClockPlay, IconDatabase, IconKey, IconLayoutDashboard, IconLogout,
  IconMoon, IconPlus, IconSearch, IconSettings, IconShieldLock, IconSpeakerphone, IconSun, IconUsers, IconWebhook,
} from '@tabler/icons-react'
import { createContext, lazy, Suspense, useContext, useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { api, useApi, type OidcPublic, type Repo, type Settings, type User } from './api'
import { FormatIcon, FORMATS } from './formats'
import Dashboard from './pages/Dashboard'

const Account = lazy(() => import('./pages/Account'))
const AuditLog = lazy(() => import('./pages/Audit'))
const Help = lazy(() => import('./pages/Help'))
const Notifications = lazy(() => import('./pages/Notifications'))
const RepoDetail = lazy(() => import('./pages/RepoDetail'))
const Repositories = lazy(() => import('./pages/Repositories'))
const RepoWizard = lazy(() => import('./pages/RepoWizard'))
const Search = lazy(() => import('./pages/Search'))
const SettingsPage = lazy(() => import('./pages/Settings'))
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
  const [user, setUser] = useState<User | null>()

  useEffect(() => {
    api<User>('/auth/me').then(setUser).catch(() => setUser(null))
    const onUnauthorized = () => setUser(null)
    window.addEventListener('rm:unauthorized', onUnauthorized)
    return () => window.removeEventListener('rm:unauthorized', onUnauthorized)
  }, [])

  if (user === undefined) return <Center h="100vh"><Loader /></Center>
  if (user === null) return <Login onLogin={setUser} />
  return <Shell user={user} logout={() => api('/auth/logout', { method: 'POST' }).finally(() => setUser(null))} />
}

function Brand() {
  return (
    <Group gap="xs" wrap="nowrap">
      <div className="brand-mark"><IconBox size={20} /></div>
      <div>
        <Text fw={700} lh={1.1}>RepoMan</Text>
        <Text size="xs" c="dimmed" lh={1.1}>Repository Manager</Text>
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
        onSpotlightOpen={repos.reload} scrollable maxHeight={420}
        searchProps={{ leftSection: <IconSearch size={18} />, placeholder: 'Jump to a page, repository or action…' }} />
      <a href="#main" className="skip-link">Skip to content</a>
      <AppShell header={{ height: 60 }} navbar={{ width: 256, breakpoint: 'sm', collapsed: { mobile: !opened } }} padding={{ base: 'md', sm: 'xl' }}>
        <AppShell.Header px="md" className="app-header">
          <Group h="100%" justify="space-between" wrap="nowrap">
            <Group wrap="nowrap">
              <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" aria-label="Toggle navigation" />
              <Link to="/" style={{ textDecoration: 'none', color: 'inherit' }}><Brand /></Link>
            </Group>
            <Group gap="sm" wrap="nowrap">
              <form onSubmit={(e) => { e.preventDefault(); navigate(`/search?q=${encodeURIComponent(q)}`); searchRef.current?.blur() }}>
                <TextInput ref={searchRef} placeholder="Search artifacts" leftSection={<IconSearch size={16} />} value={q}
                  rightSection={<Kbd size="xs">/</Kbd>} onChange={(e) => setQ(e.currentTarget.value)} visibleFrom="md" w={260} radius="xl" aria-label="Search artifacts" />
              </form>
              <ActionIcon variant="default" size="lg" radius="xl" hiddenFrom="sm" onClick={spotlight.open} aria-label="Search and jump to">
                <IconSearch size={18} />
              </ActionIcon>
              <Tooltip label="Command palette">
                <Button variant="default" radius="xl" size="sm" onClick={spotlight.open} visibleFrom="sm" leftSection={<IconSearch size={14} />}
                  rightSection={<Kbd size="xs">Ctrl K</Kbd>} c="dimmed" fw={400}>Go to…</Button>
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
          <AppShell.Section>
            <Divider mb="sm" />
            <Text size="xs" c="dimmed" px="sm">Press <Kbd size="xs">Ctrl</Kbd> + <Kbd size="xs">K</Kbd> to jump anywhere</Text>
          </AppShell.Section>
        </AppShell.Navbar>

        <AppShell.Main id="main" tabIndex={-1} style={{ outline: 'none' }}>
          <div style={{ maxWidth: 1440, marginInline: 'auto' }}>
          {settings.data?.banner && bannerHidden !== settings.data.banner && (
            <Alert icon={<IconSpeakerphone />} color="brand" variant="light" mb="lg" withCloseButton closeButtonLabel="Dismiss announcement"
              onClose={() => setBannerHidden(settings.data?.banner)}>{settings.data.banner}</Alert>
          )}
          <Suspense fallback={<Center py="xl"><Loader /></Center>}>
          <div className="page" key={pathname.split('/')[1]}>
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/repositories" element={<Repositories />} />
            {isAdmin && <Route path="/repositories/new" element={<RepoWizard />} />}
            <Route path="/repositories/:name/*" element={<RepoDetail />} />
            <Route path="/search" element={<Search />} />
            <Route path="/account" element={<Account />} />
            <Route path="/help/*" element={<Help />} />
            {isAdmin && <>
              <Route path="/users" element={<Users />} />
              <Route path="/notifications" element={<Notifications />} />
              <Route path="/webhooks" element={<Webhooks />} />
              <Route path="/tasks" element={<Tasks />} />
              <Route path="/audit" element={<AuditLog />} />
              <Route path="/settings/*" element={<SettingsPage />} />
            </>}
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
          </div>
          </Suspense>
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
    if (window.location.search) window.history.replaceState(null, '', window.location.pathname) // keep the URL clean
  }, [])

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    api<User>('/auth/login', { body: { username, password } })
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
            <SimpleGrid cols={4} spacing="xs">
              {FORMATS.map((f) => (
                <Tooltip key={f.id} label={`${f.label}: ${f.tagline}`} withArrow>
                  <ThemeIcon variant="light" color={f.color} size={44} radius="md"><f.icon size={24} /></ThemeIcon>
                </Tooltip>
              ))}
            </SimpleGrid>
            <Group gap="xs" c="dimmed"><IconShieldLock size={16} /><Text size="xs">Signed indexes · scoped tokens · audit trail · runs fully offline</Text></Group>
          </Stack>
          {choosing ? <SsoChooser onLogin={onLogin} onCancel={() => setChoosing(false)} /> : (
          <form onSubmit={submit}>
            <Stack p={40} justify="center" h="100%">
              <Group hiddenFrom="sm" mb="sm"><Brand /></Group>
              <div>
                <Title order={2}>Welcome back</Title>
                <Text c="dimmed" size="sm">Use your RepoMan account</Text>
              </div>
              {error && <Alert color="red" variant="light" py="xs" role="alert">{error}</Alert>}
              <TextInput label="Username" size="md" value={username} onChange={(e) => { setUsername(e.currentTarget.value); setError(undefined) }} autoFocus required autoComplete="username" />
              <PasswordInput label="Password" size="md" value={password} onChange={(e) => { setPassword(e.currentTarget.value); setError(undefined) }} required autoComplete="current-password" error={!!error} />
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
  if (!data) return <Center><Loader /></Center>
  return (
    <form onSubmit={(e) => { e.preventDefault(); complete({ action: 'link', username, password }) }}>
      <Stack p={40} justify="center" h="100%">
        <div>
          <Title order={2}>Almost there</Title>
          <Text c="dimmed" size="sm">Signed in with SSO{data.email ? <> as <b>{data.email}</b></> : null}. Do you already have a RepoMan account?</Text>
        </div>
        {error && <Alert color="red" variant="light" py="xs" role="alert">{error}</Alert>}
        {data.canLink && (
          <>
            <TextInput label="Existing username" value={username} onChange={(e) => { setUsername(e.currentTarget.value); setError(undefined) }} required autoComplete="username" autoFocus />
            <PasswordInput label="Existing password" value={password} onChange={(e) => { setPassword(e.currentTarget.value); setError(undefined) }} required autoComplete="current-password" />
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

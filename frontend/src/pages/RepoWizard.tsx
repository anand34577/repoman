import { Alert, Button, Card, Group, SimpleGrid, Stack, Stepper, Table, Text, ThemeIcon, Tooltip } from '@mantine/core'
import { IconArrowLeft, IconArrowRight, IconCheck, IconInfoCircle } from '@tabler/icons-react'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../App'
import { api, useApi, type Format, type Repo, type RepoType } from '../api'
import { FORMATS, formatInfo, TYPES } from '../formats'
import { FormatBadge, notifyError, notifyOk, PageHeader, TypeBadge, useUnsavedGuard } from '../util'
import { NAME_RE, RepoSettingsFields } from './RepoConfig'

const blank: Repo = {
  name: '', format: 'maven', type: 'hosted', metadataTtl: 60, allowRedeploy: false, cleanupDays: 0, retainVersions: 0, quotaMb: 0,
  offline: false, private: false, createdAt: 0, members: [], remoteUrl: '',
}

export default function RepoWizard() {
  const navigate = useNavigate()
  const { reloadRepos } = useAuth()
  const repos = useApi<Repo[]>('/repositories')
  const [step, setStep] = useState(0)
  const [r, setR] = useState<Repo>(blank)
  const [busy, setBusy] = useState(false)
  const set = (p: Partial<Repo>) => setR((cur) => ({ ...cur, ...p }))
  const all = repos.data ?? []
  const f = formatInfo(r.format)

  const pickFormat = (format: Format) => { set({ format }); setStep(1) }
  const pickType = (type: RepoType) => {
    const fi = formatInfo(r.format)
    const suggested = `${r.format}-${type}`
    const taken = all.some((x) => x.name === suggested)
    const sameFormat = all.filter((x) => x.format === r.format && x.type !== 'group')
    set({
      type,
      name: taken ? '' : suggested,
      remoteUrl: type === 'proxy' ? fi.presets[0]?.url ?? '' : '',
      allowRedeploy: type === 'hosted' && (r.format === 'docker' || r.format === 'raw'),
      distribution: r.format === 'apt' && type === 'hosted' ? 'stable' : undefined,
      // sensible default: hosted members first, then proxies
      members: type === 'group' ? [...sameFormat.filter((x) => x.type === 'hosted'), ...sameFormat.filter((x) => x.type === 'proxy')].map((x) => x.name) : [],
    })
    setStep(2)
  }

  const configError =
    !NAME_RE.test(r.name) ? 'Enter a valid name'
      : all.some((x) => x.name === r.name) ? 'Name already in use'
        : r.type === 'proxy' && !/^https?:\/\/.+/.test(r.remoteUrl ?? '') ? 'Enter the remote URL'
          : r.type === 'group' && !r.members?.length ? 'Add at least one member' : undefined

  // from the configuration step on there is typed input to lose
  const leaveFreely = useUnsavedGuard(step >= 2)
  const create = () => {
    setBusy(true)
    api<Repo>('/repositories', { body: r })
      .then((saved) => {
        notifyOk(`Repository ${saved.name} is ready. Follow the steps below to connect a client.`, 'Repository created')
        reloadRepos()
        leaveFreely()
        navigate(`/repositories/${saved.name}/setup`)
      })
      .catch(notifyError)
      .finally(() => setBusy(false))
  }

  const summary: [string, React.ReactNode][] = [
    ['Name', <Text fw={600}>{r.name}</Text>],
    ['Format', <FormatBadge format={r.format} />],
    ['Type', <TypeBadge type={r.type} />],
    ...(r.type === 'proxy' ? [['Remote', r.remoteUrl], ['Metadata cache', r.metadataTtl ? `${r.metadataTtl} minutes` : 'Always re-check'], ['Remote credentials', r.remoteUsername ? r.remoteUsername : 'None']] as [string, string][] : []),
    ...(r.type === 'group' ? [['Members (in order)', r.members?.join(' → ')]] as [string, string][] : []),
    ...(r.type === 'hosted' ? [['Redeploy', r.allowRedeploy ? 'Allowed' : 'Disabled'], ['Quota', r.quotaMb ? `${r.quotaMb} MB` : 'Unlimited']] as [string, string][] : []),
    ...(r.format === 'apt' && r.type === 'hosted' ? [['Distribution', r.distribution]] as [string, string | undefined][] : []),
    ...(r.type !== 'group' ? [['Cleanup', [r.cleanupDays && `unused for ${r.cleanupDays} days`, r.retainVersions && `keep latest ${r.retainVersions} versions`].filter(Boolean).join(', ') || 'Off']] as [string, string][] : []),
    ['Visibility', r.private ? 'Private (sign-in required)' : 'Follows the server anonymous-access setting'],
  ]

  return (
    <>
      <PageHeader title="Create repository" description="Answer a few questions; RepoMan suggests sensible defaults for everything else." />
      <Stepper active={step} onStepClick={(s) => s < step && setStep(s)} allowNextStepsSelect={false} mb="xl" size="sm">
        <Stepper.Step label="Format" description={step > 0 ? f.label : 'What will you store?'} />
        <Stepper.Step label="Type" description={step > 1 ? TYPES[r.type].label : 'Host, cache or combine'} />
        <Stepper.Step label="Configure" description="Name and options" />
        <Stepper.Step label="Review" description="Check and create" />
      </Stepper>

      {step === 0 && (
        <SimpleGrid cols={{ base: 1, xs: 2, md: 4 }}>
          {FORMATS.map((x) => (
            <Card key={x.id} padding="lg" className="select-card" data-selected={r.format === x.id || undefined} onClick={() => pickFormat(x.id)}
              role="button" tabIndex={0} onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), pickFormat(x.id))}>
              <ThemeIcon size={44} radius="md" variant="light" color={x.color}><x.icon size={26} /></ThemeIcon>
              <Text fw={600} mt="sm">{x.label}</Text>
              <Text size="sm" c="dimmed">{x.tagline}</Text>
              <Text size="xs" c="dimmed" mt="xs">Works with {x.clients}</Text>
            </Card>
          ))}
        </SimpleGrid>
      )}

      {step === 1 && (
        <Stack>
          <SimpleGrid cols={{ base: 1, md: 3 }}>
            {(['hosted', 'proxy', 'group'] as RepoType[]).map((t) => {
              const ok = f.types.includes(t)
              const help = t === 'hosted' ? f.hostedHelp : t === 'proxy' ? f.proxyHelp : f.groupHelp
              const card = (
                <Card key={t} padding="lg" className="select-card" data-disabled={!ok || undefined} data-selected={(ok && r.type === t) || undefined}
                  onClick={() => ok && pickType(t)} role="button" aria-disabled={!ok} tabIndex={ok ? 0 : -1}
                  onKeyDown={(e) => ok && (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), pickType(t))} h="100%">
                  <TypeBadge type={t} />
                  <Text fw={600} mt="sm">{TYPES[t].summary}</Text>
                  <Text size="sm" c="dimmed" mt={4}>{TYPES[t].detail}</Text>
                  {help && <Text size="sm" mt="sm"><b>For {f.label}:</b> {help}</Text>}
                </Card>
              )
              return ok ? card : (
                <Tooltip key={t} label={`${f.label} indexes are signed per repository and can't be merged. Configure clients with several sources instead.`} multiline maw={300}>
                  <div>{card}</div>
                </Tooltip>
              )
            })}
          </SimpleGrid>
          <Alert variant="light" icon={<IconInfoCircle />}>
            A common setup per format is <b>one hosted</b> repository for your own artifacts, <b>one proxy</b> for the public registry,
            and <b>one group</b> combining both that everyone points their tools at.
          </Alert>
          <Group><Button variant="default" leftSection={<IconArrowLeft size={16} />} onClick={() => setStep(0)}>Back</Button></Group>
        </Stack>
      )}

      {step === 2 && (
        <Stack maw={820}>
          <RepoSettingsFields repo={r} set={set} repos={all} />
          <Group justify="space-between">
            <Button variant="default" leftSection={<IconArrowLeft size={16} />} onClick={() => setStep(1)}>Back</Button>
            <Tooltip label={configError} disabled={!configError}>
              <Button rightSection={<IconArrowRight size={16} />} disabled={!!configError} onClick={() => setStep(3)}>Review</Button>
            </Tooltip>
          </Group>
        </Stack>
      )}

      {step === 3 && (
        <Stack maw={820}>
          <Card padding="lg">
            <Text fw={600} mb="sm">Ready to create</Text>
            <Table variant="vertical" layout="fixed" withTableBorder>
              <Table.Tbody>
                {summary.map(([k, v]) => <Table.Tr key={k}><Table.Th w={200}>{k}</Table.Th><Table.Td style={{ wordBreak: 'break-all' }}>{v}</Table.Td></Table.Tr>)}
              </Table.Tbody>
            </Table>
            <Text size="sm" c="dimmed" mt="md">Next you'll get copy-paste instructions for {f.clients}.</Text>
          </Card>
          <Group justify="space-between">
            <Button variant="default" leftSection={<IconArrowLeft size={16} />} onClick={() => setStep(2)}>Back</Button>
            <Button leftSection={<IconCheck size={16} />} loading={busy} onClick={create}>Create repository</Button>
          </Group>
        </Stack>
      )}
    </>
  )
}

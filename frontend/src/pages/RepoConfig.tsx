import {
  ActionIcon, Alert, Badge, Button, Chip, Collapse, Group, NumberInput, Paper, PasswordInput, SegmentedControl, Select, Stack,
  Switch, Text, TextInput, Textarea, Tooltip,
} from '@mantine/core'
import { IconArrowDown, IconArrowUp, IconCircleCheck, IconCircleX, IconPlugConnected, IconX } from '@tabler/icons-react'
import { useState } from 'react'
import { api, type Repo, type RemoteTest } from '../api'
import { formatInfo, FormatIcon } from '../formats'
import { HelpLabel, notifyError, Section, TypeBadge } from '../util'

export const NAME_RE = /^[a-z0-9][a-z0-9._-]{0,63}$/

const ttlPresets = [
  { value: '0', label: 'Always check' }, { value: '5', label: '5 min' }, { value: '60', label: '1 hour' }, { value: '1440', label: '1 day' },
]

/** Configuration sections for a repository; `editing` hides immutable fields. */
export function RepoSettingsFields({ repo: r, set, repos, editing }: {
  repo: Repo; set: (p: Partial<Repo>) => void; repos: Repo[]; editing?: boolean
}) {
  const f = formatInfo(r.format)
  const nameError = r.name && !NAME_RE.test(r.name) ? 'Use lowercase letters, digits, ".", "_" or "-" (start with a letter or digit)'
    : !editing && repos.some((x) => x.name === r.name) ? 'A repository with this name already exists' : undefined

  return (
    <Stack>
      <Section title="General" description="How this repository is identified. The name becomes part of its URL.">
        {!editing && (
          <TextInput label="Name" required placeholder={`${r.format}-${r.type}`} value={r.name} error={nameError}
            description={`Clients will use …/${r.format === 'docker' ? '' : 'repository/'}${r.name || `${r.format}-${r.type}`}`}
            onChange={(e) => set({ name: e.currentTarget.value.toLowerCase().replace(/\s+/g, '-') })} />
        )}
        <Textarea label="Description" placeholder="What is this repository for? Who owns it?" autosize minRows={1}
          value={r.description ?? ''} onChange={(e) => set({ description: e.currentTarget.value })} />
      </Section>

      {r.type === 'proxy' && <RemoteSection repo={r} set={set} />}
      {r.type === 'group' && <MembersSection repo={r} set={set} repos={repos} />}

      {r.type === 'hosted' && (
        <Section title="Deployment policy" description="What happens when someone uploads a file that already exists.">
          <SegmentedControl value={r.allowRedeploy ? 'allow' : 'deny'} onChange={(v) => set({ allowRedeploy: v === 'allow' })} data={[
            { value: 'deny', label: 'Disable redeploy (immutable releases)' },
            { value: 'allow', label: 'Allow redeploy' },
          ]} />
          <Text size="sm" c="dimmed">
            {r.allowRedeploy
              ? 'Existing files, versions and tags can be overwritten. Convenient for snapshots, latest tags and scratch repositories.'
              : 'Published versions can never change, so builds stay reproducible. Recommended for releases.'}
            {r.format === 'maven' && ' Maven SNAPSHOTs and maven-metadata.xml can always be updated.'}
          </Text>
          {r.format === 'apt' && (
            <TextInput label={<HelpLabel label="Distribution" help="The suite name clients put in their sources line, e.g. stable, bookworm or jammy." />}
              value={r.distribution ?? 'stable'} onChange={(e) => set({ distribution: e.currentTarget.value })} />
          )}
        </Section>
      )}

      {r.type !== 'group' && (
        <Section title="Storage & cleanup" description={r.type === 'proxy'
          ? 'Keep the cache lean by removing artifacts nobody uses anymore. They are re-downloaded on demand.'
          : 'Control how much this repository can grow and remove what you no longer need.'}>
          {r.type === 'hosted' && (
            <NumberInput label={<HelpLabel label="Storage quota (MB)" help="Uploads are refused once the repository reaches this size. 0 means unlimited." />}
              min={0} value={r.quotaMb} onChange={(v) => set({ quotaMb: Number(v) || 0 })} placeholder="0 = unlimited" />
          )}
          <Group grow align="flex-start">
            <NumberInput label={<HelpLabel label="Delete if unused for (days)" help="Artifacts that haven't been downloaded or updated in this many days are deleted by the hourly cleanup task. 0 disables." />}
              min={0} value={r.cleanupDays} onChange={(v) => set({ cleanupDays: Number(v) || 0 })} />
            <NumberInput label={<HelpLabel label="Keep latest versions" help="Keep only the newest N versions of each component (e.g. each npm package, Maven artifact or Docker image). 0 keeps all." />}
              min={0} value={r.retainVersions} onChange={(v) => set({ retainVersions: Number(v) || 0 })} />
          </Group>
          {(r.cleanupDays > 0 || r.retainVersions > 0) && (
            <Text size="xs" c="dimmed">Use “Preview cleanup” in the repository's Actions menu to see what would be removed before it happens.</Text>
          )}
        </Section>
      )}

      <Section title="Access & availability">
        <Switch checked={r.private} onChange={(e) => set({ private: e.currentTarget.checked })} label="Private repository"
          description="Never readable without signing in, even when anonymous access is enabled server-wide." />
        {editing && (
          <Switch checked={r.offline} color="orange" onChange={(e) => set({ offline: e.currentTarget.checked })} label="Offline"
            description="Refuse all client requests (e.g. during maintenance). Groups skip offline members." />
        )}
        <Text size="xs" c="dimmed">Grant individual users more or less access on the repository's Access tab.</Text>
      </Section>
      <Text size="xs" c="dimmed">
        <FormatIcon format={r.format} size={12} /> {f.label} · <TypeBadge type={r.type} /> — format and type can't be changed after creation.
      </Text>
    </Stack>
  )
}

function RemoteSection({ repo: r, set }: { repo: Repo; set: (p: Partial<Repo>) => void }) {
  const f = formatInfo(r.format)
  const [auth, setAuth] = useState(!!r.remoteUsername)
  const [test, setTest] = useState<RemoteTest>()
  const [testing, setTesting] = useState(false)
  const runTest = () => {
    setTesting(true)
    api<RemoteTest>('/repositories/test-remote', { body: r }).then(setTest).catch(notifyError).finally(() => setTesting(false))
  }
  const ttl = String(r.metadataTtl)

  return (
    <Section title="Remote" description="The upstream repository this proxy caches. Artifacts are fetched on first request and then served locally.">
      {f.presets.length > 0 && (
        <div>
          <Text size="sm" fw={500} mb={6}>Popular upstreams</Text>
          <Group gap="xs">
            {f.presets.map((p) => (
              <Chip key={p.url} size="sm" checked={r.remoteUrl === p.url} onChange={() => { set({ remoteUrl: p.url }); setTest(undefined) }}>{p.label}</Chip>
            ))}
          </Group>
        </div>
      )}
      <Group align="flex-end" wrap="nowrap">
        <TextInput flex={1} label="Remote URL" required placeholder="https://" value={r.remoteUrl ?? ''}
          description={r.format === 'yum' ? 'A baseurl containing repodata/ (mirrorlists and metalinks are not supported).' : r.format === 'apt' ? 'The archive root containing dists/ and pool/.' : r.format === 'conda' ? 'The channel URL that contains noarch/, e.g. https://conda.anaconda.org/conda-forge' : undefined}
          onChange={(e) => { set({ remoteUrl: e.currentTarget.value }); setTest(undefined) }} />
        <Button variant="default" leftSection={<IconPlugConnected size={16} />} onClick={runTest} loading={testing} disabled={!r.remoteUrl}>Test connection</Button>
      </Group>
      {test && (
        <Alert color={test.ok ? 'teal' : 'red'} icon={test.ok ? <IconCircleCheck /> : <IconCircleX />} py="xs">
          {test.message} {test.status && <Text span size="xs" c="dimmed">({test.status}, {test.latencyMs} ms)</Text>}
        </Alert>
      )}
      <Switch checked={auth} onChange={(e) => { setAuth(e.currentTarget.checked); if (!e.currentTarget.checked) set({ remoteUsername: '', remotePassword: '' }) }}
        label="The remote requires authentication" description="e.g. a private registry, GitHub Packages, or Docker Hub with a paid account to avoid rate limits." />
      <Collapse in={auth}>
        <Group grow>
          <TextInput label="Username" value={r.remoteUsername ?? ''} onChange={(e) => set({ remoteUsername: e.currentTarget.value })} />
          <PasswordInput label="Password or token" placeholder={r.createdAt ? 'Leave blank to keep the saved one' : ''} value={r.remotePassword ?? ''}
            onChange={(e) => set({ remotePassword: e.currentTarget.value })} />
        </Group>
      </Collapse>
      <div>
        <Text size="sm" fw={500}><HelpLabel label="Metadata cache time" help="Indexes, tag lists and package listings change upstream. They are re-checked after this long. Artifact files never change and are cached permanently." /></Text>
        <Group mt={6} align="center">
          <SegmentedControl size="xs" value={ttlPresets.some((p) => p.value === ttl) ? ttl : 'custom'}
            onChange={(v) => v !== 'custom' && set({ metadataTtl: Number(v) })}
            data={[...ttlPresets, { value: 'custom', label: 'Custom' }]} />
          <NumberInput w={140} size="xs" min={0} suffix=" min" value={r.metadataTtl} onChange={(v) => set({ metadataTtl: Number(v) || 0 })} aria-label="Metadata cache minutes" />
        </Group>
      </div>
    </Section>
  )
}

function MembersSection({ repo: r, set, repos }: { repo: Repo; set: (p: Partial<Repo>) => void; repos: Repo[] }) {
  const members = r.members ?? []
  const available = repos.filter((x) => x.format === r.format && x.name !== r.name && !members.includes(x.name))
  const move = (i: number, d: number) => {
    const m = [...members]
    ;[m[i], m[i + d]] = [m[i + d], m[i]]
    set({ members: m })
  }
  return (
    <Section title="Members" description="Requests are tried against members from top to bottom; the first one that has the artifact wins. Put hosted repositories first so your own packages take priority.">
      <Select placeholder={available.length ? 'Add a repository…' : `No other ${formatInfo(r.format).label} repositories to add`} data={available.map((x) => ({ value: x.name, label: `${x.name} (${x.type})` }))}
        value={null} onChange={(v) => v && set({ members: [...members, v] })} searchable disabled={!available.length} />
      {members.length === 0 ? (
        <Text size="sm" c="dimmed">No members yet.</Text>
      ) : (
        <Stack gap={6}>
          {members.map((m, i) => {
            const mr = repos.find((x) => x.name === m)
            return (
              <Paper key={m} withBorder p="xs" radius="md">
                <Group justify="space-between" wrap="nowrap">
                  <Group gap="sm" wrap="nowrap">
                    <Badge variant="filled" color="gray" circle>{i + 1}</Badge>
                    <Text size="sm" fw={500}>{m}</Text>
                    {mr && <TypeBadge type={mr.type} />}
                  </Group>
                  <Group gap={2}>
                    <Tooltip label="Move up"><ActionIcon variant="subtle" color="gray" disabled={i === 0} onClick={() => move(i, -1)} aria-label="Move up"><IconArrowUp size={16} /></ActionIcon></Tooltip>
                    <Tooltip label="Move down"><ActionIcon variant="subtle" color="gray" disabled={i === members.length - 1} onClick={() => move(i, 1)} aria-label="Move down"><IconArrowDown size={16} /></ActionIcon></Tooltip>
                    <Tooltip label="Remove"><ActionIcon variant="subtle" color="red" onClick={() => set({ members: members.filter((x) => x !== m) })} aria-label="Remove member"><IconX size={16} /></ActionIcon></Tooltip>
                  </Group>
                </Group>
              </Paper>
            )
          })}
        </Stack>
      )}
    </Section>
  )
}

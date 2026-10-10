import {
  ActionIcon, Alert, Badge, Button, Card, Group, Stack, Text, TextInput, ThemeIcon, Title, Tooltip,
} from '@mantine/core'
import { useDocumentTitle } from '@mantine/hooks'
import { modals } from '@mantine/modals'
import { notifications } from '@mantine/notifications'
import { IconAlertCircle, IconCheck, IconCopy, IconInfoCircle, IconRefresh } from '@tabler/icons-react'
import { useState, type ReactNode } from 'react'
import type { Format, Repo, RepoType } from './api'
import { formatInfo, FormatIcon } from './formats'

export function formatBytes(n = 0) {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let i = -1
  do { n /= 1024; i++ } while (n >= 1024 && i < units.length - 1)
  return `${n.toFixed(n < 10 ? 1 : 0)} ${units[i]}`
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
export function timeAgo(unix: number) {
  if (!unix) return 'never'
  const s = unix - Date.now() / 1000
  for (const [unit, sec] of [['year', 31536000], ['month', 2592000], ['day', 86400], ['hour', 3600], ['minute', 60]] as const) {
    if (Math.abs(s) >= sec) return rtf.format(Math.round(s / sec), unit)
  }
  return s > 0 ? 'in a moment' : 'just now'
}

export const fullDate = (unix: number) => (unix ? new Date(unix * 1000).toLocaleString() : '—')

export const typeColor: Record<RepoType, string> = { hosted: 'teal', proxy: 'grape', group: 'indigo' }

export const FormatBadge = ({ format }: { format: Format }) => (
  <Badge variant="light" color={formatInfo(format).color} radius="sm" leftSection={<FormatIcon format={format} size={12} />}>
    {formatInfo(format).label}
  </Badge>
)
export const TypeBadge = ({ type }: { type: RepoType }) => (
  <Badge variant="dot" color={typeColor[type]} radius="sm">{type}</Badge>
)

const statusInfo = {
  online: { color: 'teal', label: 'Online', tip: 'Serving requests normally' },
  degraded: { color: 'orange', label: 'Remote unavailable', tip: 'The upstream failed recently; cached content is still served' },
  offline: { color: 'gray', label: 'Offline', tip: 'Taken offline by an administrator; all requests are refused' },
}
export function StatusBadge({ repo }: { repo: Repo }) {
  const s = statusInfo[repo.status ?? 'online']
  return (
    <Tooltip label={repo.health?.lastError && repo.status === 'degraded' ? repo.health.lastError : s.tip} multiline maw={360} withArrow>
      <Badge variant="light" color={s.color} radius="sm">{s.label}</Badge>
    </Tooltip>
  )
}

/** Copies text; falls back to execCommand where the Clipboard API is unavailable (plain-HTTP deployments). */
export async function copyText(value: string) {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(value)
  const ta = document.createElement('textarea')
  ta.value = value
  ta.setAttribute('readonly', '')
  ta.style.cssText = 'position:fixed;opacity:0;pointer-events:none'
  document.body.appendChild(ta)
  ta.select()
  const ok = document.execCommand('copy')
  ta.remove()
  if (!ok) throw new Error('Copy failed — select the text and copy it manually')
}

export function Copy({ value, label = 'Copy' }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => copyText(value).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500) }).catch(notifyError)
  return (
    <Tooltip label={copied ? 'Copied' : label}>
      <ActionIcon variant="subtle" color={copied ? 'teal' : 'gray'} onClick={copy} aria-label={label}>
        {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
      </ActionIcon>
    </Tooltip>
  )
}

/** A form label with an inline "what's this?" tooltip. */
export function HelpLabel({ label, help }: { label: string; help: string }) {
  return (
    <Group gap={4} wrap="nowrap" component="span" display="inline-flex">
      {label}
      <Tooltip label={help} multiline maw={320} withArrow>
        <IconInfoCircle size={14} style={{ color: 'var(--mantine-color-dimmed)', cursor: 'help' }} />
      </Tooltip>
    </Group>
  )
}

export const notifyError = (e: Error) => notifications.show({ color: 'red', title: 'Something went wrong', message: e.message })
export const notifyOk = (message: string, title?: string) => notifications.show({ color: 'teal', title, message, icon: <IconCheck size={18} /> })

export function PageHeader({ title, description, action, eyebrow }: { title: string; description?: ReactNode; action?: ReactNode; eyebrow?: string }) {
  useDocumentTitle(`${title} · RepoMan`)
  return (
    <Group justify="space-between" mb="xl" align="flex-end" gap="md">
      <div style={{ minWidth: 0 }}>
        {eyebrow && <Text size="xs" fw={600} tt="uppercase" c="brand" className="page-eyebrow" mb={4}>{eyebrow}</Text>}
        <Title order={2}>{title}</Title>
        {description && <Text c="dimmed" size="sm" maw={720} mt={4}>{description}</Text>}
      </div>
      {action}
    </Group>
  )
}

/** Shown when a data request fails, with a retry. */
export function LoadError({ error, retry }: { error?: string; retry?: () => void }) {
  if (!error) return null
  return (
    <Alert color="red" variant="light" icon={<IconAlertCircle />} title="Couldn't load this data" mb="md">
      <Group justify="space-between" gap="sm">
        <Text size="sm">{error}</Text>
        {retry && <Button size="xs" variant="white" color="red" leftSection={<IconRefresh size={14} />} onClick={retry}>Try again</Button>}
      </Group>
    </Alert>
  )
}

export function EmptyState({ icon, title, children, action }: { icon: ReactNode; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <Stack align="center" gap="xs" py={48} px="md" ta="center">
      <ThemeIcon size={60} radius="xl" variant="light">{icon}</ThemeIcon>
      <Text fw={600} mt="xs">{title}</Text>
      {children && <Text size="sm" c="dimmed" maw={460}>{children}</Text>}
      {action && <div style={{ marginTop: 8 }}>{action}</div>}
    </Stack>
  )
}

/** A titled card that groups related form fields. */
export function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <Card padding="lg">
      <Text fw={600} size="md">{title}</Text>
      {description && <Text size="sm" c="dimmed" mt={2} mb="md">{description}</Text>}
      <Stack gap="md" mt={description ? 0 : 'md'}>{children}</Stack>
    </Card>
  )
}

function TypedConfirm({ name, body, label, onConfirm }: { name: string; body: ReactNode; label: string; onConfirm: () => Promise<unknown> }) {
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  return (
    <Stack>
      {body}
      <TextInput label={<>Type <b>{name}</b> to confirm</>} value={typed} onChange={(e) => setTyped(e.currentTarget.value)} autoFocus data-autofocus />
      <Group justify="flex-end">
        <Button variant="default" onClick={() => modals.closeAll()}>Cancel</Button>
        <Button color="red" disabled={typed !== name} loading={busy}
          onClick={() => { setBusy(true); onConfirm().then(() => modals.closeAll()).catch(notifyError).finally(() => setBusy(false)) }}>
          {label}
        </Button>
      </Group>
    </Stack>
  )
}

/** Destructive confirmation that requires typing the resource name. */
export function confirmTyped(opts: { title: string; name: string; body: ReactNode; label: string; onConfirm: () => Promise<unknown> }) {
  modals.open({ title: <Text fw={600}>{opts.title}</Text>, children: <TypedConfirm {...opts} /> })
}

export function confirmAction(opts: { title: string; body: ReactNode; label: string; danger?: boolean; onConfirm: () => void }) {
  modals.openConfirmModal({
    title: <Text fw={600}>{opts.title}</Text>,
    children: typeof opts.body === 'string' ? <Text size="sm">{opts.body}</Text> : opts.body,
    labels: { confirm: opts.label, cancel: 'Cancel' },
    confirmProps: { color: opts.danger ? 'red' : undefined },
    onConfirm: opts.onConfirm,
  })
}

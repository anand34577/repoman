import {
  ActionIcon, Alert, Badge, Button, Card, Group, Pagination, Select, Stack, Text, TextInput, ThemeIcon, Title, Tooltip,
} from '@mantine/core'
import { useDocumentTitle } from '@mantine/hooks'
import { modals } from '@mantine/modals'
import { notifications } from '@mantine/notifications'
import { IconAlertCircle, IconCheck, IconCopy, IconInfoCircle, IconRefresh } from '@tabler/icons-react'
import { Component, useEffect, useRef, useState, type ReactNode } from 'react'
import { useBlocker, useRouteError, useSearchParams } from 'react-router-dom'
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

/** "3 hours ago", with the exact date and time one hover (or focus) away. */
export function When({ unix, never = 'never', prefix = '' }: { unix: number; never?: string; prefix?: string }) {
  if (!unix) return <Text size="sm" c="dimmed" span>{never}</Text>
  return (
    <Tooltip label={fullDate(unix)}>
      <Text size="sm" c="dimmed" span tabIndex={0} style={{ whiteSpace: 'nowrap' }}>{prefix}{timeAgo(unix)}</Text>
    </Tooltip>
  )
}

/** The platform's command key, for shortcut hints. */
export const MOD_KEY = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl'

export const plural = (n: number, one: string, many = one + 's') => `${n.toLocaleString()} ${n === 1 ? one : many}`

/**
 * Inline form validation. Pass each field's current problem (or a falsy value); messages show after the
 * first submit attempt and clear as the field is fixed. Forms use this with `noValidate`, so people get
 * the same styled message everywhere instead of the browser's own pop-up bubbles.
 */
export function useValidation<K extends string>(problems: Record<K, string | false | null | undefined>) {
  const [tried, setTried] = useState(false)
  return {
    error: (field: K) => (tried && problems[field]) || undefined,
    /** Call on submit: reveals the messages and reports whether the form may be sent. */
    check: () => { setTried(true); return !Object.values(problems).some(Boolean) },
    reset: () => setTried(false),
  }
}

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
      {action && <div className="page-actions">{action}</div>}
    </Group>
  )
}

/** Server counts stop here (see maxCount in the backend); past it the UI says "10,000+". */
export const MAX_COUNT = 10000
const PAGE_SIZES = [25, 50, 100, 250]

/** Page and page size live in the URL (?page=&size=), so a page can be shared, reloaded and reached with Back. */
export function usePaging(defaultSize = 50) {
  const [params, setParams] = useSearchParams()
  const page = Math.max(1, Number(params.get('page')) || 1)
  const size = PAGE_SIZES.includes(Number(params.get('size'))) ? Number(params.get('size')) : defaultSize
  return {
    page, size, offset: (page - 1) * size,
    query: `offset=${(page - 1) * size}&limit=${size}`,
    setPage: (n: number, replace = false) => n !== page && setParams((p) => { if (n > 1) p.set('page', String(n)); else p.delete('page'); return p }, { replace }),
    setSize: (n: number) => setParams((p) => { p.set('size', String(n)); p.delete('page'); return p }, { replace: true }),
  }
}

/** "1–50 of 12,345", a page-size picker and page buttons, under a paged table. */
export function Pager({ paging, total, noun = 'items' }: { paging: ReturnType<typeof usePaging>; total?: number; noun?: string }) {
  const { page, size, setPage, setSize } = paging
  const pages = Math.max(1, Math.ceil(Math.min(total ?? 0, MAX_COUNT) / size))
  // filters or deletions can leave the URL past the last page
  useEffect(() => { if (total !== undefined && page > pages) setPage(pages, true) }, [total, page, pages])
  if (!total) return null
  const capped = total > MAX_COUNT
  const from = Math.min((page - 1) * size + 1, total)
  return (
    <Group justify="space-between" px="md" py="sm" gap="sm" className="pager">
      <Text size="sm" c="dimmed">
        {from.toLocaleString()}–{Math.min(page * size, total).toLocaleString()} of {capped ? `${MAX_COUNT.toLocaleString()}+` : total.toLocaleString()} {noun}
        {capped && page === pages && ' — refine your filters to see the rest'}
      </Text>
      <Group gap="sm">
        <Select size="xs" w={120} aria-label="Rows per page" allowDeselect={false} value={String(size)} onChange={(v) => setSize(Number(v))}
          data={PAGE_SIZES.map((n) => ({ value: String(n), label: `${n} per page` }))} comboboxProps={{ withinPortal: true }} />
        {pages > 1 && (
          <Pagination size="sm" total={pages} value={page} siblings={1} boundaries={1} withEdges={pages > 7}
            onChange={(n) => { setPage(n); window.scrollTo({ top: 0, behavior: 'smooth' }) }}
            getControlProps={(c) => ({ 'aria-label': `${c} page` })} getItemProps={(n) => ({ 'aria-label': `Page ${n}` })} />
        )}
      </Group>
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

/** Keeps a rendering failure (or a page chunk that no longer exists after an upgrade) from blanking the whole app. */
export class ErrorBoundary extends Component<{ children: ReactNode; resetKey?: string }, { error?: Error }> {
  state: { error?: Error } = {}
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error) { console.error(error) }
  componentDidUpdate(prev: { resetKey?: string }) {
    if (this.state.error && prev.resetKey !== this.props.resetKey) this.setState({ error: undefined }) // navigating away recovers
  }
  render() {
    return this.state.error ? <ErrorScreen error={this.state.error} /> : this.props.children
  }
}

function ErrorScreen({ error }: { error: unknown }) {
  const message = error instanceof Error ? error.message : String(error)
  // a page chunk that is gone: the server was upgraded while this tab was open
  const outdated = /dynamically imported module|module script|Failed to fetch|Unable to preload/i.test(message)
  return (
    <EmptyState icon={outdated ? <IconRefresh /> : <IconAlertCircle />} title={outdated ? 'RepoMan was updated' : 'This page ran into a problem'}
      action={<Button leftSection={<IconRefresh size={16} />} onClick={() => window.location.reload()}>Reload</Button>}>
      {outdated ? 'A newer version is running on the server. Reload to continue; nothing you saved is lost.'
        : <>Reload to try again. If it keeps happening, tell your administrator what you were doing. <Text span size="xs" ff="monospace" display="block" mt="xs">{message}</Text></>}
    </EmptyState>
  )
}

/** The router's error element: the same screen for failures outside the page area (shell, sign-in). */
export function RouteError() {
  const error = useRouteError()
  useEffect(() => console.error(error), [error])
  return <ErrorScreen error={error} />
}

/**
 * Asks before leaving a page that has unsaved edits. Only a change of page counts: paging and filters
 * (the ?query of the same page) pass. Returns a function that lets the next navigation through, for a
 * save that itself moves on.
 */
export function useUnsavedGuard(dirty: boolean) {
  const allow = useRef(false)
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && !allow.current && currentLocation.pathname !== nextLocation.pathname)
  useEffect(() => {
    if (blocker.state !== 'blocked') return
    let leaving = false
    modals.openConfirmModal({
      title: <Text fw={600}>Leave without saving?</Text>,
      children: <Text size="sm">This page has changes you haven't saved. If you leave now, they are lost.</Text>,
      labels: { confirm: 'Discard changes', cancel: 'Keep editing' },
      confirmProps: { color: 'red' },
      onConfirm: () => { leaving = true; blocker.proceed() },
      onClose: () => { if (!leaving) blocker.reset() }, // Cancel, Esc and a click outside all mean "stay"
    })
  }, [blocker.state]) // eslint-disable-line react-hooks/exhaustive-deps
  return () => { allow.current = true }
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
      <TextInput label={<>Type <b>{name}</b> to confirm</>} value={typed} onChange={(e) => setTyped(e.currentTarget.value)} autoFocus data-autofocus
        autoComplete="off" spellCheck={false} />
      <Group justify="flex-end">
        <Button variant="default" onClick={() => modals.closeAll()}>Cancel</Button>
        <Button color="red" disabled={typed.trim() !== name} loading={busy}
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

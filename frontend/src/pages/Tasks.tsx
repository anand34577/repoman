import { Badge, Button, Card, Group, Loader, SimpleGrid, Stack, Text, Tooltip } from '@mantine/core'
import { useInterval } from '@mantine/hooks'
import { IconAlertTriangle, IconCircleCheck, IconPlayerPlay } from '@tabler/icons-react'
import { useEffect } from 'react'
import { api, useApi, type Task } from '../api'
import { fullDate, LoadError, notifyError, notifyOk, PageHeader, timeAgo } from '../util'

export default function Tasks() {
  const { data, error, reload } = useApi<Task[]>('/tasks')
  const running = data?.some((t) => t.running)
  const poll = useInterval(() => reload(), 1500)
  useEffect(() => { if (running) poll.start(); else poll.stop(); return poll.stop }, [running]) // eslint-disable-line react-hooks/exhaustive-deps

  const run = (t: Task) => api(`/tasks/${t.id}/run`, { method: 'POST' })
    .then(() => { notifyOk(`${t.name} started`); setTimeout(reload, 300) }).catch(notifyError)

  return (
    <>
      <PageHeader title="Scheduled tasks" description="Housekeeping RepoMan runs automatically. Run any task now to apply changes immediately or to check system health." />
      <LoadError error={error} retry={reload} />
      <SimpleGrid cols={{ base: 1, md: 2 }}>
        {data?.map((t) => (
          <Card key={t.id} padding="lg">
            <Stack gap="xs" h="100%">
              <Group justify="space-between" wrap="nowrap" align="flex-start">
                <Text fw={600}>{t.name}</Text>
                <Badge variant="light" color={t.schedule === 'Manual' ? 'gray' : 'brand'}>{t.schedule}</Badge>
              </Group>
              <Text size="sm" c="dimmed" style={{ flex: 1 }}>{t.description}</Text>
              <Card withBorder={false} bg="var(--mantine-color-default-hover)" padding="sm" radius="md">
                {t.running ? (
                  <Group gap="xs"><Loader size="xs" /><Text size="sm">Running…</Text></Group>
                ) : t.lastRun ? (
                  <Group gap="xs" wrap="nowrap" align="flex-start">
                    {t.error ? <IconAlertTriangle size={18} color="var(--mantine-color-red-6)" /> : <IconCircleCheck size={18} color="var(--mantine-color-teal-6)" />}
                    <div>
                      <Text size="sm">{t.error ? t.error : t.result}</Text>
                      {t.error && t.result && <Text size="xs" c="dimmed">{t.result}</Text>}
                      <Tooltip label={fullDate(t.lastRun)}><Text size="xs" c="dimmed">Last run {timeAgo(t.lastRun)} · took {t.durationMs < 1000 ? `${t.durationMs} ms` : `${(t.durationMs / 1000).toFixed(1)} s`}</Text></Tooltip>
                    </div>
                  </Group>
                ) : <Text size="sm" c="dimmed">Not run yet</Text>}
              </Card>
              <Group justify="space-between">
                <Text size="xs" c="dimmed">{t.nextRun ? `Next run ${timeAgo(t.nextRun)}` : 'Runs only when started manually'}</Text>
                <Button size="xs" variant="light" leftSection={<IconPlayerPlay size={14} />} disabled={t.running} onClick={() => run(t)}>Run now</Button>
              </Group>
            </Stack>
          </Card>
        ))}
      </SimpleGrid>
    </>
  )
}

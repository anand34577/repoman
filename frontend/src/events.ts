/** Audit events, grouped for the webhook and notification pickers. Patterns ending in * match a prefix. */
export const EVENT_GROUPS = [
  { group: 'Health', items: ['repository.degraded', 'repository.recovered', 'repository.quota', 'task.failed', 'system.start'] },
  { group: 'Artifacts', items: ['asset.upload', 'asset.delete', 'component.delete', 'npm.publish', 'pypi.upload', 'docker.push', 'docker.delete', 'go.upload', 'apt.upload', 'yum.upload', 'conda.upload'] },
  { group: 'Repositories', items: ['repository.create', 'repository.update', 'repository.delete', 'repository.permissions', 'repository.reindex', 'cleanup', 'cache.invalidate'] },
  {
    group: 'Security', items: ['login', 'login.failed', 'login.lockout', 'oidc.login', 'oidc.login.failed', 'oidc.link', 'oidc.unlink', 'password.change', 'password.reset',
      'user.create', 'user.update', 'user.delete', 'token.create', 'token.delete', 'settings.update', 'config.import', 'config.export'],
  },
]

const problems = ['repository.degraded', 'repository.recovered', 'repository.quota', 'task.failed', 'login.lockout']
const publishing = ['npm.publish', 'pypi.upload', 'docker.push', 'go.upload', 'apt.upload', 'yum.upload', 'conda.upload']
const security = ['login.failed', 'oidc.login.failed', 'password.change', 'password.reset', 'user.create', 'user.update', 'user.delete', 'token.create', 'settings.update', 'repository.delete', 'repository.permissions', 'config.import']

/** Ready-made subscriptions for notification channels; `custom` leaves the picker to the user. */
export const NOTIFY_PRESETS = [
  { value: 'problems', label: 'Problems only', description: 'An upstream goes down or recovers, a quota fills up, a task fails, sign-ins get blocked.', events: problems },
  { value: 'ops', label: 'Problems + releases', description: 'The above, plus every package or image published to a hosted repository.', events: [...problems, ...publishing] },
  { value: 'security', label: 'Problems + security', description: 'The above, plus failed sign-ins and changes to users, tokens, access and settings.', events: [...problems, ...security] },
  { value: 'all', label: 'Everything', description: 'Every audit event. Chatty on a busy server: each uploaded file is one message.', events: [] as string[] },
] as const

export function presetOf(events: string[]): string {
  const key = [...events].sort().join()
  return NOTIFY_PRESETS.find((p) => [...p.events].sort().join() === key)?.value ?? 'custom'
}

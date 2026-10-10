# Notifications and webhooks

RepoMan records everything that happens as **events** (the audit log). Two features deliver events elsewhere:

| | For | Delivers |
|---|---|---|
| **Notifications** | People | A readable push message to phones and desktops through your [Gotify](https://gotify.net) server |
| **Webhooks** | Programs | The raw event as signed JSON to any HTTP endpoint: CI triggers, chat bots, SIEM |

Both are configured by administrators, filter by event and by repository, and are optional.

- [Gotify notifications](#gotify-notifications)
- [Webhooks](#webhooks)
- [Event catalogue](#event-catalogue)

## Gotify notifications

Gotify is a small self-hosted notification server with Android, browser and desktop clients. Because you
run it yourself, notifications work on networks without internet access — RepoMan only needs to reach your
Gotify server.

### Set up

1. **In Gotify:** *Apps → Create application*. Name it "RepoMan" and copy the application's **token**.
   (An *application* token sends messages. It is not your Gotify password and not a *client* token.)
2. **In RepoMan:** *Notifications → Add channel*.
   - **Gotify server URL** — the address you open Gotify at, e.g. `https://gotify.example.com`. If Gotify is
     served under a sub-path, include it: `https://example.com/gotify`. Do not add `/message`.
   - **Application token** — paste it.
   - **What to send** — pick a preset (below) or individual events.
3. **Save.** RepoMan sends a test message immediately and tells you whether Gotify accepted it.

You can add several channels — for example "Ops phone" for problems and "Release feed" for publishes — each
with its own filters and priorities.

### What to send

| Preset | Events |
|---|---|
| **Problems only** *(default)* | An upstream becomes unreachable or recovers, a storage quota is reached, a scheduled task fails, sign-ins are blocked after repeated failures |
| **Problems + releases** | The above, plus every package or image published to a hosted repository |
| **Problems + security** | The above, plus failed sign-ins, password changes, and changes to users, tokens, repository access and settings |
| **Everything** | Every event. On a busy server this is a lot: each uploaded file is one message |
| **Custom** | Any combination from the [catalogue](#event-catalogue) |

**Repositories:** leave empty for all. If you select repositories, only events *about those repositories*
are sent — server-wide events such as sign-ins and task failures are then not sent on that channel.

### Priorities

Gotify clients decide how loudly to notify from the message priority (0–10). Each channel has two:

| Setting | Default | Used for |
|---|---|---|
| Priority for routine events | 4 | Everything that is not a problem |
| Priority for problems | 8 | `repository.degraded`, `repository.quota`, `task.failed`, `login.lockout`, `login.failed`, `oidc.login.failed` |

In the Gotify Android app: 0 = no notification, 1–3 = silent, 4–7 = sound, 8–10 = sound and pop-up.

### What a message contains

```
Title:    Upstream unreachable
Message:  docker-hub
          upstream request failed; cached content is still served: dial tcp: i/o timeout
```

Title (what happened), the subject (repository, artifact, user…), details when there are any, and
"by <user>" when a person caused it. If a **Base URL** is set (*Settings → General*), tapping the
notification opens the repository in RepoMan. The raw event is attached under `extras["repoman::event"]`
for Gotify plugins.

### Delivery, failures and edge cases

- Messages are sent in the background, one at a time; a slow Gotify server never slows RepoMan down.
- A failed delivery is retried after 2 and after 10 seconds, then dropped. A rejected token (401/403) or a
  wrong URL (404) is not retried, since retrying cannot help.
- The channel list shows the outcome of the last delivery; hover over **Failed** for the reason. Use the
  paper-plane button to send a test at any time.
- If Gotify is down for long and more than 256 messages queue up, further ones are dropped (and logged).
  Nothing is lost for good: the audit log has every event.
- **An outage produces one message, not a flood:** `repository.degraded` is sent when an upstream starts
  failing and `repository.recovered` when it answers again, however many client requests failed in between.
  Likewise `repository.quota` is sent at most once an hour per repository.
- **A paused channel** keeps its configuration and sends nothing.
- **Imported configuration** ([operations](operations.md#configuration-export-and-import)) does not contain
  tokens. Imported channels arrive paused and marked *token missing*; edit them and enter the token.
- **TLS:** a Gotify server with a certificate from a private CA needs that CA in the trust store of the
  machine running RepoMan ([configuration](configuration.md#outbound-connections)). There is no option to skip verification.
- **Forward proxy:** if `HTTPS_PROXY` is set, add the Gotify host to `NO_PROXY` when it must be reached directly.
- Changes to notification channels themselves are recorded in the audit log but never sent as notifications,
  so a misconfigured channel cannot loop.

| Symptom | Cause and fix |
|---|---|
| *Gotify rejected the application token* | Wrong token, a *client* token instead of an *application* token, or the application was deleted. Create a new application token |
| *no Gotify API at this URL* | The URL points somewhere else, includes `/message`, or misses the sub-path Gotify is served under |
| *cannot reach the Gotify server: … connection refused / timeout* | Network or firewall between RepoMan and Gotify; wrong port; Gotify not running |
| *… certificate signed by unknown authority* | Private CA not trusted by the RepoMan host |
| Test works, but no notifications arrive | The channel's event selection or repository filter does not match what happened; check *Audit log* for the event names |
| Messages arrive but silently | Raise the priority (≥ 4 for sound); check the phone's notification settings for the Gotify app |

### Managing channels through the API

```bash
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"Ops phone","kind":"gotify","url":"https://gotify.example.com","token":"A1b2C3…",
       "priority":4,"alertPriority":8,"enabled":true,
       "events":["repository.degraded","repository.recovered","repository.quota","task.failed","login.lockout"],"repos":[]}' \
  https://repo.example.com/api/v1/notifiers

curl -X POST -H "Authorization: Bearer $TOKEN" https://repo.example.com/api/v1/notifiers/1/test
# {"ok":true,"status":"200 OK"}
```

See the [API reference](api.md#notification-channels). Event names may end in `*` to match a prefix
(`"repository.*"`); an empty `events` list means every event.

## Webhooks

*Webhooks → Add webhook*: a name, a URL, an optional signing secret, and filters.

RepoMan sends an HTTP `POST` with a JSON body for each matching event:

```json
{
  "event": "docker.push",
  "actor": "alice",
  "target": "docker-hosted/team/api:1.4.0",
  "detail": "",
  "timestamp": 1760000000
}
```

| Header | Value |
|---|---|
| `Content-Type` | `application/json` |
| `User-Agent` | `RepoMan-Webhook/<version>` |
| `X-RepoMan-Event` | the event name |
| `X-RepoMan-Signature` | `sha256=<hex HMAC-SHA256 of the raw body, keyed with the secret>` — only if a secret is set |

`target` is what the event is about: `repository/path` for artifacts, a repository name, a user name, and
so on. `actor` is the user name, or `system` for automatic actions. `timestamp` is in Unix seconds.

**Always set a secret and verify the signature** on the receiving side, over the raw request body before
parsing it:

```js
// Node.js
const crypto = require('crypto')
const expected = 'sha256=' + crypto.createHmac('sha256', SECRET).update(rawBody).digest('hex')
const given = req.headers['x-repoman-signature'] || ''
if (given.length !== expected.length || !crypto.timingSafeEqual(Buffer.from(given), Buffer.from(expected))) {
  return res.status(401).end()
}
```

```python
# Python
import hmac, hashlib
expected = "sha256=" + hmac.new(SECRET.encode(), raw_body, hashlib.sha256).hexdigest()
if not hmac.compare_digest(expected, request.headers.get("X-RepoMan-Signature", "")):
    abort(401)
```

Delivery rules:

- Your endpoint should answer with a 2xx status within 10 seconds. Anything else counts as a failure and is
  retried after 2 and after 10 seconds, then given up; the last status is shown in the webhook list.
- Deliveries are independent and may arrive out of order or — after a retry your endpoint actually
  processed — twice. Make handlers idempotent.
- Events and repositories are filtered as for notifications; an empty selection means "all". Event filters
  accept a trailing `*` when set through the API.
- The secret is write-only: it is never shown again or exported. Leave the field blank when editing to keep it.
- **Test** (the paper-plane button) sends a `webhook.test` event.

Example uses: trigger a deployment on `docker.push` for one repository; post `repository.degraded` to a chat
room; forward everything to a log collector for tamper-evident auditing.

## Event catalogue

Names are stable; integrations can rely on them.

### Health

| Event | When | Target |
|---|---|---|
| `repository.degraded` | A proxy's upstream starts failing (unreachable, 5xx or 429). Once per outage | repository |
| `repository.recovered` | The upstream answers again | repository |
| `repository.quota` | An upload was refused because the storage quota is reached. At most hourly per repository | repository |
| `task.failed` | A scheduled or manually started maintenance task ended with an error | task id |
| `system.start` | The server started (detail: version). Useful to notice unexpected restarts | `repoman` |

### Artifacts

| Event | When | Target |
|---|---|---|
| `asset.upload` | A file was uploaded to a raw or Maven repository, or through the UI | `repo/path` |
| `asset.delete` | A single file was deleted | `repo/path` |
| `component.delete` | A component or one of its versions was deleted | `repo/component` |
| `npm.publish` | An npm package version was published | `repo/package` |
| `pypi.upload` | A Python distribution file was uploaded | `repo/project` |
| `docker.push` | An image tag was pushed | `repo/image:tag` |
| `docker.delete` | An image manifest or tag was deleted | `repo/image@ref` |
| `go.upload` | A Go module file was uploaded | `repo/path` |
| `apt.upload` | A `.deb` was uploaded | `repo/path` |
| `yum.upload` | An `.rpm` was uploaded | `repo/path` |
| `conda.upload` | A `.conda` or `.tar.bz2` package was uploaded | `repo/subdir/file` |

Downloads are not events (they are counted per artifact instead). Files cached by proxies are not events either.
A Maven deployment uploads several files (jar, pom, checksums, metadata), each producing an `asset.upload`.

### Repositories

| Event | When |
|---|---|
| `repository.create`, `repository.update`, `repository.delete` | Configuration changes |
| `repository.permissions` | Per-user access overrides were changed |
| `repository.reindex` | An index rebuild was requested |
| `cleanup` | A cleanup policy removed artifacts (detail: how many) |
| `cache.invalidate` | A proxy's cached index data was invalidated |

### Security

| Event | When |
|---|---|
| `login`, `oidc.login` | Successful browser sign-in (password / SSO) |
| `login.failed`, `oidc.login.failed` | Failed sign-in (detail: the client address or the reason) |
| `login.lockout` | An address was blocked for 15 minutes after 10 failures (target: the address) |
| `password.change` | A user changed their password |
| `password.reset` | A password was reset with `repoman reset-password`. Audit log only: it is written by the command-line tool, so it is not delivered to webhooks or notifications |
| `oidc.link`, `oidc.unlink` | An SSO identity was linked to or removed from an account |
| `user.create`, `user.update`, `user.delete` | Account administration |
| `token.create`, `token.delete` | API token created (detail: scope) or revoked |
| `settings.update` | Server settings or SSO configuration changed |
| `config.export`, `config.import` | Configuration backup downloaded / applied |
| `webhook.create`, `webhook.update`, `webhook.delete` | Webhook administration |
| `notifier.create`, `notifier.update`, `notifier.delete` | Notification channel administration (never sent as notifications) |
| `task.run` | A maintenance task was started by hand |

Authentication with tokens or passwords by package clients is not logged per request (it would be millions
of entries); failures count towards the lockout, and the lockout itself is an event.

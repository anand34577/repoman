import { expect, request, test, type APIRequestContext, type Page } from '@playwright/test'

const ADMIN = { username: 'admin', password: 'e2e-admin-pw' }

const pom = (version: string) => `<project><modelVersion>4.0.0</modelVersion><groupId>com.acme</groupId><artifactId>lib</artifactId><version>${version}</version>
<description>E2E demo library</description><url>https://acme.example</url>
<licenses><license><name>Apache-2.0</name></license></licenses>
<dependencies><dependency><groupId>org.slf4j</groupId><artifactId>slf4j-api</artifactId><version>2.0.9</version></dependency></dependencies></project>`

let api: APIRequestContext

test.beforeAll(async ({ baseURL }) => {
  api = await request.newContext({ baseURL, httpCredentials: { ...ADMIN, send: 'always' } })
  const post = async (url: string, data: unknown) => { const r = await api.post(url, { data }); expect([200, 201, 409]).toContain(r.status()) }
  await post('/api/repositories', { name: 'e2e-staging', format: 'maven', type: 'hosted' })
  await post('/api/repositories', { name: 'e2e-releases', format: 'maven', type: 'hosted' })
  await post('/api/repositories', { name: 'e2e-public', format: 'maven', type: 'group', members: ['e2e-staging', 'e2e-releases'] })
  for (const v of ['1.0', '1.1']) {
    const base = `/repository/e2e-staging/com/acme/lib/${v}/lib-${v}`
    for (const [suffix, body] of [['.pom', pom(v)], ['.jar', `jar ${v}`], ['-sources.jar', `sources ${v}`]]) {
      const r = await api.put(base + suffix, { data: body })
      expect([201, 400]).toContain(r.status()) // 400: already uploaded by an earlier run of the suite
    }
  }
  await api.get('/repository/e2e-public/com/acme/lib/1.1/lib-1.1.jar') // one download for the charts
})

test.afterAll(async () => { await api.dispose() })

async function signIn(page: Page) {
  const r = await page.request.post('/api/auth/login', { data: ADMIN })
  expect(r.ok()).toBeTruthy()
}

test.beforeEach(async ({ page }) => { await signIn(page) })

test('a group shows the packages of its members', async ({ page }) => {
  await page.goto('/repositories/e2e-public')
  await expect(page.getByRole('row', { name: /^.*com/ }).first()).toBeVisible() // folder "com" from a member
  await expect(page.getByText('This repository is empty')).toHaveCount(0)
  await page.goto('/repositories/e2e-public/components')
  await expect(page.getByRole('link', { name: 'com.acme:lib' })).toBeVisible()
  // the header totals are the members' totals, not zero
  await page.goto('/repositories/e2e-public')
  await expect(page.getByText('Artifacts').first().locator('xpath=following-sibling::*[1]')).not.toHaveText('0')
})

test('the package page explains the package and how to use it', async ({ page }) => {
  await page.goto('/repositories/e2e-public/package?component=com.acme%3Alib')
  await expect(page.getByRole('heading', { name: 'com.acme:lib' })).toBeVisible()
  await expect(page.getByText('E2E demo library')).toBeVisible()
  await expect(page.getByText('Apache-2.0')).toBeVisible()
  // Maven dependency block for the main artifact, no classifier
  const code = page.locator('pre.snippet, .snippet').first()
  await expect(code).toContainText('<artifactId>lib</artifactId>')
  await expect(code).not.toContainText('classifier')
  await page.getByRole('tab', { name: 'Gradle (Kotlin)' }).click()
  await expect(code).toContainText('implementation("com.acme:lib:1.1")')
  // dependencies come from the pom
  await page.getByRole('tab', { name: /^Dependencies/ }).click()
  await expect(page.getByText('org.slf4j:slf4j-api')).toBeVisible()
})

test('files show checksums and signature state', async ({ page }) => {
  await page.goto('/repositories/e2e-public/package?component=com.acme%3Alib&version=1.1')
  await page.getByRole('tab', { name: /^Files/ }).click()
  await expect(page.getByText('lib-1.1.jar', { exact: true })).toBeVisible()
  await expect(page.getByText(/^[0-9a-f]{40}$/).first()).toBeVisible() // SHA-1
  await expect(page.getByText('not signed').first()).toBeVisible()
})

test('a sources jar gets a classifier in its dependency snippet', async ({ page }) => {
  await page.goto('/repositories/e2e-staging?path=com%2Facme%2Flib%2F1.1&select=com%2Facme%2Flib%2F1.1%2Flib-1.1-sources.jar')
  const drawer = page.getByRole('dialog')
  await expect(drawer.getByText('Use this package')).toBeVisible()
  await expect(drawer.locator('.snippet').first()).toContainText('<classifier>sources</classifier>')
  await expect(drawer.getByText('Checksums & verification')).toBeVisible()
})

test('search understands operators, groups and saved searches', async ({ page }) => {
  await page.goto('/search?q=lib+version%3A1.0&repo=e2e-public')
  await expect(page.getByRole('link', { name: 'com.acme:lib' }).first()).toBeVisible()
  await expect(page.getByText('1.1', { exact: true })).toHaveCount(0) // version:1.0 filters 1.1 out
  await page.getByRole('button', { name: 'Save this search' }).click()
  await page.getByLabel('Name').fill('lib one-oh')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('lib one-oh')).toBeVisible()
  await page.goto('/search')
  await page.getByText('lib one-oh').click()
  await expect(page).toHaveURL(/version%3A1\.0|version:1\.0/)
  await page.getByLabel('Delete saved search lib one-oh').click()
  await expect(page.getByText('lib one-oh')).toHaveCount(0)
})

test('versions can be promoted to another repository', async ({ page }) => {
  const art = `promo${Date.now()}` // a package of its own, so the test can run again against the same data
  await api.put(`/repository/e2e-staging/com/acme/${art}/2.0/${art}-2.0.jar`, { data: 'promote me' })
  await page.goto('/repositories/e2e-staging/components')
  await page.getByLabel(`Select com.acme:${art}`, { exact: true }).check()
  await page.getByRole('button', { name: /^Promote/ }).click()
  await page.getByRole('textbox', { name: 'Target repository' }).click()
  await page.getByRole('option', { name: 'e2e-releases' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Promote', exact: true }).click()
  await expect(page.getByText(/Copied \d+ files? to e2e-releases/)).toBeVisible()
  expect((await api.get(`/repository/e2e-releases/com/acme/${art}/2.0/${art}-2.0.jar`)).status()).toBe(200)
})

test('files can be selected and deleted in bulk', async ({ page }) => {
  await api.put('/repository/raw-hosted/bulk/a.txt', { data: 'a' })
  await api.put('/repository/raw-hosted/bulk/b.txt', { data: 'b' })
  await page.goto('/repositories/raw-hosted?path=bulk')
  await page.getByLabel('Select all on this page').check()
  await expect(page.getByText('2 items selected')).toBeVisible()
  await page.getByRole('button', { name: 'Delete selected' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(page.getByText('Deleted 2 files')).toBeVisible()
  expect((await api.get('/repository/raw-hosted/bulk/a.txt')).status()).toBe(404)
})

test('the command palette finds packages', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: /Go to/ }).click()
  await page.getByPlaceholder(/Jump to a page/).fill('com.acme')
  const hit = page.getByRole('button', { name: /com\.acme:lib/ }).first()
  await expect(hit).toBeVisible()
  await hit.click()
  await expect(page).toHaveURL(/\/package\?component=com\.acme/)
})

test('the storage page reports usage', async ({ page }) => {
  await page.goto('/storage')
  await expect(page.getByRole('heading', { name: 'Storage' })).toBeVisible()
  await expect(page.getByText('e2e-staging')).toBeVisible()
  await page.getByRole('button', { name: 'Scan for unused files' }).click()
  await expect(page.getByText(/in \d+ files/)).toBeVisible()
})

// Invoked by the isolated Go integration fixture, not against a user broker.
import { execFile, spawn } from 'node:child_process'
import { createHmac } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { promisify } from 'node:util'
import { chromium, expect } from '@playwright/test'

const check = expect.configure({ timeout: 15000 })
const browser = await chromium.launch({
  headless: true,
  ...(process.env.PLAYWRIGHT_CHROMIUM_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH } : {}),
})
const page = await browser.newPage({ acceptDownloads: true })
page.setDefaultTimeout(15000)
const errors = []
page.on('pageerror', (error) => errors.push(error.message))

function totp(secret) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  const bits = [...secret.replace(/=|\s/g, '')].map((c) => alphabet.indexOf(c).toString(2).padStart(5, '0')).join('')
  const key = Buffer.from(bits.match(/.{8}/g).map((byte) => Number.parseInt(byte, 2)))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)))
  const digest = createHmac('sha1', key).update(counter).digest()
  return String((digest.readUInt32BE(digest[19] & 15) & 0x7fffffff) % 1000000).padStart(6, '0')
}

// The isolated broker of the Go fixture. The connection test of the
// onboarding journey (#353) runs a real MQTT client from its image.
const brokerContainer = process.env.MCM_E2E_BROKER_CONTAINER
const servicePassword = process.env.MCM_E2E_SERVICE_PASSWORD
const runFile = promisify(execFile)
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
// The one-time password of the journey's MQTT user. It is never printed:
// a failure report redacts it from the page body.
let probePassword = ''

// The client arguments carry a password, so a failure reports the client's
// own stderr and exit, never the command line Node would echo.
async function brokerClient(args) {
  try {
    return await runFile('docker', ['exec', brokerContainer, ...args], { timeout: 15000 })
  } catch (error) {
    throw new Error(`${args[0]} in the isolated broker failed (exit ${error.code ?? error.signal}): ${String(error.stderr ?? '').trim()}`)
  }
}

function mqttPublish(user, password, topic, payload, clientID) {
  return brokerClient(['mosquitto_pub', '-h', '127.0.0.1', '-p', '1883', ...(clientID ? ['-i', clientID] : []), '-u', user, '-P', password, '-t', topic, '-m', payload])
}

// Proves a denied publish never reaches the broker, independently of the UI:
// the service account subscribes to the denied topic (MCM always grants it
// read #; after an apply it may write only its healthcheck topic). The
// tested account publishes a control message on its granted topic before
// the denied publish, which proves the subscription is live so the absence
// means something, and another one after it, which bounds the wait: the
// broker delivers to one subscriber in the order it processed the publishes.
async function proveDenialWithServiceAccount({ deniedTopic, deniedPayload, controlTopic, publish }) {
  const control = `${deniedPayload}-control`
  const subscriber = spawn('docker', ['exec', brokerContainer, 'mosquitto_sub', '-h', '127.0.0.1', '-p', '1883', '-u', 'mcm', '-P', servicePassword, '-t', deniedTopic, '-t', controlTopic, '-W', '30'], { stdio: ['ignore', 'pipe', 'pipe'] })
  const received = []
  let pending = ''
  let failure = ''
  subscriber.stdout.setEncoding('utf8')
  subscriber.stdout.on('data', (chunk) => {
    const lines = (pending + chunk).split('\n')
    pending = lines.pop()
    received.push(...lines)
  })
  subscriber.stderr.setEncoding('utf8')
  subscriber.stderr.on('data', (chunk) => { failure += chunk })
  async function receive(payload, repeat) {
    const deadline = Date.now() + 15000
    while (!received.includes(payload)) {
      if (Date.now() > deadline) throw new Error(`the service account never received ${payload} on ${controlTopic} ${failure.trim()}`)
      if (repeat) await publish(controlTopic, payload)
      await sleep(500)
    }
  }
  try {
    await receive(`${control}-before`, true)
    await publish(deniedTopic, deniedPayload)
    await publish(controlTopic, `${control}-after`)
    await receive(`${control}-after`, false)
  } finally {
    subscriber.kill()
  }
  check(received.filter((payload) => payload === deniedPayload)).toEqual([])
}

let secret
let usedStep = -1
async function confirmMFA() {
  // Wait for a genuinely unused TOTP time step, not a retry of a rejected code.
  const delay = (usedStep + 1) * 30000 - Date.now() + 250
  if (delay > 0) await new Promise((resolve) => setTimeout(resolve, delay))
  usedStep = Math.floor(Date.now() / 30000)
  await page.getByPlaceholder('Authenticator code').last().fill(totp(secret))
  await page.getByRole('button', { name: 'Verify MFA', exact: true }).click()
  await check(page.getByText(/MFA confirmed for this session/).last()).toBeVisible()
}

async function openNavigationGroup(group) {
  const control = page.getByRole('button', { name: `Expand ${group}`, exact: true })
  if (await control.isVisible()) await control.click()
}

async function applyReviewedRevision() {
  await page.getByLabel(/I adopt this reviewed configuration/).check()
  const applied = page.waitForResponse((response) => response.url().endsWith('/broker/config/apply') && response.request().method() === 'POST')
  await page.getByRole('button', { name: 'Apply', exact: true }).click()
  const response = await applied
  check(response.status()).toBe(200)
  const result = await response.json()
  check(result.revision_id).toBeTruthy()
  check(['active_verified', 'pending_activation']).toContain(result.status)
  await check(page.getByText(`Revision result: ${result.status}`)).toBeVisible()
  return result
}

try {
  await page.goto(process.env.MCM_UI_URL)
  await page.locator('input[autocomplete="username"]').fill('admin')
  await page.locator('input[autocomplete="current-password"]').fill('ui-test-password')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  for (const name of ['Configuration', 'MQTT Access', 'Changes', 'Diagnostics', 'Panel administration']) {
    await check(page.getByRole('navigation').getByRole('heading', { name, exact: true })).toBeVisible()
  }
  // #355: at 360 px the navigation folds behind a keyboard-operable Menu button.
  await page.setViewportSize({ width: 360, height: 740 })
  const menu = page.getByRole('button', { name: 'Menu', exact: true })
  await check(menu).toHaveAttribute('aria-expanded', 'false')
  await check(page.getByRole('navigation')).toBeHidden()
  await check(page.locator('main h2').first()).toBeInViewport()
  await menu.focus()
  await page.keyboard.press('Enter')
  await check(page.getByRole('navigation')).toBeVisible()
  await page.locator('nav a[href="/dashboard"]').focus()
  await page.keyboard.press('Escape')
  await check(page.getByRole('navigation')).toBeHidden()
  await check(menu).toBeFocused()
  await page.setViewportSize({ width: 1280, height: 720 })
  await check(menu).toBeHidden()
  await openNavigationGroup('Panel administration')
  await page.locator('nav a[href="/account"]').click()
  const enrollment = page.waitForResponse((response) => response.url().endsWith('/auth/mfa/setup'))
  await page.getByRole('button', { name: 'Enable MFA', exact: true }).click()
  secret = (await (await enrollment).json()).secret
  await page.getByLabel('I have saved my recovery codes in a safe place').check()
  usedStep = Math.floor(Date.now() / 30000)
  await page.getByPlaceholder('000000').fill(totp(secret))
  await page.getByRole('button', { name: 'Confirm & enable', exact: true }).click()
  await check(page.getByRole('button', { name: 'Disable MFA', exact: true })).toBeVisible()
  console.log('[UI] MFA enrollment completed through the browser')

  // The integrated onboarding journey of issue #341: the MQTT user, its
  // access rule, the preview and the apply in a single journey, against a
  // real Mosquitto.
  await openNavigationGroup('MQTT Access')
  await page.locator('nav a[href="/users"]').click()
  await page.getByRole('button', { name: 'Add User', exact: true }).click()
  const journey = page.getByRole('region', { name: 'MQTT user onboarding' })
  await journey.getByPlaceholder('device-sensor-01').fill('probe')
  await journey.getByRole('button', { name: 'Create user', exact: true }).click()
  await check(journey.getByText('User created — save this password')).toBeVisible()
  // The one-time password is read from its banner before anything dismisses
  // it: the connection test below authenticates a real client with it.
  probePassword = ((await journey.locator('pre').textContent()) ?? '').trim()
  check(probePassword).not.toBe('')
  // The saved user is not applied yet, so the pending deploy banner covers it.
  await check(page.getByText('Pending deploy')).toBeVisible()
  await journey.getByPlaceholder('sensors/# or device/+/status').fill('probe/allowed')
  // The form carries several selects, so the permission one is addressed by its label.
  await journey.getByLabel(/^Permission/).selectOption('readwrite')
  await journey.getByRole('button', { name: 'Save access rule', exact: true }).click()
  await journey.getByRole('button', { name: 'Preview changes', exact: true }).click()
  await confirmMFA()
  const accessApply = page.waitForResponse((response) => response.url().endsWith('/deployments/apply'))
  await journey.getByRole('button', { name: 'Apply and verify', exact: true }).click()
  const applied = await accessApply
  check(applied.status()).toBe(200)
  check((await applied.json()).status).toBe('active_verified')
  await check(journey.getByText('Applied and verified')).toBeVisible()
  // The journey cleared the pending deploy state: nothing is left saved-only.
  await check(page.getByText('Pending deploy')).toHaveCount(0)

  // The connection test of issue #353: a real MQTT client connects with the
  // created credentials; the journey observes the connection in the broker
  // log and the operation through the MCM monitor, and a publish outside the
  // grant stays denied.
  await journey.getByRole('button', { name: 'Test the connection', exact: true }).click()
  const connectionTest = journey.getByRole('region', { name: 'Connection test' })
  const stages = connectionTest.getByRole('list', { name: 'Connection test stages' })
  // Broker log entries are not replayed: the watch must be open before the client connects.
  await check(stages.getByLabel('Connection', { exact: true })).toContainText('Watching for a connection authenticated as probe.')
  await check(stages.getByLabel('Operation', { exact: true })).toContainText('Watching ')
  const [allowedTopic, allowedPayload] = await connectionTest.locator('li', { hasText: 'Allowed operation:' }).locator('code').allTextContents()
  check(allowedTopic).toBe('probe/allowed')
  check(allowedPayload).toMatch(/^mcm-test-[0-9a-f]{8}$/)
  const [deniedPayload, deniedTopic] = await connectionTest.locator('li', { hasText: 'Operation that must stay denied:' }).locator('code').allTextContents()
  check(deniedPayload).toMatch(/^mcm-denied-[0-9a-f]{8}$/)
  check(deniedTopic).toBe(`${deniedPayload}/probe`)
  // A client id of its own tells the test client apart from the deployment's
  // activation probe, which also authenticates as probe and whose log line
  // a later poll may still deliver: only the real client may satisfy this stage.
  const clientID = `mcm-e2e-${allowedPayload}`
  await mqttPublish('probe', probePassword, allowedTopic, allowedPayload, clientID)
  // The log poller reads the broker log every 5 seconds.
  await check(stages.getByLabel('Connection', { exact: true })).toContainText(`Connection observed: client ${clientID} as probe.`, { timeout: 20000 })
  await check(stages.getByLabel('Operation', { exact: true })).toContainText(`Operation verified: ${allowedPayload} reached ${allowedTopic}.`, { timeout: 20000 })
  await proveDenialWithServiceAccount({
    deniedTopic,
    deniedPayload,
    controlTopic: allowedTopic,
    publish: (topic, payload) => mqttPublish('probe', probePassword, topic, payload),
  })
  // The UI watch keeps reporting the absence well past two log polls.
  await sleep(12000)
  const denied = stages.getByLabel('Denied operation', { exact: true })
  await check(denied).toContainText('No message with the denied payload reached the broker')
  await check(denied).not.toContainText('The denied operation reached the broker')
  console.log('[UI] A real MQTT client connected as the new user, its granted publish was verified and the denied one never reached the broker')
  // The rule of the journey is an ordinary MCM rule: the ACL panel lists it.
  await page.locator('nav a[href="/acls"]').click()
  // The topic filter appears in the rule table and in the effective permissions table.
  await check(page.getByRole('cell', { name: 'probe/allowed', exact: true }).first()).toBeVisible()
  console.log('[UI] MQTT user, ACL grant and verified deployment completed in one journey')

  const current = page.waitForResponse((response) => response.url().endsWith('/broker/config'))
  // Configuration is expanded by default, including after navigating away.
  await page.locator('nav a[href="/broker-config"]').click()
  const original = (await (await current).json()).body
  const imported = page.waitForResponse((response) => response.url().endsWith('/broker/config/import'))
  await page.getByRole('button', { name: 'Import / Refresh preview', exact: true }).click()
  const originalRevision = (await (await imported).json()).revision_id
  check(originalRevision).toBeTruthy()
  const modified = original.replace('connection_messages true', 'connection_messages false')
  check(modified).not.toBe(original)
  await page.getByRole('textbox', { name: 'Draft configuration' }).fill(modified)
  await page.getByRole('button', { name: 'Validate draft', exact: true }).click()
  await check(page.getByText('+connection_messages false', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Review changes', exact: true }).click()
  await check(page).toHaveURL(/\/changes$/)
  await confirmMFA()
  await applyReviewedRevision()
  console.log('[UI] Imported, edited, validated, reviewed and activated the exact configuration revision')

  const downloadEvent = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export configuration copy', exact: true }).click()
  const download = await downloadEvent
  const copyPath = await download.path()
  const copy = JSON.parse(await readFile(copyPath, 'utf8'))
  check(copy.format).toBe('mcm-broker-config/v1')
  check(copy.body).toBe(modified)
  await page.getByLabel('Import configuration copy', { exact: true }).setInputFiles(copyPath)
  await check(page.getByText('No root-file changes.', { exact: true })).toBeVisible()
  const recovery = page.waitForResponse((response) => response.url().endsWith(`/revisions/${originalRevision}/recover`))
  await page.getByRole('button', { name: `Recover revision ${originalRevision.slice(0, 8)}`, exact: true }).click()
  const recovered = await (await recovery).json()
  check(recovered.revision_id).not.toBe(originalRevision)
  check(recovered.conf_body).toBe(original)
  await applyReviewedRevision()
  console.log('[UI] Exported/imported a configuration copy and recovered the initial revision with activation')
  check(errors).toEqual([])
} catch (error) {
  console.error('[UI] journey failed:', error.message)
  const body = (await page.textContent('body').catch(() => '')) ?? ''
  console.error('[UI] page body:', (probePassword ? body.replaceAll(probePassword, '<redacted>') : body).slice(0, 1200))
  console.error('[UI] page errors:', errors)
  throw error
} finally {
  await browser.close()
}

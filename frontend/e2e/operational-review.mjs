// Browser review of the operational views (issue #359, S5/S6): Dashboard, MQTT
// users and Deploy on desktop and mobile, in empty, loaded and error states.
// It serves the built frontend/dist, answers every /api/v1 request and the
// broker event WebSocket with fixtures, and signs in with a fake token: no
// broker, no backend, no credentials.
//
//   npm --prefix frontend run build
//   node frontend/e2e/operational-review.mjs [output-dir]
//
// Output (default frontend/e2e/output/operational-review/): results.json,
// summary.md and one full-page JPEG per screen. Exit code 0 even when defects
// are found: the review reports, it does not gate.
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from '@playwright/test'
import { preview } from 'vite'

const frontend = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const out = resolve(process.argv[2] ?? `${frontend}/e2e/output/operational-review`)
await mkdir(out, { recursive: true })

// The primary lever is recognised by its class string, as in App.test.tsx.
const styles = await readFile(`${frontend}/src/features/ui/styles.ts`, 'utf8')
const leverPrimary = styles.match(/export const leverPrimary = '([^']+)'/)[1]

const viewports = { desktop: { width: 1440, height: 900 }, mobile: { width: 390, height: 844 } }
const views = { dashboard: '/', users: '/users', deploy: '/deploy' }
const states = ['empty', 'loaded', 'error']
const fakeToken = 'review-fake-token'

// ---------------------------------------------------------------- fixtures
const ago = (ms) => new Date(Date.now() - ms).toISOString()
const json = (status, body) => ({ status, contentType: 'application/json', body: JSON.stringify(body) })

const me = { id: 3, username: 'review-admin', disabled: false, role: 'admin', mfa_enabled: true, created_at: '2026-01-05T08:00:00Z', updated_at: '2026-01-05T08:00:00Z' }

const mqttUsers = [
  ['plant-gateway-01', false, '2026-02-03T09:12:00Z'],
  ['sensor-line-1', false, '2026-02-10T10:00:00Z'],
  ['sensor-line-2', false, '2026-02-10T10:02:00Z'],
  ['sensor-line-3', true, '2026-02-10T10:04:00Z'],
  ['packaging-plc', false, '2026-03-01T14:30:00Z'],
  ['cold-room-probe', false, '2026-03-18T07:45:00Z'],
  ['forklift-telemetry', false, '2026-04-22T16:20:00Z'],
  ['dashboard-reader', false, '2026-05-02T11:05:00Z'],
].map(([username, disabled, created], i) => ({ id: i + 1, username, disabled, created_at: created, updated_at: created }))

const deployments = [
  ['5c1e9a40-2f7b-4d1e-9a0c-1b2c3d4e5f60', 'active_verified', 'Configuration verified: the probe user connected with the new ACL.', 1],
  ['8d2f0b13-6a4c-4e2f-8b1d-2c3d4e5f6071', 'applied', 'Files written; the broker did not confirm the reload in time.', 26],
  ['1a7c3e55-9b2d-4f3a-8c4e-3d4e5f607182', 'rolled_back', 'Activation probe failed; the previous configuration was restored.', 50],
  ['b4e6d277-0c5e-4a4b-9d5f-4e5f60718293', 'failed', 'mosquitto_passwd rejected the rendered password file.', 75],
  ['e9f1a388-3d6f-4b5c-8e60-5f60718293a4', 'pending_activation', 'Listener 8883 files written; activation waits for the listener reload.', 98],
].map(([id, status, message, hours]) => ({ id, status, message, created_at: ago(hours * 3600_000) }))

const previewLoaded = {
  revision_id: 'rev-7f3c2a10',
  acl_diff: '-user sensor-line-3\n-topic read factory/line3/#\n+user cold-room-probe\n+topic readwrite cold-room/+/temperature\n+user forklift-telemetry\n+topic write fleet/forklift/+/telemetry',
  passwd_diff: '+cold-room-probe:$7$<hash elided>\n+forklift-telemetry:$7$<hash elided>',
  orphan_rules: [
    { id: 41, principal: 'sensor-line-3', topic_filter: 'factory/line3/#', permission: 'read' },
    { id: 57, principal: 'retired-scanner', topic_filter: 'warehouse/scanner/+/events', permission: 'write' },
  ],
  at_risk_entries: ['user sensor-line-3 / topic read factory/line3/#'],
  composition_warnings: ['Rule 63 grants readwrite on fleet/# to dashboard-reader, wider than its other rules.'],
  scope_pending_activation: ['listener 8883 (tls-devices): acl_file declared after the next listener reload'],
  summary: { users_added: 2, users_removed: 1, users_rotated: 0, has_passwd_changes: true, topics_added: 2, topics_removed: 1, has_acl_changes: true },
  has_changes: true,
}
const previewEmpty = { revision_id: 'rev-empty', acl_diff: '', passwd_diff: '', orphan_rules: [], has_changes: false }

function status(state) {
  const loaded = state === 'loaded'
  const topTopics = [['factory/line1/temperature', 412], ['factory/line2/temperature', 388], ['cold-room/a/temperature', 251], ['fleet/forklift/7/telemetry', 140], ['packaging/plc/state', 93]]
  return {
    broker: {
      status: 'connected',
      observed_at: ago(40_000),
      target: { host: 'mosquitto', port: 1883, address: 'mosquitto:1883', tls_enabled: false },
      metrics: {
        last_message_at: loaded ? ago(5_000) : '0001-01-01T00:00:00Z',
        traffic: {
          snapshot_at: ago(60_000),
          window_seconds: 300,
          message_count: loaded ? 1284 : 0,
          message_rate_per_minute: loaded ? 256.8 : 0,
          rate_points: loaded ? [231, 262, 249, 270, 255, 17].map((count, i) => ({ timestamp: ago((5 - i) * 60_000), count })) : [],
          top_topics: loaded ? topTopics.map(([name, count]) => ({ name, count, percentage: (count * 100) / 1284 })) : [],
          top_clients: [],
          top_clients_available: false,
          top_clients_note: 'Client identity is not included in MQTT application messages observed via wildcard subscriptions.',
          persistence: 'In-memory: resets when MCM restarts.',
        },
      },
    },
  }
}

// A paged listing envelope that filters, pages and counts like the server.
function paged(rowsKey, all, url, matches) {
  const params = new URL(url).searchParams
  const found = all.filter((row) => matches(row, params))
  const limit = Number(params.get('limit') ?? 25)
  const offset = Number(params.get('offset') ?? 0)
  return json(200, { [rowsKey]: found.slice(offset, offset + limit), total: found.length, limit, offset })
}

function routesFor(state) {
  const loaded = state === 'loaded'
  const error = state === 'error'
  return {
    'GET /api/v1/auth/me': () => json(200, me),
    'GET /api/v1/status': () => (error ? json(503, { error: 'broker status unavailable' }) : json(200, status(state))),
    'GET /api/v1/mqtt-users': (url) => (error
      ? json(500, { error: 'Could not read the MQTT users: database is locked.' })
      : paged('users', loaded ? mqttUsers : [], url, (row, p) => row.username.toLowerCase().includes((p.get('q') ?? '').toLowerCase()))),
    'GET /api/v1/deployments': (url) => (error
      ? json(500, { error: 'Could not read the deployment history.' })
      : paged('deployments', loaded ? deployments : [], url, (row, p) => !p.get('status') || row.status === p.get('status'))),
    'POST /api/v1/deployments/preview': () => (error
      ? json(500, { error: 'Rendering the ACL file failed: the store is not readable.' })
      : json(200, loaded ? previewLoaded : previewEmpty)),
  }
}

// Broker events of the live stream (useBrokerStream.ts shapes).
function brokerEvents(state) {
  if (state !== 'loaded') return [{ type: 'broker_status', status: 'connected', observed_at: ago(0) }]
  return [
    { type: 'broker_status', status: 'connected', observed_at: ago(0) },
    ...[['factory/line1/temperature', '{"celsius":21.4,"sensor":"t1"}'], ['cold-room/a/temperature', '{"celsius":3.1}'], ['packaging/plc/state', '{"state":"running","count":5521}']].map(([topic, payload], i) => ({
      type: 'topic_message', source: 'observed', topic, payload_preview: payload, payload_format: 'json', payload_bytes: payload.length, truncated: false, observed_at: ago((3 - i) * 1000),
    })),
    { type: 'broker_log_entry', source: 'mosquitto_log', severity: 'warning', message: 'Client forklift-7 has exceeded timeout, disconnecting.', observed_at: ago(20_000) },
  ]
}

// ------------------------------------------------- in-page measuring helpers
// Injected before the app loads; every measurement runs in the page.
function reviewHelpers(leverPrimary) {
  const canvas = document.createElement('canvas')
  canvas.width = canvas.height = 1
  const ctx = canvas.getContext('2d', { willReadFrequently: true })
  let unparsed = 0
  // Paints the layers (outermost first) over white and reads the sRGB pixel:
  // the browser resolves oklch/color-mix and composites the alpha.
  function paint(layers) {
    ctx.globalAlpha = 1
    ctx.fillStyle = '#fff'
    ctx.fillRect(0, 0, 1, 1)
    for (const { color, alpha = 1 } of layers) {
      ctx.fillStyle = '#010203'
      ctx.fillStyle = color
      if (ctx.fillStyle === '#010203' && color !== '#010203') unparsed++
      ctx.globalAlpha = alpha
      ctx.fillRect(0, 0, 1, 1)
      ctx.globalAlpha = 1
    }
    return [...ctx.getImageData(0, 0, 1, 1).data.slice(0, 3)]
  }
  const channel = (v) => { v /= 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4 }
  const luminance = ([r, g, b]) => 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
  const ratio = (a, b) => { const [x, y] = [luminance(a), luminance(b)].sort((m, n) => n - m); return (x + 0.05) / (y + 0.05) }
  const transparent = (color) => /^rgba\(.*,\s*0\)$/.test(color) || color === 'transparent'
  // Background colours from the element up to the first opaque one, outermost first.
  function backgrounds(el) {
    const layers = []
    let image = false
    for (let node = el; node; node = node.parentElement) {
      const s = getComputedStyle(node)
      if (s.backgroundImage !== 'none') image = true
      if (!transparent(s.backgroundColor)) {
        layers.unshift({ color: s.backgroundColor })
        if (paintAlpha(s.backgroundColor) === 255) break
      }
    }
    return { layers, image }
  }
  function paintAlpha(color) {
    ctx.clearRect(0, 0, 1, 1)
    ctx.fillStyle = color
    ctx.fillRect(0, 0, 1, 1)
    return ctx.getImageData(0, 0, 1, 1).data[3]
  }
  function opacity(el) {
    let value = 1
    for (let node = el; node; node = node.parentElement) value *= Number(getComputedStyle(node).opacity)
    return value
  }
  // The part of a rect left after the overflow:hidden/clip ancestors (scroll
  // containers do not count: their content is reachable by scrolling).
  function clipped(rect, el) {
    let { left, top, right, bottom } = rect
    for (let node = el; node && node !== document.body; node = node.parentElement) {
      const s = getComputedStyle(node)
      if (s.clipPath === 'inset(50%)' || s.clip === 'rect(0px, 0px, 0px, 0px)') return 0
      if (/hidden|clip/.test(s.overflowX + s.overflowY)) {
        const r = node.getBoundingClientRect()
        left = Math.max(left, r.left); top = Math.max(top, r.top); right = Math.min(right, r.right); bottom = Math.min(bottom, r.bottom)
      }
    }
    return Math.max(0, right - left) * Math.max(0, bottom - top)
  }
  function visible(el, rect = el.getBoundingClientRect()) {
    const s = getComputedStyle(el)
    return rect.width * rect.height > 0 && s.visibility !== 'hidden' && opacity(el) > 0.05 && clipped(rect, el) > 2
  }
  function describe(el) {
    const region = el.closest('main') ? 'main' : el.closest('nav') ? 'nav' : el.closest('aside') ? 'aside' : 'page'
    const name = el.getAttribute('aria-label') || el.getAttribute('placeholder') || (el.textContent || '').trim().replace(/\s+/g, ' ')
    const role = el.getAttribute('role')
    return `${region} ${el.tagName.toLowerCase()}${role ? `[role=${role}]` : ''}${el.type && el.tagName !== 'BUTTON' ? `[type=${el.type}]` : ''} "${name.slice(0, 60)}"`
  }
  function large(s) {
    const size = parseFloat(s.fontSize)
    return size >= 24 || (size >= 18.66 && Number(s.fontWeight) >= 700)
  }
  function check(el, color, kind, found) {
    const s = getComputedStyle(el)
    const { layers, image } = backgrounds(el)
    const bg = paint(layers)
    const fg = paint([...layers, { color, alpha: opacity(el) }])
    const value = ratio(fg, bg)
    const needed = large(s) ? 3 : 4.5
    found.checked++
    if (!found.lowest || value / needed < found.lowest.ratio / found.lowest.needed) found.lowest = { element: describe(el), ratio: Number(value.toFixed(2)), needed }
    if (value < needed) found.failures.push({ element: describe(el), kind, ratio: Number(value.toFixed(2)), needed, fontSize: s.fontSize, fontWeight: s.fontWeight, color: `rgb(${fg})`, background: `rgb(${bg})`, ...(image ? { note: 'background image in the chain: the colour behind the gradient was used' } : {}) })
  }

  window.__review = {
    levers() {
      const scope = document.querySelector('main') ?? document.body
      return [...scope.querySelectorAll('button')].filter((b) => b.className.includes(leverPrimary) && visible(b)).map((b) => `${b.textContent.trim()}${b.disabled ? ' (disabled)' : ''}`)
    },
    contrast() {
      const found = { checked: 0, exempt: 0, failures: [] }
      const seen = new Set()
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        const el = node.parentElement
        if (!node.textContent.trim() || !el || seen.has(el) || el.closest('svg, script, style, noscript, option, select, textarea')) continue
        seen.add(el)
        const range = document.createRange()
        range.selectNodeContents(node)
        if (!visible(el, range.getBoundingClientRect())) continue
        // WCAG 1.4.3: text of an inactive (disabled) control has no contrast requirement.
        if (el.closest(':disabled, [aria-disabled="true"]')) { found.exempt++; continue }
        check(el, getComputedStyle(el).color, 'text', found)
      }
      for (const field of document.querySelectorAll('input:not([type=checkbox]):not([type=radio]):not([type=hidden]), select, textarea')) {
        if (!visible(field)) continue
        if (field.disabled) { found.exempt++; continue }
        if (field.value || field.tagName === 'SELECT') check(field, getComputedStyle(field).color, 'value', found)
        else if (field.placeholder) check(field, getComputedStyle(field, '::placeholder').color, 'placeholder', found)
      }
      return { ...found, unparsed }
    },
    // The focused element and whether its indicator is visible: an outline of
    // at least 2px or a box-shadow ring with a spread of at least 2px, at 3:1
    // against what is behind it, not clipped away on every side.
    focus() {
      const el = document.activeElement
      if (!el || el === document.body || el === document.documentElement) return null
      el.__reviewKey ??= `k${Math.random()}`
      // transition-colors also transitions outline-color: measure the end state, not the first frame.
      document.getAnimations().forEach((animation) => animation.finish())
      const s = getComputedStyle(el)
      const rect = el.getBoundingClientRect()
      const width = s.outlineStyle === 'none' ? 0 : parseFloat(s.outlineWidth) || 0
      const outline = width >= 2 && !transparent(s.outlineColor)
      const ring = s.boxShadow !== 'none' && s.boxShadow.split(/,(?![^(]*\))/).some((layer) => {
        const lengths = layer.replace(/rgba?\([^)]*\)|oklch\([^)]*\)|oklab\([^)]*\)|color\([^)]*\)/g, '').match(/-?[\d.]+px/g) ?? []
        return parseFloat(lengths[3] ?? '0') >= 2
      })
      const problems = []
      if (!visible(el, rect)) problems.push('focused element not visible')
      if (!outline && !ring) problems.push(`no indicator (outline ${s.outlineStyle} ${s.outlineWidth}, box-shadow ${s.boxShadow === 'none' ? 'none' : 'without a ring'})`)
      let indicatorContrast
      if (outline) {
        const { layers } = backgrounds(el.parentElement ?? el)
        const bg = paint(layers)
        const ring = paint([...layers, { color: s.outlineColor }])
        indicatorContrast = Number(ratio(ring, bg).toFixed(2))
        if (indicatorContrast < 3) problems.push(`outline contrast ${indicatorContrast}:1 < 3:1 (${s.outlineColor} → rgb(${ring}) on rgb(${bg}))`)
        const grow = width + (parseFloat(s.outlineOffset) || 0)
        const sides = { left: rect.left - grow, top: rect.top - grow, right: rect.right + grow, bottom: rect.bottom + grow }
        const cut = new Set()
        for (let node = el.parentElement; node && node !== document.body; node = node.parentElement) {
          const ns = getComputedStyle(node)
          if (!/hidden|clip|auto|scroll/.test(ns.overflowX + ns.overflowY)) continue
          const r = node.getBoundingClientRect()
          if (sides.left < r.left - 0.5) cut.add('left')
          if (sides.top < r.top - 0.5) cut.add('top')
          if (sides.right > r.right + 0.5) cut.add('right')
          if (sides.bottom > r.bottom + 0.5) cut.add('bottom')
        }
        if (cut.size === 4) problems.push('outline clipped on every side')
        else if (cut.size) problems.push(`note: outline clipped on ${[...cut].join(', ')}`)
      }
      return { key: el.__reviewKey, element: describe(el), indicatorContrast, problems, rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } }
    },
    // Horizontal overflow of the document and the outermost elements that
    // escape the viewport outside a scroll container of their own.
    reflow() {
      const root = document.documentElement
      const offenders = []
      for (const el of document.body.querySelectorAll('*')) {
        const r = el.getBoundingClientRect()
        if (!(r.right > innerWidth + 1 || r.left < -1) || !visible(el, r)) continue
        let contained = false
        for (let node = el.parentElement; node && node !== document.body; node = node.parentElement) {
          if (/auto|scroll|hidden|clip/.test(getComputedStyle(node).overflowX)) { contained = true; break }
          if (offenders.some((o) => o.el === node)) { contained = true; break }
        }
        if (!contained) offenders.push({ el, element: describe(el), left: Math.round(r.left), right: Math.round(r.right) })
      }
      // The innermost descendant that still overflows names the cause (a select, a long word).
      for (const offender of offenders) {
        let cause = offender.el
        for (let next; (next = [...cause.children].filter((c) => c.getBoundingClientRect().right > innerWidth + 1).sort((a, b) => b.getBoundingClientRect().right - a.getBoundingClientRect().right)[0]);) cause = next
        if (cause !== offender.el) offender.cause = `${describe(cause)} (${Math.round(cause.getBoundingClientRect().width)}px wide)`
      }
      return { viewport: innerWidth, scrollWidth: root.scrollWidth, documentScroll: root.scrollWidth > root.clientWidth + 1, offenders: offenders.slice(0, 5).map(({ el, ...rest }) => rest) }
    },
  }
}

// ------------------------------------------------------------------ runner
const server = await preview({ root: frontend, logLevel: 'silent', preview: { host: '127.0.0.1', port: 4319 } })
const base = server.resolvedUrls.local[0].replace(/\/$/, '')
const browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_CHROMIUM_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH } : {}) })
const unknownRequests = new Set()
const pageErrors = []

async function openContext(state, viewport, { pending = state === 'loaded' } = {}) {
  const context = await browser.newContext({ viewport, deviceScaleFactor: 1, reducedMotion: 'reduce' })
  await context.addInitScript(([token, pendingDeploy]) => {
    window.localStorage.setItem('mcm_admin_token', token)
    if (pendingDeploy) window.localStorage.setItem('mcm_deploy_pending', '1')
  }, [fakeToken, pending])
  await context.addInitScript(reviewHelpers, leverPrimary)
  const routes = routesFor(state)
  await context.route(/\/api\/v1\//, (route) => {
    const request = route.request()
    const url = request.url()
    const key = `${request.method()} ${new URL(url).pathname}`
    const handler = routes[key]
    if (!handler) unknownRequests.add(key)
    return route.fulfill(handler ? handler(url) : json(404, { error: 'not mocked by the review' }))
  })
  await context.routeWebSocket(/\/api\/v1\/broker\/events/, (ws) => {
    // The error state: MCM's event stream is down too.
    if (state === 'error') { ws.close({ code: 1011, reason: 'unavailable' }); return }
    setTimeout(() => brokerEvents(state).forEach((event) => ws.send(JSON.stringify(event))), 150)
  })
  const page = await context.newPage()
  page.setDefaultTimeout(15000)
  page.on('pageerror', (error) => pageErrors.push(`${state} ${viewport.width}: ${error.message}`))
  return { context, page }
}

// Waits until no view reports loading and fonts are in.
async function settle(page) {
  await page.waitForFunction(() => {
    const main = document.querySelector('main')
    return main && !/Loading|Checking broker status|Generating…|Restoring/.test(main.innerText)
  })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(400)
}

async function open(page, view, { generatePreview = view === 'deploy' } = {}) {
  await page.goto(`${base}${views[view]}`)
  await settle(page)
  if (generatePreview) {
    // A scripted click, so focus stays where the keyboard pass starts it.
    const response = page.waitForResponse((r) => r.url().endsWith('/deployments/preview'))
    await page.getByRole('button', { name: 'Preview Changes', exact: true }).evaluate((button) => button.click())
    await response
    await settle(page)
  }
}

// Puts the sequential focus starting point at the top of the document.
async function resetFocus(page) {
  await page.evaluate(() => {
    const start = document.createElement('div')
    start.tabIndex = -1
    document.body.prepend(start)
    start.focus()
    start.remove()
  })
}

// Tabs through up to 60 stops; a failing stop is photographed with its surroundings.
async function focusPass(page, name) {
  await resetFocus(page)
  const stops = []
  const photographed = new Set()
  let first
  for (let i = 0; i < 60; i++) {
    await page.keyboard.press('Tab')
    const info = await page.evaluate(() => window.__review.focus())
    if (!info || info.key === first) break
    first ??= info.key
    const finding = `${info.element} ${info.problems.join()}`
    if (info.problems.length && !photographed.has(finding)) {
      photographed.add(finding)
      // The element and 16px around it, cut to the viewport (it may sit off-screen in a scroll region).
      const viewport = page.viewportSize()
      const x = Math.min(Math.max(0, info.rect.x - 16), viewport.width)
      const y = Math.min(Math.max(0, info.rect.y - 16), viewport.height)
      const width = Math.min(viewport.width, info.rect.x + info.rect.width + 16) - x
      const height = Math.min(viewport.height, info.rect.y + info.rect.height + 16) - y
      if (width > 0 && height > 0) {
        info.evidence = `focus-${name}-${stops.length + 1}.jpg`
        await page.screenshot({ path: `${out}/${info.evidence}`, type: 'jpeg', quality: 80, clip: { x, y, width, height } })
      }
    }
    stops.push(info)
  }
  return {
    stops: stops.length,
    order: stops.map((s) => s.element),
    failures: stops.filter((s) => s.problems.some((p) => !p.startsWith('note:'))).map(({ element, problems, evidence }) => ({ element, problems, evidence })),
    notes: stops.filter((s) => s.problems.some((p) => p.startsWith('note:'))).map(({ element, problems, evidence }) => ({ element, problems, evidence })),
  }
}

async function reflowAt(page, width, restore, name) {
  await page.setViewportSize({ width, height: 800 })
  await page.waitForTimeout(300)
  const result = await page.evaluate(() => window.__review.reflow())
  if (result.documentScroll) {
    result.evidence = `reflow-${name}-${width}.jpg`
    await page.screenshot({ path: `${out}/${result.evidence}`, type: 'jpeg', quality: 70, fullPage: true })
  }
  await page.setViewportSize(restore)
  return result
}

const screens = []
for (const view of Object.keys(views)) {
  for (const [viewportName, viewport] of Object.entries(viewports)) {
    for (const state of states) {
      const { context, page } = await openContext(state, viewport)
      try {
        await open(page, view)
        const screenshot = `${view}-${viewportName}-${state}.jpg`
        await page.screenshot({ path: `${out}/${screenshot}`, fullPage: true, type: 'jpeg', quality: 70 })
        const levers = await page.evaluate(() => window.__review.levers())
        const contrast = await page.evaluate(() => window.__review.contrast())
        const focus = await focusPass(page, `${view}-${viewportName}-${state}`)
        const reflow320 = await reflowAt(page, 320, viewport, `${view}-${viewportName}-${state}`)
        const reflow640 = await reflowAt(page, 640, viewport, `${view}-${viewportName}-${state}`)
        screens.push({ view, viewport: viewportName, state, screenshot, levers, contrast, focus, reflow: { 320: reflow320, 640: reflow640 } })
        console.log(`[review] ${view} ${viewportName} ${state}: levers ${levers.length}, contrast failures ${contrast.failures.length}, focus failures ${focus.failures.length}/${focus.stops}, reflow 320 ${reflow320.documentScroll ? 'SCROLLS' : 'ok'}, 640 ${reflow640.documentScroll ? 'SCROLLS' : 'ok'}`)
      } catch (error) {
        screens.push({ view, viewport: viewportName, state, error: error.message })
        console.error(`[review] ${view} ${viewportName} ${state} failed: ${error.message}`)
      } finally {
        await context.close()
      }
    }
  }
}

// ------------------------------------------------------- keyboard journeys
const focused = (page) => page.evaluate(() => window.__review.focus())

// Presses Tab until the focused element satisfies the predicate (evaluated in
// the page with its argument); returns the number of presses.
async function tabUntil(page, predicate, arg, limit = 120) {
  for (let presses = 1; presses <= limit; presses++) {
    await page.keyboard.press('Tab')
    if (await page.evaluate(predicate, arg)) return presses
  }
  throw new Error(`not reached in ${limit} Tab presses (last: ${(await focused(page))?.element ?? 'body'})`)
}
const isLink = (href) => document.activeElement?.matches(`a[href="${href}"]`)
const isUsersLinkOrExpand = () => document.activeElement?.matches('a[href="/users"], button[aria-label="Expand MQTT Access"]')

async function reachUsersLink(page) {
  let presses = await tabUntil(page, isUsersLinkOrExpand)
  if (!(await page.evaluate(isLink, '/users'))) {
    await page.keyboard.press('Enter')
    presses += await tabUntil(page, isLink, '/users')
  }
  return presses
}

async function journey(name, run) {
  const { context, page } = await openContext('loaded', viewports.desktop)
  const steps = []
  try {
    await run(page, steps)
    return { name, ok: steps.every((s) => s.ok !== false), steps }
  } catch (error) {
    return { name, ok: false, steps, error: error.message }
  } finally {
    await context.close()
  }
}

const journeys = []
journeys.push(await journey('Dashboard: primary lever and MQTT users link (desktop)', async (page, steps) => {
  await open(page, 'dashboard')
  await resetFocus(page)
  const presses = await tabUntil(page, (lever) => document.activeElement?.className.includes(lever), leverPrimary)
  const lever = (await focused(page)).element
  await page.keyboard.press('Enter')
  await page.waitForURL(/\/deploy$/)
  steps.push({ step: `Tab to the primary lever (${lever}), Enter opens /deploy`, tabs: presses, ok: true })
  await open(page, 'dashboard')
  await resetFocus(page)
  const toUsers = await reachUsersLink(page)
  await page.keyboard.press('Enter')
  await page.waitForURL(/\/users$/)
  steps.push({ step: 'Tab to the MQTT users nav link, Enter opens /users', tabs: toUsers, ok: true })
}))

journeys.push(await journey('Dashboard: Menu to MQTT users (mobile 390)', async (page, steps) => {
  await page.setViewportSize(viewports.mobile)
  await open(page, 'dashboard')
  await resetFocus(page)
  const toMenu = await tabUntil(page, () => document.activeElement?.getAttribute('aria-controls') === 'primary-menu')
  await page.keyboard.press('Enter')
  const toUsers = await reachUsersLink(page)
  await page.keyboard.press('Enter')
  await page.waitForURL(/\/users$/)
  const menuClosed = await page.locator('#primary-menu').isHidden()
  steps.push({ step: 'Tab to Menu, Enter opens it', tabs: toMenu, ok: true })
  steps.push({ step: `Tab to the MQTT users link, Enter opens /users (menu closed after: ${menuClosed})`, tabs: toUsers, ok: menuClosed })
}))

journeys.push(await journey('MQTT users: find a user, read its row, delete confirmation focus', async (page, steps) => {
  await open(page, 'users')
  await resetFocus(page)
  const toSearch = await tabUntil(page, () => document.activeElement?.matches('input[type=search]'))
  const searched = page.waitForResponse((r) => r.url().includes('q=sensor-line-3'))
  await page.keyboard.type('sensor-line-3')
  await searched
  await settle(page)
  const rows = await page.locator('main tbody tr').evaluateAll((trs) => trs.map((tr) => [...tr.cells].slice(0, 2).map((td) => td.innerText.trim())))
  steps.push({ step: `Tab to the search, type sensor-line-3; rows read: ${JSON.stringify(rows)}`, tabs: toSearch, ok: rows.length === 1 && rows[0][0] === 'sensor-line-3' && rows[0][1] === 'disabled' })
  const toDelete = await tabUntil(page, () => document.activeElement?.textContent.trim() === 'Delete' && document.activeElement.closest('tr')?.innerText.includes('sensor-line-3'))
  await page.keyboard.press('Enter')
  const onCancel = (await focused(page))?.element ?? 'body'
  steps.push({ step: `Tab to Delete, Enter: focus on ${onCancel}`, tabs: toDelete, ok: / button "Cancel"/.test(onCancel) })
  await page.keyboard.press('Enter')
  const back = (await focused(page))?.element ?? 'body'
  steps.push({ step: `Enter on Cancel: focus back on ${back}`, tabs: 0, ok: / button "Delete"/.test(back) && (await page.evaluate(() => document.activeElement.closest('tr')?.cells[0].textContent)) === 'sensor-line-3' })
}))

journeys.push(await journey('Deploy: preview, Apply, MFA field (no apply)', async (page, steps) => {
  await open(page, 'deploy', { generatePreview: false })
  await resetFocus(page)
  const toPreview = await tabUntil(page, () => document.activeElement?.textContent.trim() === 'Preview Changes')
  const generated = page.waitForResponse((r) => r.url().endsWith('/deployments/preview'))
  await page.keyboard.press('Enter')
  await generated
  await settle(page)
  const afterPreview = (await focused(page))?.element ?? 'body'
  steps.push({ step: `Tab to Preview Changes, Enter generates the preview (focus after: ${afterPreview})`, tabs: toPreview, ok: true })
  const toApply = await tabUntil(page, () => document.activeElement?.textContent.trim() === 'Apply')
  await page.keyboard.press('Enter')
  const afterApply = (await focused(page))?.element ?? 'body'
  steps.push({ step: `Tab to Apply, Enter arms the apply (focus after: ${afterApply})`, tabs: toApply, ok: true })
  const isCode = () => document.activeElement?.matches('input[placeholder="Authenticator code"]')
  let toCode
  let direction = 'Tab'
  try {
    toCode = await tabUntil(page, isCode, undefined, 80)
  } catch {
    direction = 'Shift+Tab'
    for (toCode = 1; toCode <= 80; toCode++) {
      await page.keyboard.press('Shift+Tab')
      if (await page.evaluate(isCode)) break
    }
  }
  const reached = await page.evaluate(isCode)
  steps.push({ step: `${direction} to the MFA code field from where focus was left (stopped there, no apply)`, tabs: toCode, ok: reached })
}))

// ------------------------------------------------------------------ report
await browser.close()
await server.close()

const results = { generatedAt: new Date().toISOString(), base: 'vite preview of frontend/dist, every /api/v1 request mocked', viewports, screens, journeys, unknownRequests: [...unknownRequests].sort(), pageErrors }
await writeFile(`${out}/results.json`, `${JSON.stringify(results, null, 2)}\n`)

const reflowCell = (r) => (r.documentScroll ? `scrolls (${r.scrollWidth}px)` : 'ok')
const lines = [
  '# Operational views review',
  '',
  `Generated ${results.generatedAt}. Screens: ${screens.length}.`,
  '',
  '| View | Viewport | State | Primary levers | Contrast failures (checked) | Reflow 320 | Reflow 640 | Focus failures (stops) |',
  '| --- | --- | --- | --- | --- | --- | --- | --- |',
  ...screens.map((s) => (s.error
    ? `| ${s.view} | ${s.viewport} | ${s.state} | error: ${s.error} | | | | |`
    : `| ${s.view} | ${s.viewport} | ${s.state} | ${s.levers.length}${s.levers.length ? ` (${s.levers.join(', ')})` : ''} | ${s.contrast.failures.length} (${s.contrast.checked}) | ${reflowCell(s.reflow[320])} | ${reflowCell(s.reflow[640])} | ${s.focus.failures.length} (${s.focus.stops}) |`)),
  '',
  '## Keyboard journeys',
  '',
  ...journeys.flatMap((j) => [`### ${j.name}: ${j.ok ? 'ok' : 'FAILED'}`, '', ...j.steps.map((s) => `- ${s.ok === false ? 'FAIL' : 'ok'} · ${s.tabs} Tab · ${s.step}`), ...(j.error ? [`- error: ${j.error}`] : []), '']),
  '## Findings',
  '',
  ...screens.filter((s) => !s.error).flatMap((s) => {
    const items = [
      ...s.contrast.failures.map((f) => `- contrast ${f.ratio}:1 < ${f.needed}:1 · ${f.kind} ${f.element} · ${f.color} on ${f.background}${f.note ? ` (${f.note})` : ''}`),
      ...s.focus.failures.map((f) => `- focus · ${f.element} · ${f.problems.join('; ')} · ${f.evidence}`),
      ...s.focus.notes.map((f) => `- focus note · ${f.element} · ${f.problems.join('; ')}${f.evidence ? ` · ${f.evidence}` : ''}`),
      ...[320, 640].flatMap((w) => s.reflow[w].offenders.map((o) => `- reflow ${w} · ${o.element} spans ${o.left}..${o.right}px${o.cause ? `, cause ${o.cause}` : ''}${s.reflow[w].evidence ? ` · ${s.reflow[w].evidence}` : ''}`)),
      ...(s.levers.length > 1 ? [`- ${s.levers.length} primary levers: ${s.levers.join(', ')}`] : []),
    ]
    // Repeated rows (one per table row) collapse into one line with a count.
    const counted = new Map()
    for (const item of items) {
      const key = item.replace(/ · [\w-]+\.jpg$/, '')
      const entry = counted.get(key) ?? { item, count: 0 }
      entry.count++
      counted.set(key, entry)
    }
    const unique = [...counted.values()].map(({ item, count }) => (count > 1 ? `${item} (×${count})` : item))
    return unique.length ? [`### ${s.view} · ${s.viewport} · ${s.state}`, '', ...unique, ''] : []
  }),
  `Unmocked API requests (answered 404): ${results.unknownRequests.join(', ') || 'none'}`,
  '',
  `Page errors: ${pageErrors.join(' | ') || 'none'}`,
  '',
]
await writeFile(`${out}/summary.md`, lines.join('\n'))
console.log(`[review] results: ${out}/results.json and summary.md`)

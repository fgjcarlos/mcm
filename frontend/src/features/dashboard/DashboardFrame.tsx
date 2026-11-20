import { useRef, useState, type ReactNode } from 'react'
import type { AdminUser } from '../auth/useAuthSession'
import type { BrokerConnectionStatus, BrokerStatusLoad, BrokerStreamState } from '../broker/useBrokerStream'
import { lampDot } from '../ui/styles'

export type NavItem = {
  id: string
  group?: string
  label: string
  title: string
  description: string
  path: string
  /** Minimum role required to see this nav item. Hidden from lower roles. */
  minRole?: string
  /** Shown only in advanced mode. */
  advanced?: boolean
}

type DashboardFrameProps = {
  navItems: NavItem[]
  activeItem: NavItem
  activeId: string
  brokerStatus: BrokerConnectionStatus
  statusLoad: BrokerStatusLoad
  streamState: BrokerStreamState
  uniqueTopicCount: number
  logCount: number
  currentUser: AdminUser
  advanced: boolean
  onToggleAdvanced: () => void
  onSelectNav: (item: NavItem) => void
  onLogout: () => void
  children: ReactNode
}

export function DashboardFrame({
  navItems,
  activeItem,
  activeId,
  brokerStatus,
  statusLoad,
  streamState,
  uniqueTopicCount,
  logCount,
  currentUser,
  advanced,
  onToggleAdvanced,
  onSelectNav,
  onLogout,
  children,
}: DashboardFrameProps) {
  // Connected or disconnected only once the status was read; an unread status is not a disconnection.
  const statusWord = statusLoad === 'loading' ? 'checking' : statusLoad === 'failed' ? 'unknown' : brokerStatus
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(() => new Set(['MQTT Access', 'Changes']))
  // Below lg the sidebar folds behind a menu button so the content comes first.
  const [menuOpen, setMenuOpen] = useState(false)
  const menuButtonRef = useRef<HTMLButtonElement>(null)
  const closeMenu = () => {
    setMenuOpen(false)
    menuButtonRef.current?.focus()
  }

  const navLink = (item: NavItem) => {
    const isActive = item.id === activeId
    return (
      <a
        key={item.id}
        href={item.path}
        aria-current={isActive ? 'page' : undefined}
        onClick={(event) => {
          event.preventDefault()
          onSelectNav(item)
          if (menuOpen) closeMenu()
        }}
        // The focus ring is drawn inside: the collapsible group and the scrolling sidebar clip anything outside.
        className={`flex items-center gap-2.5 rounded-plate px-2 py-2.5 text-sm lg:py-2 transition-colors duration-150 focus-visible:outline-offset-[-2px] ${
          isActive
            ? 'bg-frame-high font-semibold text-enamel-bright'
            : 'text-enamel-soft hover:bg-frame-raised hover:text-enamel-bright'
        }`}
      >
        {/* The current view is lit: lamp, weight and fill, not colour alone. */}
        <span aria-hidden="true" className={`size-1.5 shrink-0 rounded-full ${isActive ? 'bg-enamel-bright' : ''}`} />
        {item.label}
      </a>
    )
  }

  return (
    <div className="min-h-screen bg-frame text-enamel lg:flex">
      <aside
        className="border-b border-track lg:sticky lg:top-0 lg:h-screen lg:w-72 lg:shrink-0 lg:overflow-y-auto lg:border-r lg:border-b-0"
        onKeyDown={({ key }) => {
          if (key === 'Escape' && menuOpen) closeMenu()
        }}
      >
        <div className="flex items-center justify-between gap-3 px-4 py-3 lg:px-5 lg:pt-6">
          <h1 className="flex min-w-0 items-center gap-2.5 font-display text-base font-semibold uppercase leading-none tracking-[0.1em] text-enamel-bright">
            <Mark />
            <span className="sm:hidden">MCM</span>
            <span className="hidden sm:inline">Mosquitto Control Manager</span>
          </h1>
          <div className="flex shrink-0 items-center gap-2">
            {/* The site's amber plate: alpha is a caution that holds for the whole product. */}
            <span className="rounded-plate bg-lamp px-2 pb-[0.2rem] pt-[0.3rem] font-display text-sm font-bold uppercase leading-none tracking-[0.12em] text-frame">Alpha</span>
            <button
              ref={menuButtonRef}
              type="button"
              aria-expanded={menuOpen}
              aria-controls="primary-menu"
              onClick={() => setMenuOpen((open) => !open)}
              className="flex h-10 items-center gap-2 rounded-plate border border-track-bright px-3 font-mono text-xs uppercase tracking-[0.12em] text-enamel transition-colors duration-150 hover:bg-frame-raised hover:text-enamel-bright lg:hidden"
            >
              <Glyph shape={menuOpen ? 'close' : 'menu'} />
              Menu
            </button>
          </div>
        </div>

        <div id="primary-menu" className={`${menuOpen ? 'block' : 'hidden'} border-t border-track px-4 pb-6 lg:block lg:border-t-0 lg:px-5`}>
          <div className="border-b border-track py-4">
            <p className="text-xs text-enamel-muted">Broker</p>
            {/* Connected is not verified, so no green: an enamel lamp when up, amber when not, always with the word. */}
            <p className="mt-1 flex items-center gap-2 font-mono text-sm text-enamel-bright">
              <span aria-hidden="true" className={`size-2.5 shrink-0 rounded-full ${statusWord === 'connected' ? 'bg-enamel' : statusWord === 'checking' ? lampDot.off : 'bg-lamp'}`} />
              <span className="capitalize">{statusWord}</span>
            </p>
            <p className="mt-1 text-xs text-enamel-muted">Event stream: {streamState}</p>
          </div>

          <button
            type="button"
            role="switch"
            aria-checked={advanced}
            aria-labelledby="advanced-mode-label"
            aria-describedby="advanced-mode-description"
            onClick={onToggleAdvanced}
            className="flex w-full items-center justify-between gap-3 border-b border-track py-3 text-left transition-colors duration-150 hover:text-enamel-bright"
          >
            <span className="min-w-0">
              <span id="advanced-mode-label" className="block text-sm font-semibold text-enamel-bright">Advanced mode</span>
              <span id="advanced-mode-description" className="mt-0.5 block text-xs text-enamel-muted">{advanced ? 'Every broker, change and administration view' : 'MQTT users, ACLs, deploy and logs'}</span>
            </span>
            {/* On and off read from position and fill, not colour alone. */}
            <span aria-hidden="true" className={`relative h-6 w-11 shrink-0 rounded-full border transition-colors duration-150 ${advanced ? 'border-enamel bg-frame-high' : 'border-track-bright'}`}>
              <span className={`absolute top-0.5 size-4 rounded-full transition-all duration-150 ${advanced ? 'left-[1.375rem] bg-enamel-bright' : 'left-0.5 bg-enamel-muted'}`} />
            </span>
          </button>

          <nav className="mt-2 divide-y divide-track" aria-label="Primary navigation">
            {navItems.filter((item) => !item.group).length ? <div className="py-3">{navItems.filter((item) => !item.group).map(navLink)}</div> : null}
            {['MQTT Access', 'Broker', 'Changes', 'Diagnostics', 'Panel administration'].map((group) => {
              const items = navItems.filter((item) => item.group === group)
              if (!items.length) return null
              const groupId = `navigation-${group.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`
              const hasActiveItem = items.some((item) => item.id === activeId)
              const expanded = hasActiveItem || expandedGroups.has(group)
              const hint = group === 'Diagnostics' ? 'Observation · read-only' : groupHints[group]
              return (
                <section key={group} className="py-3">
                  <div className="flex items-center justify-between gap-2">
                    <h2 id={`${groupId}-heading`} className="text-sm font-semibold text-enamel-bright">{group}</h2>
                    <div className="flex items-center gap-2">
                      {/* The disabled toggle says why it cannot close. */}
                      {hasActiveItem ? <span id={`${groupId}-pinned`} className="text-xs text-enamel-muted">Current view</span> : null}
                      <button
                        type="button"
                        aria-label={`${expanded ? 'Collapse' : 'Expand'} ${group}`}
                        aria-expanded={expanded}
                        aria-controls={groupId}
                        aria-describedby={hasActiveItem ? `${groupId}-pinned` : undefined}
                        disabled={hasActiveItem}
                        title={hasActiveItem ? `The ${group} group holds the current view and stays open` : undefined}
                        onClick={() => setExpandedGroups((current) => {
                          const next = new Set(current)
                          if (expanded) next.delete(group)
                          else next.add(group)
                          return next
                        })}
                        className="flex size-10 lg:size-8 shrink-0 items-center justify-center rounded-plate border border-track-bright text-enamel transition-colors duration-150 hover:bg-frame-raised hover:text-enamel-bright disabled:cursor-not-allowed disabled:border-track disabled:text-enamel-muted disabled:hover:bg-transparent"
                      >
                        <Glyph shape={expanded ? 'minus' : 'plus'} />
                      </button>
                    </div>
                  </div>
                  {hint ? <p className="mt-1 text-xs text-enamel-muted">{hint}</p> : null}
                  <div
                    id={groupId}
                    role="region"
                    aria-labelledby={`${groupId}-heading`}
                    inert={!expanded}
                    className={`overflow-hidden transition-[max-height,opacity] duration-200 ${expanded ? 'mt-2 max-h-[40rem] opacity-100' : 'max-h-0 opacity-0'}`}
                  >
                    {items.map(navLink)}
                  </div>
                </section>
              )
            })}
          </nav>

          <div className="flex items-center justify-between gap-3 border-t border-track pt-4">
            <div className="min-w-0">
              <p className="text-xs text-enamel-muted">Signed in</p>
              <p className="mt-1 truncate text-sm font-semibold text-enamel-bright">{currentUser.username}</p>
              <p className="mt-1.5 inline-flex rounded-plate bg-frame-high px-2 pb-[0.15rem] pt-[0.25rem] font-mono text-xs text-enamel ring-1 ring-inset ring-track-strong">{currentUser.role}</p>
            </div>
            <button
              type="button"
              onClick={onLogout}
              className="h-10 lg:h-9 shrink-0 rounded-plate border border-track-bright px-3 font-mono text-xs uppercase tracking-[0.12em] text-enamel transition-colors duration-150 hover:bg-frame-raised hover:text-enamel-bright"
            >
              Logout
            </button>
          </div>
        </div>
      </aside>

      <main className="min-w-0 flex-1 px-4 py-6 sm:px-6 lg:px-10 lg:py-10">
        <div className="mx-auto max-w-6xl">
          <header className="flex flex-col gap-6 xl:flex-row xl:items-end xl:justify-between">
            <div className="max-w-3xl">
              <h2 className="font-display text-3xl font-bold uppercase leading-none tracking-[0.01em] text-enamel-bright sm:text-4xl">{activeItem.title}</h2>
              <p className="mt-3 max-w-[68ch] text-base leading-relaxed text-enamel-soft">{activeItem.description}</p>
            </div>

            {/* The live counts belong to the dashboard home, not to every page. */}
            {activeId === 'dashboard' ? (
              <dl className="flex divide-x divide-track border-y border-track">
                <Metric label="Status" value={statusWord} />
                <Metric label="Topics" value={String(uniqueTopicCount)} />
                <Metric label="Logs" value={String(logCount)} />
              </dl>
            ) : null}
          </header>
          {/* The section rule: a set segment on the track hairline. */}
          <div aria-hidden="true" className="relative mt-6 h-px bg-track-strong">
            <span className="absolute -top-px left-0 h-[3px] w-12 bg-enamel" />
          </div>

          {children}
        </div>
      </main>
    </div>
  )
}

// Says whose accounts each group manages, so broker clients and console operators never blur.
const groupHints: Record<string, string> = {
  'MQTT Access': 'Broker clients · devices and apps',
  'Panel administration': 'MCM console · operator accounts',
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-20 flex-col px-4 py-3 first:pl-0 xl:first:pl-4">
      <dt className="order-2 mt-1 text-xs text-enamel-muted">{label}</dt>
      <dd className="font-mono text-xl capitalize tabular-nums text-enamel-bright">{value}</dd>
    </div>
  )
}

// The MCM mark: a route set through the points onto the diverging line, cleared by a lit lamp.
export function Mark() {
  return (
    <svg viewBox="0 0 32 32" width="24" height="24" aria-hidden="true" className="shrink-0">
      <g fill="none" strokeWidth="3">
        <path d="M9 25H30" className="stroke-enamel" />
        <path d="M2 25H9L18 16" className="stroke-lamp" />
      </g>
      <circle cx="24" cy="10" r="5.5" className="fill-lamp" />
    </svg>
  )
}

function Glyph({ shape }: { shape: 'menu' | 'close' | 'plus' | 'minus' }) {
  const paths = {
    menu: 'M2 4h12M2 8h12M2 12h12',
    close: 'M3.5 3.5l9 9M12.5 3.5l-9 9',
    plus: 'M8 3v10M3 8h10',
    minus: 'M3 8h10',
  }
  return (
    <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" className="shrink-0">
      <path d={paths[shape]} fill="none" stroke="currentColor" strokeWidth="1.5" />
    </svg>
  )
}

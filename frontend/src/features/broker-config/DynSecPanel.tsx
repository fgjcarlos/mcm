import { useState } from 'react'
import { can } from '../auth/permissions'
import { useDynSec } from './useDynSec'
import { dynSecACLTypes, dynSecBackendLabels, dynSecBackendTones } from './dynsecTypes'
import type {
  DynSecACL,
  DynSecClient,
  DynSecDefaultAccess,
  DynSecDocument,
  DynSecGroup,
  DynSecGuard,
  DynSecRole,
} from './dynsecTypes'

// emptyDocument is the security database of a broker that has none yet.
const emptyDocument: DynSecDocument = {
  clients: [],
  groups: [],
  roles: [],
  default_access: {
    publish_client_send: false,
    publish_client_receive: true,
    subscribe: false,
    unsubscribe: true,
  },
  anonymous_group: '',
}

export default function DynSecPanel({
  token,
  onLogout,
  role = '',
}: {
  token: string
  onLogout: () => void
  role?: string
}) {
  const { state, preview, adoption, guard, previewError, applyError, issues, isLoading, isPreviewing, isApplying, isAdopting, applied, requestPreview, apply, adopt } =
    useDynSec({ token, onLogout })
  const [draft, setDraft] = useState<DynSecDocument | undefined>()
  const [previewedDocument, setPreviewedDocument] = useState<DynSecDocument | null>(null)
  const isAdmin = can(role, 'deploy.apply')
  // The preview endpoint requires an operator; the apply endpoint an admin.
  // The panel stays visible to an auditor, but the buttons that would be
  // refused are disabled.
  const canPreview = can(role, 'deploy.preview')

  const base = state?.document ?? emptyDocument
  const document = draft ?? base
  const dirty = draft !== undefined
  // A plan only applies to the exact document that is on screen. Without
  // this guard the operator could apply a plan that no longer matches
  // what they reviewed.
  const isPreviewCurrent = Boolean(preview?.plan_id) && JSON.stringify(previewedDocument) === JSON.stringify(document)
  const applyEnabled = isAdmin && !isApplying && isPreviewCurrent && preview?.has_changes !== false

  const roleNames = document.roles.map((role) => role.rolename)
  const groupNames = document.groups.map((group) => group.groupname)

  const mutate = (change: (doc: DynSecDocument) => DynSecDocument) => {
    setDraft((current) => change(current ?? base))
  }

  const updateClient = (index: number, changes: Partial<DynSecClient>) => {
    mutate((doc) => ({ ...doc, clients: doc.clients.map((client, i) => (i === index ? { ...client, ...changes } : client)) }))
  }

  const renameClient = (index: number, username: string) => {
    mutate((doc) => {
      const previous = doc.clients[index]?.username ?? ''
      const clients = doc.clients.map((client, i) => (i === index ? { ...client, username } : client))
      if (!previous || previous === username) return { ...doc, clients }
      const groups = doc.groups.map((group) => ({
        ...group,
        clients: group.clients.map((ref) => (ref.username === previous ? { ...ref, username } : ref)),
      }))
      return { ...doc, clients, groups }
    })
  }

  const addClient = () => {
    mutate((doc) => ({
      ...doc,
      clients: [...doc.clients, { username: '', textname: '', textdescription: '', clientid: '', password: '', roles: [], groups: [] }],
    }))
  }

  const removeClient = (index: number) => {
    mutate((doc) => {
      const username = doc.clients[index]?.username ?? ''
      return {
        ...doc,
        clients: doc.clients.filter((_, i) => i !== index),
        groups: doc.groups.map((group) => ({ ...group, clients: group.clients.filter((ref) => ref.username !== username) })),
      }
    })
  }

  const updateGroup = (index: number, changes: Partial<DynSecGroup>) => {
    mutate((doc) => ({ ...doc, groups: doc.groups.map((group, i) => (i === index ? { ...group, ...changes } : group)) }))
  }

  const renameGroup = (index: number, groupname: string) => {
    mutate((doc) => {
      const previous = doc.groups[index]?.groupname ?? ''
      const groups = doc.groups.map((group, i) => (i === index ? { ...group, groupname } : group))
      if (!previous || previous === groupname) return { ...doc, groups }
      const clients = doc.clients.map((client) => ({
        ...client,
        groups: client.groups.map((ref) => (ref.groupname === previous ? { ...ref, groupname } : ref)),
      }))
      return { ...doc, groups, clients }
    })
  }

  const addGroup = () => {
    mutate((doc) => ({
      ...doc,
      groups: [...doc.groups, { groupname: '', textname: '', textdescription: '', roles: [], clients: [] }],
    }))
  }

  const removeGroup = (index: number) => {
    mutate((doc) => {
      const groupname = doc.groups[index]?.groupname ?? ''
      return {
        ...doc,
        groups: doc.groups.filter((_, i) => i !== index),
        clients: doc.clients.map((client) => ({
          ...client,
          groups: client.groups.filter((ref) => ref.groupname !== groupname),
        })),
      }
    })
  }

  const updateRole = (index: number, changes: Partial<DynSecRole>) => {
    mutate((doc) => ({ ...doc, roles: doc.roles.map((role, i) => (i === index ? { ...role, ...changes } : role)) }))
  }

  const renameRole = (index: number, rolename: string) => {
    mutate((doc) => {
      const previous = doc.roles[index]?.rolename ?? ''
      const roles = doc.roles.map((role, i) => (i === index ? { ...role, rolename } : role))
      if (!previous || previous === rolename) return { ...doc, roles }
      const clients = doc.clients.map((client) => ({
        ...client,
        roles: client.roles.map((ref) => (ref.rolename === previous ? { ...ref, rolename } : ref)),
      }))
      const groups = doc.groups.map((group) => ({
        ...group,
        roles: group.roles.map((ref) => (ref.rolename === previous ? { ...ref, rolename } : ref)),
      }))
      return { ...doc, roles, clients, groups }
    })
  }

  const addRole = () => {
    mutate((doc) => ({
      ...doc,
      roles: [...doc.roles, { rolename: '', textname: '', textdescription: '', acls: [] }],
    }))
  }

  const removeRole = (index: number) => {
    mutate((doc) => {
      const rolename = doc.roles[index]?.rolename ?? ''
      return {
        ...doc,
        roles: doc.roles.filter((_, i) => i !== index),
        clients: doc.clients.map((client) => ({
          ...client,
          roles: client.roles.filter((ref) => ref.rolename !== rolename),
        })),
        groups: doc.groups.map((group) => ({
          ...group,
          roles: group.roles.filter((ref) => ref.rolename !== rolename),
        })),
      }
    })
  }

  const updateDefaultAccess = (changes: Partial<DynSecDefaultAccess>) => {
    mutate((doc) => ({ ...doc, default_access: { ...doc.default_access, ...changes } }))
  }

  const handlePreview = () => {
    void requestPreview(document).then(() => setPreviewedDocument(document))
  }

  const handleApply = async () => {
    if (!preview?.plan_id || !applyEnabled) return
    const result = await apply({ plan_id: preview.plan_id, document: previewedDocument ?? document })
    if (result) {
      setDraft(undefined)
      setPreviewedDocument(null)
    }
  }

  const handleAdopt = async () => {
    await adopt()
  }

  const handleAdoptApply = async () => {
    if (!adoption?.plan_id || !isAdmin || isApplying) return
    await apply({ plan_id: adoption.plan_id })
  }

  if (isLoading && !state) {
    return (
      <section className="mt-8">
        <p className="text-sm text-slate-400">Loading the dynamic security state…</p>
      </section>
    )
  }

  if (!state) {
    return (
      <section className="mt-8">
        <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">
          Dynamic security management is not configured on this MCM instance.
        </div>
      </section>
    )
  }

  const adopted = state.backend === 'dynamic-security'

  return (
    <section className="mt-8 space-y-6">
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Dynamic security</p>
            <p className="mt-1 text-sm text-slate-300">
              The clients, groups and roles the broker enforces, and the report of the management channel.
            </p>
            {state.management_client ? (
              <p className="mt-1 text-xs text-slate-400">
                Management account: <span className="font-mono">{state.management_client}</span>
              </p>
            ) : null}
            <p className="mt-1 text-xs text-slate-400">{dirty ? 'Unsaved changes' : 'Saved'}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${dynSecBackendTones[state.backend] ?? dynSecBackendTones.none}`}>
              {dynSecBackendLabels[state.backend] ?? state.backend}
            </span>
            {adopted ? (
              <button
                type="button"
                onClick={handlePreview}
                disabled={!canPreview || isLoading || isPreviewing || isApplying}
                title={!canPreview ? 'Requires operator role' : undefined}
                className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
              >
                {isPreviewing ? 'Generating…' : 'Preview'}
              </button>
            ) : null}
          </div>
        </div>

        {previewError ? (
          <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100" role="alert">
            {previewError}
          </div>
        ) : null}
        {applyError ? (
          <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100" role="alert">
            {applyError}
          </div>
        ) : null}
        {applied ? (
          <div className="mt-5 rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5 text-sm text-emerald-100" role="status">
            The security database was updated and the broker enforces it.
          </div>
        ) : null}
        {state.warnings?.length ? (
          <div className="mt-5 rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Warnings</p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-amber-100">
              {state.warnings.map((warning, index) => (
                <li key={`${index}-${warning}`}>{warning}</li>
              ))}
            </ul>
          </div>
        ) : null}
        {issues.length > 0 ? (
          <div className="mt-5 rounded-2xl border border-rose-300/30 bg-rose-400/10 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-rose-200">Issues</p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-rose-100">
              {issues.map((issue, index) => (
                <li key={`${issue.kind}-${issue.object}-${index}`}>
                  {issue.object ? <span className="font-mono text-xs">{issue.object}</span> : null}
                  {issue.object ? ' · ' : null}
                  {issue.message}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        {guard && !preview ? <GuardReport guard={guard} /> : null}
      </div>

      {adopted ? (
        <>
          <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Clients</p>
                <p className="mt-1 text-sm text-slate-400">The clients the plugin authenticates.</p>
              </div>
              <button
                type="button"
                onClick={addClient}
                disabled={isApplying || isPreviewing}
                className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
              >
                Add client
              </button>
            </div>
            <div className="mt-5 space-y-4">
              {document.clients.length === 0 ? (
                <p className="text-sm text-slate-400">No client is configured.</p>
              ) : null}
              {document.clients.map((client, index) => (
                <ClientCard
                  key={index}
                  client={client}
                  index={index}
                  roleNames={roleNames}
                  groupNames={groupNames}
                  disabled={isApplying || isPreviewing}
                  onChange={(changes) => updateClient(index, changes)}
                  onRename={(username) => renameClient(index, username)}
                  onRemove={() => removeClient(index)}
                  onRolesChange={(roles) => updateClient(index, { roles })}
                  onGroupsChange={(groups) => updateClient(index, { groups })}
                />
              ))}
            </div>
          </div>

          <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Groups</p>
                <p className="mt-1 text-sm text-slate-400">The groups of clients that share the same roles.</p>
              </div>
              <button
                type="button"
                onClick={addGroup}
                disabled={isApplying || isPreviewing}
                className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
              >
                Add group
              </button>
            </div>
            <div className="mt-5 space-y-4">
              {document.groups.length === 0 ? (
                <p className="text-sm text-slate-400">No group is configured.</p>
              ) : null}
              {document.groups.map((group, index) => (
                <GroupCard
                  key={index}
                  group={group}
                  index={index}
                  roleNames={roleNames}
                  clientNames={document.clients.map((client) => client.username)}
                  disabled={isApplying || isPreviewing}
                  onChange={(changes) => updateGroup(index, changes)}
                  onRename={(groupname) => renameGroup(index, groupname)}
                  onRemove={() => removeGroup(index)}
                  onRolesChange={(roles) => updateGroup(index, { roles })}
                  onClientsChange={(clients) => updateGroup(index, { clients })}
                />
              ))}
            </div>
          </div>

          <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Roles</p>
                <p className="mt-1 text-sm text-slate-400">The roles that carry the topic permissions.</p>
              </div>
              <button
                type="button"
                onClick={addRole}
                disabled={isApplying || isPreviewing}
                className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
              >
                Add role
              </button>
            </div>
            <div className="mt-5 space-y-4">
              {document.roles.length === 0 ? (
                <p className="text-sm text-slate-400">No role is configured.</p>
              ) : null}
              {document.roles.map((role, index) => (
                <RoleCard
                  key={index}
                  role={role}
                  index={index}
                  disabled={isApplying || isPreviewing}
                  onChange={(changes) => updateRole(index, changes)}
                  onRename={(rolename) => renameRole(index, rolename)}
                  onRemove={() => removeRole(index)}
                  onACLsChange={(acls) => updateRole(index, { acls })}
                />
              ))}
            </div>
          </div>

          <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Default access</p>
            <p className="mt-1 text-sm text-slate-400">The access the plugin grants when no rule of a role matches.</p>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <Toggle
                label="Publish (client send)"
                ariaLabel="Default publish client send"
                checked={document.default_access.publish_client_send}
                disabled={isApplying || isPreviewing}
                onChange={(checked) => updateDefaultAccess({ publish_client_send: checked })}
              />
              <Toggle
                label="Publish (client receive)"
                ariaLabel="Default publish client receive"
                checked={document.default_access.publish_client_receive}
                disabled={isApplying || isPreviewing}
                onChange={(checked) => updateDefaultAccess({ publish_client_receive: checked })}
              />
              <Toggle
                label="Subscribe"
                ariaLabel="Default subscribe"
                checked={document.default_access.subscribe}
                disabled={isApplying || isPreviewing}
                onChange={(checked) => updateDefaultAccess({ subscribe: checked })}
              />
              <Toggle
                label="Unsubscribe"
                ariaLabel="Default unsubscribe"
                checked={document.default_access.unsubscribe}
                disabled={isApplying || isPreviewing}
                onChange={(checked) => updateDefaultAccess({ unsubscribe: checked })}
              />
            </div>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <Field label="Anonymous group" hint="The group the anonymous clients belong to.">
                <input
                  aria-label="Anonymous group"
                  type="text"
                  disabled={isApplying || isPreviewing}
                  value={document.anonymous_group ?? ''}
                  onChange={(event) => mutate((doc) => ({ ...doc, anonymous_group: event.target.value }))}
                  className="w-full rounded bg-slate-800 px-2 py-1 font-mono text-sm text-slate-100"
                />
              </Field>
            </div>
          </div>

          {preview ? (
            <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Plan</p>
                <span className="rounded-full bg-cyan-400/10 px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-200">
                  {preview.kind}
                </span>
              </div>
              <p className="mt-2 break-all font-mono text-xs text-slate-400">plan {preview.plan_id}</p>

              {preview.plan?.changes?.length ? (
                <ul className="mt-4 space-y-2">
                  {preview.plan.changes.map((change, index) => (
                    <li key={`${change.kind}-${change.name}-${index}`} className="rounded-2xl border border-white/10 bg-slate-950/40 p-3">
                      <p className="text-xs font-semibold uppercase tracking-[0.18em] text-slate-400">
                        {change.kind} · {change.action}
                      </p>
                      <p className="mt-1 text-sm text-slate-200">{change.summary}</p>
                      {change.commands?.length ? (
                        <ul className="mt-2 space-y-1 font-mono text-xs text-slate-400">
                          {change.commands.map((command, commandIndex) => (
                            <li key={`${command.command}-${commandIndex}`}>{command.command}</li>
                          ))}
                        </ul>
                      ) : null}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="mt-4 text-sm text-slate-300">No changes — the security database already matches.</p>
              )}

              {preview.warnings?.length ? (
                <div className="mt-4 rounded-2xl border border-amber-300/30 bg-amber-400/10 p-4">
                  <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Warnings</p>
                  <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-amber-100">
                    {preview.warnings.map((warning, index) => (
                      <li key={`${index}-${warning}`}>{warning}</li>
                    ))}
                  </ul>
                </div>
              ) : null}

              {guard ? <GuardReport guard={guard} /> : null}

              <div className="mt-5 flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  onClick={() => { void handleApply() }}
                  disabled={!applyEnabled}
                  title={!isAdmin ? 'Requires admin role' : undefined}
                  className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
                >
                  {isApplying ? 'Applying…' : 'Apply'}
                </button>
              </div>
            </div>
          ) : null}
        </>
      ) : (
        <>
          <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Adopt the dynamic security backend</p>
            <p className="mt-2 text-sm text-slate-300">
              The dynamic security plugin takes the authentication and the authorization of the broker over. The migration:
            </p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-slate-300">
              <li>the users of the password file become the clients of the plugin, and the rules of the ACL file become the roles of the plugin;</li>
              <li>the plugin is loaded in the broker configuration;</li>
              <li>the password file and the ACL file are dropped from the configuration.</li>
            </ul>
            <p className="mt-3 text-xs text-amber-200">
              The broker is restarted by the migration: Mosquitto loads a plugin at start-up.
            </p>
            <div className="mt-5">
              <button
                type="button"
                onClick={() => { void handleAdopt() }}
                disabled={!isAdmin || isAdopting || isApplying}
                title={!isAdmin ? 'Requires admin role' : undefined}
                className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
              >
                {isAdopting ? 'Preparing…' : 'Adopt the dynamic security backend'}
              </button>
            </div>
          </div>

          {adoption ? (
            <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
              <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Adoption plan</p>
              <p className="mt-2 break-all font-mono text-xs text-slate-400">plan {adoption.plan_id}</p>

              {adoption.credentials?.length ? (
                <div className="mt-4 rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
                  <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">
                    Copy these now, they are shown once
                  </p>
                  <ul className="mt-3 space-y-2">
                    {adoption.credentials.map((credential) => (
                      <li key={credential.username}>
                        <p className="font-mono text-sm text-amber-50">
                          {credential.username} · {credential.password}
                        </p>
                        {credential.reason ? <p className="mt-0.5 text-xs text-amber-200/80">{credential.reason}</p> : null}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}

              {adoption.config_diff ? (
                <pre className="mt-4 max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 font-mono text-xs leading-5">
                  {adoption.config_diff.split('\n').map((line, index) => (
                    <span
                      key={index}
                      className={`block ${line.startsWith('+') ? 'bg-emerald-500/10 text-emerald-200' : line.startsWith('-') ? 'bg-rose-500/10 text-rose-200' : 'text-slate-300'}`}
                    >
                      {line || ' '}
                    </span>
                  ))}
                </pre>
              ) : null}

              {adoption.warnings?.length ? (
                <div className="mt-4 rounded-2xl border border-amber-300/30 bg-amber-400/10 p-4">
                  <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Warnings</p>
                  <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-amber-100">
                    {adoption.warnings.map((warning, index) => (
                      <li key={`${index}-${warning}`}>{warning}</li>
                    ))}
                  </ul>
                </div>
              ) : null}

              <div className="mt-5 flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  onClick={() => { void handleAdoptApply() }}
                  disabled={!isAdmin || isApplying}
                  title={!isAdmin ? 'Requires admin role' : undefined}
                  className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
                >
                  {isApplying ? 'Applying…' : 'Apply'}
                </button>
              </div>
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}

// GuardReport renders the report of the management channel. A violation is
// an error the operator must fix before the change is safe.
function GuardReport({ guard }: { guard: DynSecGuard }) {
  const violations = guard.violations ?? []
  const failed = !guard.ok || violations.length > 0

  return (
    <div
      className={`mt-5 rounded-2xl border p-5 ${failed ? 'border-rose-300/30 bg-rose-400/10' : 'border-emerald-300/30 bg-emerald-400/10'}`}
      role={failed ? 'alert' : undefined}
    >
      <p className={`text-xs font-semibold uppercase tracking-[0.18em] ${failed ? 'text-rose-200' : 'text-emerald-200'}`}>
        Management channel {violations.length > 0 ? 'is open' : failed ? 'is at risk' : 'is closed'}
      </p>
      <p className="mt-2 text-sm text-slate-200">
        Only the management account may reach <span className="font-mono">$CONTROL/dynamic-security/#</span>.
      </p>
      {guard.management_client ? (
        <p className="mt-1 text-xs text-slate-300">
          Management account: <span className="font-mono">{guard.management_client}</span>
        </p>
      ) : null}
      {guard.allowed?.length ? (
        <p className="mt-1 text-xs text-slate-300">
          Allowed: <span className="font-mono">{guard.allowed.join(', ')}</span>
        </p>
      ) : null}
      {violations.length > 0 ? (
        <>
          <p className="mt-3 text-xs font-semibold uppercase tracking-[0.18em] text-rose-200">Must not reach the channel</p>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-rose-100">
            {violations.map((violation) => (
              <li key={violation} className="font-mono text-xs">{violation}</li>
            ))}
          </ul>
        </>
      ) : null}
      {guard.reasons?.length ? (
        <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-slate-200">
          {guard.reasons.map((reason) => (
            <li key={reason}>{reason}</li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}

// ClientCard renders the editable form of one client.
function ClientCard({
  client,
  index,
  roleNames,
  groupNames,
  disabled,
  onChange,
  onRename,
  onRemove,
  onRolesChange,
  onGroupsChange,
}: {
  client: DynSecClient
  index: number
  roleNames: string[]
  groupNames: string[]
  disabled: boolean
  onChange: (changes: Partial<DynSecClient>) => void
  onRename: (username: string) => void
  onRemove: () => void
  onRolesChange: (roles: Array<{ rolename: string; priority: number }>) => void
  onGroupsChange: (groups: Array<{ groupname: string; priority: number }>) => void
}) {
  const label = `client ${index + 1}`

  return (
    <div className="rounded-2xl border border-white/10 bg-slate-950/40 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-sm font-semibold text-cyan-100">{client.username || '(unnamed client)'}</p>
          <p className="mt-1 text-xs text-slate-500">{client.password_set ? 'A password is set.' : 'No password is set.'}</p>
        </div>
        <button
          type="button"
          aria-label={`Remove client ${label}`}
          disabled={disabled}
          onClick={onRemove}
          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-rose-200 transition hover:border-rose-300/40 hover:text-rose-100 disabled:opacity-50"
        >
          Remove
        </button>
      </div>

      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <Field label="Username">
          <input
            aria-label={`Username ${label}`}
            type="text"
            disabled={disabled}
            value={client.username}
            onChange={(event) => onRename(event.target.value)}
            className="w-full rounded bg-slate-800 px-2 py-1 font-mono text-sm text-slate-100"
          />
        </Field>
        <Field label="Text name">
          <input
            aria-label={`Text name ${label}`}
            type="text"
            disabled={disabled}
            value={client.textname ?? ''}
            onChange={(event) => onChange({ textname: event.target.value })}
            className="w-full rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
          />
        </Field>
        <Field label="Description">
          <input
            aria-label={`Description ${label}`}
            type="text"
            disabled={disabled}
            value={client.textdescription ?? ''}
            onChange={(event) => onChange({ textdescription: event.target.value })}
            className="w-full rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
          />
        </Field>
        <Field label="Client id" hint="Pins the MQTT client id of the client.">
          <input
            aria-label={`Client id ${label}`}
            type="text"
            disabled={disabled}
            value={client.clientid ?? ''}
            onChange={(event) => onChange({ clientid: event.target.value })}
            className="w-full rounded bg-slate-800 px-2 py-1 font-mono text-sm text-slate-100"
          />
        </Field>
        <Field
          label="Password"
          hint={client.password_set ? 'A password is stored. Leave the field empty to keep it.' : 'No password is set yet.'}
        >
          <input
            aria-label={`Password ${label}`}
            type="password"
            disabled={disabled}
            placeholder={client.password_set ? 'stored' : 'set a new password'}
            value={client.password ?? ''}
            onChange={(event) => onChange({ password: event.target.value })}
            className="w-full rounded bg-slate-800 px-2 py-1 font-mono text-sm text-slate-100"
          />
        </Field>
        <div className="flex items-end pb-1">
          <Toggle
            label="Disabled"
            ariaLabel={`Disabled ${label}`}
            checked={client.disabled === true}
            disabled={disabled}
            onChange={(checked) => onChange({ disabled: checked })}
          />
        </div>
      </div>

      <RefEditor
        label="Roles"
        ariaPrefix={`Role ${label}`}
        items={client.roles.map((ref) => ({ name: ref.rolename, priority: ref.priority }))}
        options={roleNames}
        disabled={disabled}
        onChange={(items) => onRolesChange(items.map((item) => ({ rolename: item.name, priority: item.priority })))}
      />
      <RefEditor
        label="Groups"
        ariaPrefix={`Group ${label}`}
        items={client.groups.map((ref) => ({ name: ref.groupname, priority: ref.priority }))}
        options={groupNames}
        disabled={disabled}
        onChange={(items) => onGroupsChange(items.map((item) => ({ groupname: item.name, priority: item.priority })))}
      />
    </div>
  )
}

// GroupCard renders the editable form of one group.
function GroupCard({
  group,
  index,
  roleNames,
  clientNames,
  disabled,
  onChange,
  onRename,
  onRemove,
  onRolesChange,
  onClientsChange,
}: {
  group: DynSecGroup
  index: number
  roleNames: string[]
  clientNames: string[]
  disabled: boolean
  onChange: (changes: Partial<DynSecGroup>) => void
  onRename: (groupname: string) => void
  onRemove: () => void
  onRolesChange: (roles: Array<{ rolename: string; priority: number }>) => void
  onClientsChange: (clients: Array<{ username: string; priority: number }>) => void
}) {
  const label = `group ${index + 1}`

  return (
    <div className="rounded-2xl border border-white/10 bg-slate-950/40 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <p className="text-sm font-semibold text-cyan-100">{group.groupname || '(unnamed group)'}</p>
        <button
          type="button"
          aria-label={`Remove group ${label}`}
          disabled={disabled}
          onClick={onRemove}
          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-rose-200 transition hover:border-rose-300/40 hover:text-rose-100 disabled:opacity-50"
        >
          Remove
        </button>
      </div>

      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <Field label="Name">
          <input
            aria-label={`Name ${label}`}
            type="text"
            disabled={disabled}
            value={group.groupname}
            onChange={(event) => onRename(event.target.value)}
            className="w-full rounded bg-slate-800 px-2 py-1 font-mono text-sm text-slate-100"
          />
        </Field>
        <Field label="Text name">
          <input
            aria-label={`Text name ${label}`}
            type="text"
            disabled={disabled}
            value={group.textname ?? ''}
            onChange={(event) => onChange({ textname: event.target.value })}
            className="w-full rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
          />
        </Field>
        <Field label="Description">
          <input
            aria-label={`Description ${label}`}
            type="text"
            disabled={disabled}
            value={group.textdescription ?? ''}
            onChange={(event) => onChange({ textdescription: event.target.value })}
            className="w-full rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
          />
        </Field>
      </div>

      <RefEditor
        label="Roles"
        ariaPrefix={`Role ${label}`}
        items={group.roles.map((ref) => ({ name: ref.rolename, priority: ref.priority }))}
        options={roleNames}
        disabled={disabled}
        onChange={(items) => onRolesChange(items.map((item) => ({ rolename: item.name, priority: item.priority })))}
      />
      <RefEditor
        label="Clients"
        ariaPrefix={`Client ${label}`}
        items={group.clients.map((ref) => ({ name: ref.username, priority: ref.priority }))}
        options={clientNames}
        disabled={disabled}
        onChange={(items) => onClientsChange(items.map((item) => ({ username: item.name, priority: item.priority })))}
      />
    </div>
  )
}

// RoleCard renders the editable form of one role and of its access rules.
function RoleCard({
  role,
  index,
  disabled,
  onChange,
  onRename,
  onRemove,
  onACLsChange,
}: {
  role: DynSecRole
  index: number
  disabled: boolean
  onChange: (changes: Partial<DynSecRole>) => void
  onRename: (rolename: string) => void
  onRemove: () => void
  onACLsChange: (acls: DynSecACL[]) => void
}) {
  const label = `role ${index + 1}`

  return (
    <div className="rounded-2xl border border-white/10 bg-slate-950/40 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <p className="text-sm font-semibold text-cyan-100">{role.rolename || '(unnamed role)'}</p>
        <button
          type="button"
          aria-label={`Remove role ${label}`}
          disabled={disabled}
          onClick={onRemove}
          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-rose-200 transition hover:border-rose-300/40 hover:text-rose-100 disabled:opacity-50"
        >
          Remove
        </button>
      </div>

      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <Field label="Name">
          <input
            aria-label={`Name ${label}`}
            type="text"
            disabled={disabled}
            value={role.rolename}
            onChange={(event) => onRename(event.target.value)}
            className="w-full rounded bg-slate-800 px-2 py-1 font-mono text-sm text-slate-100"
          />
        </Field>
        <Field label="Text name">
          <input
            aria-label={`Text name ${label}`}
            type="text"
            disabled={disabled}
            value={role.textname ?? ''}
            onChange={(event) => onChange({ textname: event.target.value })}
            className="w-full rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
          />
        </Field>
        <Field label="Description">
          <input
            aria-label={`Description ${label}`}
            type="text"
            disabled={disabled}
            value={role.textdescription ?? ''}
            onChange={(event) => onChange({ textdescription: event.target.value })}
            className="w-full rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
          />
        </Field>
      </div>

      <ACLEditor roleName={label} acls={role.acls} disabled={disabled} onChange={onACLsChange} />
    </div>
  )
}

// ACLEditor renders the access control entries of one role.
function ACLEditor({
  roleName,
  acls,
  disabled,
  onChange,
}: {
  roleName: string
  acls: DynSecACL[]
  disabled: boolean
  onChange: (acls: DynSecACL[]) => void
}) {
  return (
    <div className="mt-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs font-semibold uppercase tracking-[0.18em] text-slate-400">Access rules</p>
        <button
          type="button"
          aria-label={`Add rule ${roleName}`}
          disabled={disabled}
          onClick={() => onChange([...acls, { acltype: 'subscribePattern', topic: '', allow: true, priority: 0 }])}
          className="rounded-xl border border-white/10 px-3 py-1 text-xs font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
        >
          Add rule
        </button>
      </div>
      <div className="mt-3 overflow-x-auto">
        <table className="w-full min-w-[42rem] text-left text-sm">
          <thead>
            <tr className="text-xs uppercase tracking-[0.18em] text-slate-500">
              <th className="px-3 py-2">Access</th>
              <th className="px-3 py-2">Topic</th>
              <th className="px-3 py-2">Allow</th>
              <th className="px-3 py-2">Priority</th>
              <th className="px-3 py-2" />
            </tr>
          </thead>
          <tbody>
            {acls.map((acl, index) => (
              <tr key={index} className="border-b border-white/5 last:border-0 align-top">
                <td className="px-3 py-2">
                  <select
                    aria-label={`Access type ${roleName} ${index + 1}`}
                    disabled={disabled}
                    value={acl.acltype}
                    onChange={(event) => onChange(acls.map((entry, i) => (i === index ? { ...entry, acltype: event.target.value as DynSecACL['acltype'] } : entry)))}
                    className="rounded bg-slate-800 px-2 py-1 text-xs text-slate-100"
                  >
                    {dynSecACLTypes.map((type) => (
                      <option key={type} value={type}>{type}</option>
                    ))}
                  </select>
                </td>
                <td className="px-3 py-2">
                  <input
                    aria-label={`Topic ${roleName} ${index + 1}`}
                    type="text"
                    disabled={disabled}
                    placeholder="sensors/#"
                    value={acl.topic}
                    onChange={(event) => onChange(acls.map((entry, i) => (i === index ? { ...entry, topic: event.target.value } : entry)))}
                    className="w-full rounded bg-slate-800 px-2 py-1 font-mono text-xs text-slate-100"
                  />
                </td>
                <td className="px-3 py-2">
                  <select
                    aria-label={`Allow ${roleName} ${index + 1}`}
                    disabled={disabled}
                    value={acl.allow ? 'true' : 'false'}
                    onChange={(event) => onChange(acls.map((entry, i) => (i === index ? { ...entry, allow: event.target.value === 'true' } : entry)))}
                    className="rounded bg-slate-800 px-2 py-1 text-xs text-slate-100"
                  >
                    <option value="true">allow</option>
                    <option value="false">deny</option>
                  </select>
                </td>
                <td className="px-3 py-2">
                  <input
                    aria-label={`Priority ${roleName} ${index + 1}`}
                    type="number"
                    disabled={disabled}
                    value={acl.priority}
                    onChange={(event) => onChange(acls.map((entry, i) => (i === index ? { ...entry, priority: Number(event.target.value) } : entry)))}
                    className="w-24 rounded bg-slate-800 px-2 py-1 font-mono text-xs text-slate-100"
                  />
                </td>
                <td className="px-3 py-2">
                  <button
                    type="button"
                    aria-label={`Remove rule ${roleName} ${index + 1}`}
                    disabled={disabled}
                    onClick={() => onChange(acls.filter((_, i) => i !== index))}
                    className="text-xs text-rose-200 disabled:opacity-50"
                  >
                    Remove
                  </button>
                </td>
              </tr>
            ))}
            {acls.length === 0 ? (
              <tr>
                <td colSpan={5} className="px-3 py-4 text-sm text-slate-400">This role grants nothing.</td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </div>
  )
}

// RefEditor renders the references a client or a group carries.
function RefEditor({
  label,
  ariaPrefix,
  items,
  options,
  disabled,
  onChange,
}: {
  label: string
  ariaPrefix: string
  items: Array<{ name: string; priority: number }>
  options: string[]
  disabled: boolean
  onChange: (items: Array<{ name: string; priority: number }>) => void
}) {
  return (
    <div className="mt-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs font-semibold uppercase tracking-[0.18em] text-slate-400">{label}</p>
        <button
          type="button"
          aria-label={`Add ${label.toLowerCase()} reference`}
          disabled={disabled}
          onClick={() => onChange([...items, { name: options[0] ?? '', priority: 0 }])}
          className="rounded-xl border border-white/10 px-3 py-1 text-xs font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
        >
          Add {label.toLowerCase().replace(/s$/, '')}
        </button>
      </div>
      {items.length === 0 ? <p className="mt-2 text-xs text-slate-500">No {label.toLowerCase()}.</p> : null}
      <div className="mt-2 space-y-2">
        {items.map((item, index) => {
          const names = item.name && !options.includes(item.name) ? [item.name, ...options] : options
          return (
            <div key={index} className="flex flex-wrap items-center gap-2">
              <select
                aria-label={`${ariaPrefix} ${index + 1}`}
                disabled={disabled}
                value={item.name}
                onChange={(event) => onChange(items.map((entry, i) => (i === index ? { ...entry, name: event.target.value } : entry)))}
                className="rounded bg-slate-800 px-2 py-1 text-xs text-slate-100"
              >
                {names.length === 0 ? <option value="">none</option> : null}
                {names.map((name) => (
                  <option key={name} value={name}>{name}</option>
                ))}
              </select>
              <input
                aria-label={`${ariaPrefix} priority ${index + 1}`}
                type="number"
                disabled={disabled}
                value={item.priority}
                onChange={(event) => onChange(items.map((entry, i) => (i === index ? { ...entry, priority: Number(event.target.value) } : entry)))}
                className="w-24 rounded bg-slate-800 px-2 py-1 font-mono text-xs text-slate-100"
              />
              <button
                type="button"
                aria-label={`Remove ${ariaPrefix} ${index + 1}`}
                disabled={disabled}
                onClick={() => onChange(items.filter((_, i) => i !== index))}
                className="text-xs text-rose-200 disabled:opacity-50"
              >
                Remove
              </button>
            </div>
          )
        })}
      </div>
    </div>
  )
}

// Field renders one labelled input with its hint.
function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <label className="block">
      <span className="text-xs font-semibold uppercase tracking-[0.18em] text-slate-400">{label}</span>
      <span className="mt-1 block">{children}</span>
      {hint ? <span className="mt-1 block text-xs text-slate-500">{hint}</span> : null}
    </label>
  )
}

// Toggle renders one checkbox.
function Toggle({
  label,
  ariaLabel,
  checked,
  disabled,
  onChange,
}: {
  label: string
  ariaLabel: string
  checked: boolean
  disabled: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="flex items-center gap-2 text-xs text-slate-300">
      <input
        type="checkbox"
        aria-label={ariaLabel}
        disabled={disabled}
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
      />
      {label}
    </label>
  )
}

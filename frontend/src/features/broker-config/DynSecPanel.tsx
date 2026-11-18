import { useState } from 'react'
import { can } from '../auth/permissions'
import { useDynSec } from './useDynSec'
import { useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
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
import { deploymentLamp, lampPanel } from '../status/deploymentLamp'
import { DiffBlock, Plate, Section } from '../ui/primitives'
import { bodyText, fieldHint, fieldLabel, input, leverDanger, leverPrimary, leverSecondary, notice, select, tableHead, tableRow } from '../ui/styles'
import { DraftState, Findings } from './panelParts'

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
  const mfa = useRecentMFA(token, onLogout)
  const { state, preview, adoption, guard, previewError, applyError, issues, isLoading, isPreviewing, isApplying, isAdopting, applied, requestPreview, apply, adopt } =
    useDynSec({ token, onLogout, onMFARejected: mfa.clear })
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
  const applyEnabled = isAdmin && Boolean(mfa.proof) && !isApplying && isPreviewCurrent && preview?.has_changes !== false

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
    const result = await apply({ plan_id: preview.plan_id, document: previewedDocument ?? document }, mfa.proof)
    if (result) {
      setDraft(undefined)
      setPreviewedDocument(null)
    }
  }

  const handleAdopt = async () => {
    await adopt(mfa.proof)
  }

  const handleAdoptApply = async () => {
    if (!adoption?.plan_id || !isAdmin || !mfa.proof || isApplying) return
    await apply({ plan_id: adoption.plan_id }, mfa.proof)
  }

  if (isLoading && !state) {
    return (
      <section className="mt-8">
        <p className={bodyText}>Loading the dynamic security state…</p>
      </section>
    )
  }

  if (!state) {
    return (
      <section className="mt-8">
        <div className={notice.neutral}>
          Dynamic security management is not configured on this MCM instance.
        </div>
      </section>
    )
  }

  const adopted = state.backend === 'dynamic-security'

  return (
    <section className="mt-8 space-y-10">
      <Section
        title="Dynamic security"
        description="The clients, groups and roles the broker enforces, and the report of the management channel."
        aside={
          <div className="flex flex-wrap items-center gap-2">
            <Plate tone={dynSecBackendTones[state.backend] ?? dynSecBackendTones.none}>
              {dynSecBackendLabels[state.backend] ?? state.backend}
            </Plate>
            {adopted ? (
              <button
                type="button"
                onClick={handlePreview}
                disabled={!canPreview || isLoading || isPreviewing || isApplying}
                title={!canPreview ? 'Requires operator role' : undefined}
                className={leverSecondary}
              >
                {isPreviewing ? 'Generating…' : 'Preview'}
              </button>
            ) : null}
          </div>
        }
      >
        {state.management_client ? (
          <p className={`mt-1 ${fieldHint}`}>
            Management account: <span className="font-mono">{state.management_client}</span>
          </p>
        ) : null}
        <DraftState dirty={dirty} />

        {previewError ? (
          <div className={`mt-5 ${notice.failed}`} role="alert">
            {previewError}
          </div>
        ) : null}
        {applyError ? (
          <div className={`mt-5 ${notice.failed}`} role="alert">
            {applyError}
          </div>
        ) : null}
        {applied ? (
          // The apply reports no activation status, so the lamp stays amber: never verified green.
          <div className={`mt-5 px-4 py-3 text-sm ${lampPanel[deploymentLamp(undefined)]}`} role="status">
            The security database was updated and the broker enforces it.
          </div>
        ) : null}
        {state.warnings?.length ? (
          <Findings tone="caution" title="Warnings">
            {state.warnings.map((warning, index) => (
              <li key={`${index}-${warning}`}>{warning}</li>
            ))}
          </Findings>
        ) : null}
        {issues.length > 0 ? (
          <Findings tone="failed" title="Issues">
            {issues.map((issue, index) => (
              <li key={`${issue.kind}-${issue.object}-${index}`}>
                {issue.object ? <span className="font-mono text-xs">{issue.object}</span> : null}
                {issue.object ? ' · ' : null}
                {issue.message}
              </li>
            ))}
          </Findings>
        ) : null}
        {guard && !preview ? <GuardReport guard={guard} /> : null}
      </Section>

      {adopted ? (
        <>
          <Section
            title="Clients"
            description="The clients the plugin authenticates."
            aside={<button type="button" onClick={addClient} disabled={isApplying || isPreviewing} className={leverSecondary}>Add client</button>}
          >
            <div className="mt-4">
              {document.clients.length === 0 ? (
                <p className={bodyText}>No client is configured.</p>
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
          </Section>

          <Section
            title="Groups"
            description="The groups of clients that share the same roles."
            aside={<button type="button" onClick={addGroup} disabled={isApplying || isPreviewing} className={leverSecondary}>Add group</button>}
          >
            <div className="mt-4">
              {document.groups.length === 0 ? (
                <p className={bodyText}>No group is configured.</p>
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
          </Section>

          <Section
            title="Roles"
            description="The roles that carry the topic permissions."
            aside={<button type="button" onClick={addRole} disabled={isApplying || isPreviewing} className={leverSecondary}>Add role</button>}
          >
            <div className="mt-4">
              {document.roles.length === 0 ? (
                <p className={bodyText}>No role is configured.</p>
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
          </Section>

          <Section title="Default access" description="The access the plugin grants when no rule of a role matches.">
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
                  className={`w-full font-mono ${input}`}
                />
              </Field>
            </div>
          </Section>

          {preview ? (
            <Section title="Plan" aside={<Plate>{preview.kind}</Plate>}>
              <p className="mt-2 break-all font-mono text-xs text-enamel-muted">plan {preview.plan_id}</p>

              {preview.plan?.changes?.length ? (
                <ul className="mt-4 border-y border-track">
                  {preview.plan.changes.map((change, index) => (
                    <li key={`${change.kind}-${change.name}-${index}`} className="border-b border-track py-3 last:border-0">
                      <p className="font-mono text-xs text-enamel-muted">
                        {change.kind} · {change.action}
                      </p>
                      <p className="mt-1 text-sm text-enamel">{change.summary}</p>
                      {change.commands?.length ? (
                        <ul className="mt-2 space-y-1 font-mono text-xs text-enamel-soft">
                          {change.commands.map((command, commandIndex) => (
                            <li key={`${command.command}-${commandIndex}`}>{command.command}</li>
                          ))}
                        </ul>
                      ) : null}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className={`mt-4 ${bodyText}`}>No changes — the security database already matches.</p>
              )}

              {preview.warnings?.length ? (
                <Findings tone="caution" title="Warnings" className="mt-4">
                  {preview.warnings.map((warning, index) => (
                    <li key={`${index}-${warning}`}>{warning}</li>
                  ))}
                </Findings>
              ) : null}

              {guard ? <GuardReport guard={guard} /> : null}

              {isAdmin ? <div className="mt-5"><RecentMFAConfirmation state={mfa} /></div> : null}

              <div className="mt-5 flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  onClick={() => { void handleApply() }}
                  disabled={!applyEnabled}
                  title={!isAdmin ? 'Requires admin role' : undefined}
                  className={leverPrimary}
                >
                  {isApplying ? 'Applying…' : 'Apply'}
                </button>
                {isAdmin && !mfa.proof ? <p className="text-xs text-lamp">Confirm MFA above before applying.</p> : null}
              </div>
            </Section>
          ) : null}
        </>
      ) : (
        <>
          <Section
            title="Adopt the dynamic security backend"
            description="The dynamic security plugin takes the authentication and the authorization of the broker over. The migration:"
          >
            <ul className={`mt-3 list-disc space-y-1 pl-5 ${bodyText}`}>
              <li>the users of the password file become the clients of the plugin, and the rules of the ACL file become the roles of the plugin;</li>
              <li>the plugin is loaded in the broker configuration;</li>
              <li>the password file and the ACL file are dropped from the configuration.</li>
            </ul>
            <p className="mt-3 text-xs text-lamp">
              The broker is restarted by the migration: Mosquitto loads a plugin at start-up.
            </p>
            {isAdmin ? <div className="mt-5"><RecentMFAConfirmation state={mfa} /></div> : null}
            <div className="mt-5 flex flex-wrap items-center gap-3">
              <button
                type="button"
                onClick={() => { void handleAdopt() }}
                disabled={!isAdmin || !mfa.proof || isAdopting || isApplying}
                title={!isAdmin ? 'Requires admin role' : undefined}
                className={leverPrimary}
              >
                {isAdopting ? 'Preparing…' : 'Adopt the dynamic security backend'}
              </button>
              {isAdmin && !mfa.proof ? <p className="text-xs text-lamp">Confirm MFA above before adopting.</p> : null}
            </div>
          </Section>

          {adoption ? (
            <Section title="Adoption plan">
              <p className="mt-2 break-all font-mono text-xs text-enamel-muted">plan {adoption.plan_id}</p>

              {adoption.credentials?.length ? (
                <div className={`mt-4 ${notice.caution}`}>
                  <h4 className="font-semibold text-lamp">
                    Copy these now, they are shown once
                  </h4>
                  <ul className="mt-3 space-y-2">
                    {adoption.credentials.map((credential) => (
                      <li key={credential.username}>
                        <p className="font-mono text-sm text-enamel-bright">
                          {credential.username} · {credential.password}
                        </p>
                        {credential.reason ? <p className={`mt-0.5 ${fieldHint}`}>{credential.reason}</p> : null}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}

              {adoption.config_diff ? (
                <div className="mt-4"><DiffBlock content={adoption.config_diff} /></div>
              ) : null}

              {adoption.warnings?.length ? (
                <Findings tone="caution" title="Warnings" className="mt-4">
                  {adoption.warnings.map((warning, index) => (
                    <li key={`${index}-${warning}`}>{warning}</li>
                  ))}
                </Findings>
              ) : null}

              <div className="mt-5 flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  onClick={() => { void handleAdoptApply() }}
                  disabled={!isAdmin || !mfa.proof || isApplying}
                  title={!isAdmin ? 'Requires admin role' : undefined}
                  className={leverPrimary}
                >
                  {isApplying ? 'Applying…' : 'Apply'}
                </button>
                {isAdmin && !mfa.proof ? <p className="text-xs text-lamp">Confirm MFA above before applying.</p> : null}
              </div>
            </Section>
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
    <div className={`mt-5 ${failed ? notice.failed : notice.neutral}`} role={failed ? 'alert' : undefined}>
      <h4 className={`font-semibold ${failed ? '' : 'text-enamel-bright'}`}>
        Management channel {violations.length > 0 ? 'is open' : failed ? 'is at risk' : 'is closed'}
      </h4>
      <p className="mt-2">
        Only the management account may reach <span className="font-mono">$CONTROL/dynamic-security/#</span>.
      </p>
      {guard.management_client ? (
        <p className="mt-1 text-xs">
          Management account: <span className="font-mono">{guard.management_client}</span>
        </p>
      ) : null}
      {guard.allowed?.length ? (
        <p className="mt-1 text-xs">
          Allowed: <span className="font-mono">{guard.allowed.join(', ')}</span>
        </p>
      ) : null}
      {violations.length > 0 ? (
        <>
          <h5 className="mt-3 text-xs font-semibold">Must not reach the channel</h5>
          <ul className="mt-2 list-disc space-y-1 pl-5">
            {violations.map((violation) => (
              <li key={violation} className="font-mono text-xs">{violation}</li>
            ))}
          </ul>
        </>
      ) : null}
      {guard.reasons?.length ? (
        <ul className="mt-3 list-disc space-y-1 pl-5">
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
    <div className="border-t border-track py-4 first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h4 className="text-sm font-semibold text-enamel-bright">{client.username || '(unnamed client)'}</h4>
          <p className={`mt-1 ${fieldHint}`}>{client.password_set ? 'A password is set.' : 'No password is set.'}</p>
        </div>
        <button
          type="button"
          aria-label={`Remove client ${label}`}
          disabled={disabled}
          onClick={onRemove}
          className={leverDanger}
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
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Text name">
          <input
            aria-label={`Text name ${label}`}
            type="text"
            disabled={disabled}
            value={client.textname ?? ''}
            onChange={(event) => onChange({ textname: event.target.value })}
            className={`w-full ${input}`}
          />
        </Field>
        <Field label="Description">
          <input
            aria-label={`Description ${label}`}
            type="text"
            disabled={disabled}
            value={client.textdescription ?? ''}
            onChange={(event) => onChange({ textdescription: event.target.value })}
            className={`w-full ${input}`}
          />
        </Field>
        <Field label="Client id" hint="Pins the MQTT client id of the client.">
          <input
            aria-label={`Client id ${label}`}
            type="text"
            disabled={disabled}
            value={client.clientid ?? ''}
            onChange={(event) => onChange({ clientid: event.target.value })}
            className={`w-full font-mono ${input}`}
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
            className={`w-full font-mono ${input}`}
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
    <div className="border-t border-track py-4 first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <h4 className="text-sm font-semibold text-enamel-bright">{group.groupname || '(unnamed group)'}</h4>
        <button
          type="button"
          aria-label={`Remove group ${label}`}
          disabled={disabled}
          onClick={onRemove}
          className={leverDanger}
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
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Text name">
          <input
            aria-label={`Text name ${label}`}
            type="text"
            disabled={disabled}
            value={group.textname ?? ''}
            onChange={(event) => onChange({ textname: event.target.value })}
            className={`w-full ${input}`}
          />
        </Field>
        <Field label="Description">
          <input
            aria-label={`Description ${label}`}
            type="text"
            disabled={disabled}
            value={group.textdescription ?? ''}
            onChange={(event) => onChange({ textdescription: event.target.value })}
            className={`w-full ${input}`}
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
    <div className="border-t border-track py-4 first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <h4 className="text-sm font-semibold text-enamel-bright">{role.rolename || '(unnamed role)'}</h4>
        <button
          type="button"
          aria-label={`Remove role ${label}`}
          disabled={disabled}
          onClick={onRemove}
          className={leverDanger}
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
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Text name">
          <input
            aria-label={`Text name ${label}`}
            type="text"
            disabled={disabled}
            value={role.textname ?? ''}
            onChange={(event) => onChange({ textname: event.target.value })}
            className={`w-full ${input}`}
          />
        </Field>
        <Field label="Description">
          <input
            aria-label={`Description ${label}`}
            type="text"
            disabled={disabled}
            value={role.textdescription ?? ''}
            onChange={(event) => onChange({ textdescription: event.target.value })}
            className={`w-full ${input}`}
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
        <h5 className="text-xs font-semibold text-enamel">Access rules</h5>
        <button
          type="button"
          aria-label={`Add rule ${roleName}`}
          disabled={disabled}
          onClick={() => onChange([...acls, { acltype: 'subscribePattern', topic: '', allow: true, priority: 0 }])}
          className={leverSecondary}
        >
          Add rule
        </button>
      </div>
      <div className="mt-3 table-scroll">
        <table className="w-full min-w-[42rem] text-left text-sm">
          <thead>
            <tr className="border-b border-track-strong">
              <th className={`${tableHead} text-left`}>Access</th>
              <th className={`${tableHead} text-left`}>Topic</th>
              <th className={`${tableHead} text-left`}>Allow</th>
              <th className={`${tableHead} text-left`}>Priority</th>
              <th className={tableHead} />
            </tr>
          </thead>
          <tbody>
            {acls.map((acl, index) => (
              <tr key={index} className={`${tableRow} align-top`}>
                <td className="py-2 pr-3">
                  <select
                    aria-label={`Access type ${roleName} ${index + 1}`}
                    disabled={disabled}
                    value={acl.acltype}
                    onChange={(event) => onChange(acls.map((entry, i) => (i === index ? { ...entry, acltype: event.target.value as DynSecACL['acltype'] } : entry)))}
                    className={select}
                  >
                    {dynSecACLTypes.map((type) => (
                      <option key={type} value={type}>{type}</option>
                    ))}
                  </select>
                </td>
                <td className="py-2 pr-3">
                  <input
                    aria-label={`Topic ${roleName} ${index + 1}`}
                    type="text"
                    disabled={disabled}
                    placeholder="sensors/#"
                    value={acl.topic}
                    onChange={(event) => onChange(acls.map((entry, i) => (i === index ? { ...entry, topic: event.target.value } : entry)))}
                    className={`w-full font-mono ${input}`}
                  />
                </td>
                <td className="py-2 pr-3">
                  <select
                    aria-label={`Allow ${roleName} ${index + 1}`}
                    disabled={disabled}
                    value={acl.allow ? 'true' : 'false'}
                    onChange={(event) => onChange(acls.map((entry, i) => (i === index ? { ...entry, allow: event.target.value === 'true' } : entry)))}
                    className={select}
                  >
                    <option value="true">allow</option>
                    <option value="false">deny</option>
                  </select>
                </td>
                <td className="py-2 pr-3">
                  <input
                    aria-label={`Priority ${roleName} ${index + 1}`}
                    type="number"
                    disabled={disabled}
                    value={acl.priority}
                    onChange={(event) => onChange(acls.map((entry, i) => (i === index ? { ...entry, priority: Number(event.target.value) } : entry)))}
                    className={`w-24 font-mono ${input}`}
                  />
                </td>
                <td className="py-2 pr-3">
                  <button
                    type="button"
                    aria-label={`Remove rule ${roleName} ${index + 1}`}
                    disabled={disabled}
                    onClick={() => onChange(acls.filter((_, i) => i !== index))}
                    className={leverDanger}
                  >
                    Remove
                  </button>
                </td>
              </tr>
            ))}
            {acls.length === 0 ? (
              <tr>
                <td colSpan={5} className={`py-4 ${bodyText}`}>This role grants nothing.</td>
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
        <h5 className="text-xs font-semibold text-enamel">{label}</h5>
        <button
          type="button"
          aria-label={`Add ${label.toLowerCase()} reference`}
          disabled={disabled}
          onClick={() => onChange([...items, { name: options[0] ?? '', priority: 0 }])}
          className={leverSecondary}
        >
          Add {label.toLowerCase().replace(/s$/, '')}
        </button>
      </div>
      {items.length === 0 ? <p className={`mt-2 ${fieldHint}`}>No {label.toLowerCase()}.</p> : null}
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
                className={select}
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
                className={`w-24 font-mono ${input}`}
              />
              <button
                type="button"
                aria-label={`Remove ${ariaPrefix} ${index + 1}`}
                disabled={disabled}
                onClick={() => onChange(items.filter((_, i) => i !== index))}
                className={leverDanger}
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
      <span className={fieldLabel}>{label}</span>
      <span className="mt-2 block">{children}</span>
      {hint ? <span className={`mt-1 block ${fieldHint}`}>{hint}</span> : null}
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
    <label className="flex items-center gap-2 text-sm text-enamel">
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

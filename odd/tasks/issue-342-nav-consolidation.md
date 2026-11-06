# Issue #342 — collapsible sidebar groups in the HMI

Reduce the density of the HMI left sidebar without hiding capabilities and
without breaking any URL or test. Driven by the operator feedback that the
menu shows "muchas opciones" (see the issue), and by the audited inventory in
this document.

## Audit (facts, not opinions)

Inventory taken from `frontend/src/App.tsx:118-304` (19 entries, 5 groups) and
the sidebar renderer `frontend/src/features/dashboard/DashboardFrame.tsx:47-95`.

| Group | Entries | Visible to viewer | Visible to auditor/operator | Visible to admin |
|---|---|---|---|---|
| Configuration | Configuration, Broker settings, Bridges, Plugins | 0 | 4 | 4 |
| MQTT Access | Users, ACLs, Listeners, Certificates, Dynamic security | 2 | 5 | 5 |
| Changes | Deploy, Configuration revisions | 1 | 2 | 2 |
| Diagnostics | Dashboard, Topics, Broker diagnostics, Logs | 3 | 4 | 4 |
| Administration | Security, Audit, Account, Operators | 4 | 4 | 4 |
| **Total** | **19** | **10** | **19** | **19** |

Role filtering is `minRole` + `ROLE_RANK` (`frontend/src/App.tsx:340-346`,
`frontend/src/features/auth/permissions.ts:4-11`).

Classification of every entry (required by the issue):

- **Core operation (changes broker or MCM state):** Configuration, Broker
  settings, Bridges, Plugins, Users, ACLs, Listeners, Certificates, Dynamic
  security, Deploy, Configuration revisions, Account, Operators.
- **Observation (read-only, never enforces):** Dashboard, Topics, Broker
  diagnostics, Logs.
- **Optional message diagnostics inside Topics** (inspection only, never
  filters or rejects broker traffic — issue #307): Sparkplug B and JSON Schema
  results.
- **Deferred, and therefore absent from the navigation:** multi-broker
  administration, fleets, SSO, high availability, PostgreSQL, complete
  backup/restore (#295), an explicit broker version support matrix. No entry
  may imply any of them.

Consolidation candidates assessed and rejected on evidence: Topics is not
reachable from Broker diagnostics (payload, schema and Sparkplug inspection is
absent there); Logs keeps the live MCM event feed that Broker diagnostics does
not render (it shows counters and broker-log entries only); Dashboard shows
recent activity that diagnostics does not. Configuration and Configuration
revisions already share one panel through the `view` prop
(`frontend/src/features/broker-config/BrokerConfigPanel.tsx:10-18`), so the
existing panel boundary is not the source of the density. The density comes
from every group being expanded at once.

## Specs

- S1 (user decision, verbatim): "Solo colapsar grupos: 19 entradas" — the
  navigation keeps all 19 entries and all 5 groups; only the presentation
  changes.
- S2 (user decision, verbatim): "Preservar rutas heredadas" — every current
  URL (`/broker-config`, `/broker-settings`, `/broker-bridges`,
  `/broker-plugins`, `/users`, `/acls`, `/listeners`, `/certificates`,
  `/broker-dynsec`, `/deploy`, `/changes`, `/dashboard`, `/topics`,
  `/broker-diagnostics`, `/logs`, `/security`, `/audit`, `/account`,
  `/admin-users`) keeps resolving to the same view, and no entry is removed
  from the navigation.
- S3: The **Configuration** group is expanded by default; the other four
  groups (MQTT Access, Changes, Diagnostics, Administration) start collapsed.
- S4: The group that contains the active item is always expanded, including
  on a direct load of a URL inside a collapsed group (for example `/logs`
  expands Diagnostics and shows Logs active).
- S5: Collapsing a group hides its entries, not its heading: the group
  headings stay visible and remain the navigation landmarks, so the
  information architecture is still readable at a glance.
- S6: The collapse control is a real button with `aria-expanded` and
  `aria-controls`, the group is a labelled region, and the toggle is
  keyboard operable. Collapsed groups do not hide the active item from
  assistive technology.
- S7: The observation group is visually distinguished from the core operation
  group, and the optional message diagnostics inside Topics keep the framing
  of issue #307 (inspection, never enforcement). The distinction must be
  visible in the sidebar, not only in a panel.
- S8: No navigation entry may announce a deferred or unimplemented capability
  as available. Every entry's label, eyebrow, title and description are
  audited against what the API and the panel actually do; any overclaim is
  corrected with cited evidence, and no capability is removed.
- S9: The existing unit tests and the e2e journey keep passing. Tests that
  depend on the sidebar are updated only where the presentation genuinely
  changed, and new tests cover the collapse behavior, the active-group rule
  and the accessibility contract.
- S10: Role filtering is unchanged: a viewer still sees exactly the entries
  the `minRole` rules allow, and a group with no visible entries still
  disappears.

## Tasks

| ID | Specs | Route | Commit |
|----|-------|-------|--------|
| T1 | S1, S3, S5, S6 | gpt-luna-6 (frontend/src/**) | — |
| T2 | S4, S10 | gpt-luna-6 (frontend/src/**) | — |
| T3 | S7, S8 | gpt-luna-6 (frontend/src/**) | — |
| T4 | S9 | gpt-luna-6 (frontend/src/**, frontend/e2e/**) | — |
| T5 | verify: unit tests, lint, e2e, rebuild image, visual check | parent | — |
| T6 | update #342 with the evidence + PR | parent (user decides) | — |

## Log

- L1: "Mergea todas las PRs y vamos a por la #342" (verbatim).
- L2: Decisions taken in the interview: consolidation level = "Solo colapsar
  grupos: 19 entradas"; legacy URLs = "Preservar rutas heredadas". The
  workspaces and aggressive options were offered and declined in favour of
  the conservative one, which keeps every capability one click away.
- L3: The audit corrected two assumptions carried by the original issue: the
  optional diagnostics (Sparkplug, JSON Schema) are not navigation entries but
  part of the Topics panel, and the real redundancy candidate (Configuration
  vs Configuration revisions) is already a single panel with a `view` prop.
  The audit therefore targets the expanded-by-default presentation, not the
  entry list.

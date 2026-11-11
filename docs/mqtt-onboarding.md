# The MQTT onboarding journey

MCM admits an MQTT user in one journey: the user, its access rule, the
preview and the apply, without leaving the **Users** panel. The journey
exists because the alta used to be a multi-window errand: create the user
in *Users*, create the rule in *ACLs*, then go to *Changes*, preview,
apply and confirm MFA. The intermediate "saved but not applied" state is
easy to mistake for a completed alta, and the new client fails with
`not authorised` until someone discovers the missing apply.

The journey adds no guarantee of its own. It chains the endpoints MCM
already exposes and keeps every gate the backend owns.

## The steps

| Step | What it does | What it writes |
| --- | --- | --- |
| 1. User | `POST /api/v1/mqtt-users` with the username. The generated password is shown once. | The MQTT user. |
| 2. Access rule | `POST /api/v1/acls` with the topic filter, the permission and the ACL file scope. The principal is the user of step 1. | The ACL rule. |
| 3. Preview | `POST /api/v1/deployments/preview`. | Nothing. It renders the ACL and password file diffs. |
| 4. Apply | `POST /api/v1/deployments/apply` with the preview revision. | The broker files, then the reload and the activation verification. |

The journey opens from **Add User** in the *Users* panel. **Complete
setup** in a user row opens it at step 2 for a user that already exists,
so an identity created outside the journey can still get its rule and its
apply in the same place.

The rule of the journey is an ordinary MCM rule: the *ACLs* panel lists
it, `GET /api/v1/acls/effective` resolves it, and the apply renders it
into the Mosquitto ACL file.

## The gates the journey does not touch

- **Role.** Creating the user and the rule needs `operator`. The apply
  needs `admin`, and it is refused for anyone else before a byte is
  written.
- **Recent MFA.** The apply step demands a proof from
  `POST /api/v1/auth/mfa/step-up`. The button stays disabled until the
  proof exists, the proof is sent in the `X-MCM-MFA` header, and it is
  kept in browser memory only. An account without an enrollment is
  refused; enable it under **Administration → Account**.
- **Activation verification.** The apply still verifies the activation
  against the live broker and rolls back from the snapshot when the
  verification fails. The journey reports that outcome; it never decides
  it. See [the activation probe](./acl.md#activation-probe).

## What the operator reads at the end

The journey names the stage that failed and shows the deployment message
verbatim, so the cause and the concrete exit of
[issue #339](./acl.md#activation-probe-after-a-restart-of-mcm) reach the
operator instead of an opaque failure.

| Outcome | What the journey shows |
| --- | --- |
| `active_verified` | **Applied and verified**, with the deployment message. |
| `pending_activation` | **Applied — activation pending**: the configuration is on disk, the activation is not proven yet. |
| `rolled_back` | **Rolled back**, the failed stage (activation verification) and the deployment message. MCM restored the previous configuration. |
| `rollback_failed` | **Rollback failed**: the previous configuration was not restored and the operator has to intervene. |
| a refused request | **Apply step failed** with the reason of the refusal, for example a preview revision that no longer matches the on-disk configuration. |

A failure in an earlier step names its own stage: *User creation
failed*, *Access rule step failed*, *Preview step failed*.

## The pending deploy banner

The banner that says **Pending deploy** still covers every change that
is saved but not applied to Mosquitto, whoever made it: a rule edited in
the *ACLs* panel, a user toggled in the *Users* panel, a change made
outside the journey. The journey does not replace it:

- the user of step 1 and the rule of step 2 raise the banner, because
  they are saved and not applied yet;
- a successful apply inside the journey clears it, because nothing of
  that journey is left waiting;
- a rolled back apply keeps it raised: the change is still saved, and the
  broker is still serving the previous configuration.

The banner is cleared only when no other change was saved in the
meantime. A change made elsewhere while the journey was open stays
pending and keeps the banner visible.

## Limits

- The journey applies the whole pending deployment, not only the user and
  the rule it created. A change saved elsewhere before the preview is
  applied by the same apply.
- One rule per journey. A user that needs several topic filters gets one
  rule here and the rest in the *ACLs* panel.
- The verification proves that the broker serves the applied
  configuration. It does not prove the semantics of every directive; see
  [what the activation probe does not prove](./acl.md#what-the-activation-probe-does-not-prove).
- A `readwrite` grant on the new identity is what lets the activation
  verification prove that identity. A narrower grant is applied too, but
  the activation is then verified through the labelled managed service
  account, and the deployment message says so.

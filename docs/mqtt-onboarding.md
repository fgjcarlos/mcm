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
| 5. Test connection | Shows the connection data and a manual test for the operator's own MQTT client, then watches the broker. Offered only after a deployment that left the configuration on the broker. | Nothing. |

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

## The connection test

An applied rule proves what the broker serves, not that the
application can use it. The last step closes that gap with the client the
application really uses: any MQTT 3.1.1 or MQTT 5 client works, and
Node-RED is one optional example (see
[the Node-RED guide](./integrations/node-red.md)).

The step shows:

- the **client host**, editable: MCM has no public address of the
  broker, so it suggests the host the browser uses to reach MCM;
- the **listener**, picked from `GET /api/v1/listeners` (the listener of
  the rule when the rule is scoped to one), or a listener MCM does not
  manage, with its port and TLS mode entered by hand;
- the **port**, the **TLS** mode (plain, TLS, or TLS with a client
  certificate) and the transport when the listener speaks WebSockets;
- the **username** and the **allowed operation** (`<permission> on
  <topic filter>`);
- a warning when the listener admits anonymous clients, because a
  connection there does not prove the credentials.

The one-time password is not shown again. It can be copied from its
banner right after the user is created (**Copy password**); MCM does not
keep it and has no way to retrieve it later.

The test uses a concrete topic inside the filter (every `+` or `#` level
becomes `mcm-test`) and a unique payload (`mcm-test-<nonce>`):

| Permission | Allowed operation | Operation that must stay denied |
| --- | --- | --- |
| `readwrite` | Subscribe to the test topic and publish the payload to it. | Publish `mcm-denied-<nonce>` to `mcm-denied-<nonce>/probe`, a topic outside the filter. None when the filter is `#`. |
| `write` | Publish the payload to the test topic. | Subscribe to the test topic. |
| `read` | Subscribe to the test topic; another account publishes to it. | Publish `mcm-denied-<nonce>` to the test topic. |
| `deny` | None. | Publish `mcm-denied-<nonce>` to the test topic. |

### What MCM observes

MCM never runs the test and never takes the activation probe of the
deployment for the application's client. It opens the broker event
stream (`/api/v1/broker/events`) when the step opens and keeps four
stages apart:

| Stage | Observed when | Not observable when |
| --- | --- | --- |
| Applied | The deployment reported `active_verified`, `applied` or `pending_activation`. | — |
| Connection | A broker log line `New client connected … u'<username>'` names the new account. The deployment's own probe clients (`mcm-verify-*`) are ignored. | MCM does not ingest the broker log (`MCM_MOSQUITTO_LOG_PATH`), the role is below auditor, or the event stream is closed. |
| Operation | The MCM monitor, which reads every topic, sees the unique payload on the test topic. For `readwrite` this proves the publish only; the subscription half stays with the operator's client. | The operation is a subscription, the monitor is not subscribed to `#` (or reports no subscriptions), or the event stream is closed. |
| Denied operation | The denied payload reaches the broker: the step flags it, because another rule grants it to the account. Its absence is reported as consistent with the denial; MCM cannot see the refusal itself. | The denied operation is a subscription: the broker reports the refusal to the client only. |

A stage MCM cannot observe says why, and offers **Confirm manually**. A
confirmed stage reads *Confirmed by you — not observed by MCM*.

The broker log entry is read every 5 seconds and is not replayed to a
late subscriber, so run the test once the step says *Watching*.

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

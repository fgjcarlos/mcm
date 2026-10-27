# Plugins and Dynamic Security API

MCM manages the Mosquitto plugins of the managed broker and the security
database of the dynamic security plugin. This page covers the plugin
catalog, the model of the security database, the management channel, the
separation of the two access-control backends and the review-and-apply
flow.

## Two different things

The plugin surface and the Dynamic Security surface touch the broker in
two different ways, and MCM keeps them apart.

| | Plugin surface | Dynamic Security surface |
|---|---|---|
| What it manages | The `plugin` and `plugin_opt_*` directives of `mosquitto.conf` | The clients, groups, roles and access entries of the plugin |
| Where the state lives | The managed configuration file | The JSON database the plugin owns, reached through its control channel |
| How it is read | From the file on disk | From the broker itself: the plugin is the authority on what it enforces |
| When it takes effect | After a broker restart: Mosquitto loads plugins at start-up and never re-reads the directive on SIGHUP | At once: the plugin applies a change as it receives it |

## Plugin catalog

The catalog lists the plugins MCM is able to configure for a broker
version. It lives in `internal/mosquitto/plugins/data/<version>.yaml` and
is embedded in the binary, so the catalog of a version is the same
everywhere.

| Field | Meaning |
|-------|---------|
| `id` | Identifier of the plugin inside MCM. |
| `title` | Name the UI shows. |
| `description` | What the plugin does. |
| `module` | Default path of the shared object the broker loads. |
| `modules` | Paths the plugin is known to live at. The directory depends on the distribution, so the file name is what identifies the plugin. |
| `since_version` | Earliest broker version that ships the plugin. |
| `reload_kind` | Always `restart`: a plugin is loaded at start-up. |
| `options` | The options MCM writes — see below. |

An option carries a `name` (without the `plugin_opt_` prefix), a
`description`, a `required` flag, a `default` and a `placeholder` flag
that tells the UI the value is a path.

### What MCM preserves

MCM never drops a directive it does not understand.

- A **plugin** the catalog does not know stays in the file. It is not
  editable and it is not removed, not even when the operator saves the
  plugin document without it.
- A **`plugin_opt_*` option** the catalog does not manage stays next to
  the options MCM does write, with its value untouched.

Only the plugins of the catalog and the options MCM manages are part of
the desired state the operator edits.

### Rendered Mosquitto syntax

```conf
plugin /usr/lib/mosquitto_dynamic_security.so
plugin_opt_config_file /mosquitto/config/dynamic-security.json
```

The `plugin_opt_*` options belong to the `plugin` directive that
precedes them, which is how Mosquitto reads them.

## Dynamic Security model

The plugin owns the clients, the groups, the roles and the access
entries of the broker, and keeps them in a JSON database of its own. The
database holds a hash of every password, never a cleartext one.

### Clients

| Field | Meaning |
|-------|---------|
| `username` | Name of the client. |
| `textname` | Human readable name. |
| `textdescription` | Description of the client. |
| `clientid` | Pins the MQTT client id of the client. |
| `password` | Password of the client. **Write-only.** |
| `password_set` | Reports whether a password is configured. The server never returns the password itself. |
| `roles` | The roles granted to the client directly. |
| `groups` | The groups the client belongs to. |
| `disabled` | The plugin refuses the client while it is set. |

### Groups

| Field | Meaning |
|-------|---------|
| `groupname` | Name of the group. |
| `textname` | Human readable name. |
| `textdescription` | Description of the group. |
| `roles` | The roles granted to every client of the group. |
| `clients` | The clients that belong to the group. |

### Roles and access entries

| Field | Meaning |
|-------|---------|
| `rolename` | Name of the role. |
| `textname` | Human readable name. |
| `textdescription` | Description of the role. |
| `acls` | The access entries of the role. |

An access entry carries an `acltype`, a `topic`, an `allow` flag and a
`priority`. The plugin decides every event of the protocol on its own, so
a role that may subscribe to a topic filter is not automatically allowed
to receive the messages published to it.

| `acltype` | Meaning |
|-----------|---------|
| `publishClientSend` | The client may publish to the topic. |
| `publishClientReceive` | The client may receive the messages published to the topic. |
| `subscribeLiteral` | The client may subscribe to the exact topic. |
| `subscribePattern` | The client may subscribe to the topic filter. |
| `unsubscribeLiteral` | The client may unsubscribe from the exact topic. |
| `unsubscribePattern` | The client may unsubscribe from the topic filter. |

### Default access

The default access applies to every topic that no access entry matches.

| Field | Meaning |
|-------|---------|
| `publish_client_send` | The default of a publish. |
| `publish_client_receive` | The default of a delivery. |
| `subscribe` | The default of a subscription. |
| `unsubscribe` | The default of an unsubscription. |

### Membership

The plugin stores the membership of a group in the group itself, so the
group is the authority: a client a group lists belongs to it, and a
client no group lists does not. MCM normalises the document before it
plans a change, so the two views of a membership cannot drift apart.

## The management channel

The plugin answers on `$CONTROL/dynamic-security/v1` and publishes its
answers on `$CONTROL/dynamic-security/v1/response`. That channel
administers every permission of the broker, so it is the one thing in
this surface that must stay closed.

MCM connects with the broker account it already uses, which the adoption
turns into the management account of the plugin. Every command carries a
correlation value, and every answer is matched with the request it
belongs to, so an answer of another request is never mistaken for the
answer of this one.

The plugin disconnects the client it just changed and publishes its
answer after that disconnect. A change that touches the management
account therefore never reaches MCM. The state of the database is
read back and compared with the requested state after every change: the
verification of the state decides whether the change landed, not the
answer.

### The guard

The guard reports who may reach the management channel.

| Field | Meaning |
|-------|---------|
| `ok` | The channel is limited to the management account. |
| `management_client` | The account that administers the broker. |
| `allowed` | The principals that may reach the channel. |
| `violations` | The principals that must not reach it. |
| `reasons` | The findings in operator language. |

Three findings are refused by the preview:

- a client that may reach the channel and is not the management account;
- a default access that lets every client publish or subscribe — the
  plugin never grants fall-through access to `$CONTROL` topics, so the
  finding names the default that has to change;
- a management account that lost its access, because nobody could
  administer the database any more and the change could not be undone
  through the channel.

## The two access-control backends

The access control of the broker lives either in the password and ACL
files or in the database of the plugin. MCM detects which one is in
charge and never writes to one while the other is.

| `backend` | Meaning |
|-----------|---------|
| `none` | The broker does not restrict access. |
| `file` | The password and ACL files own the access control. |
| `dynamic-security` | The plugin owns the access control. |

### The adoption

The migration from the file backend to the plugin is an explicit
operation. `POST /api/v1/broker/dynsec/adopt` prepares it and changes
nothing on the broker:

- the users and the rules of the file backend become the clients and the
  roles of the plugin. A rule that applies to a user becomes a role of
  that user, a rule that applies to every user becomes a role every
  client carries, and a rule that applies to the anonymous clients
  becomes the group of the anonymous clients;
- the database of the plugin is written, with the passwords hashed the
  way the plugin hashes them (PBKDF2 over HMAC-SHA512, 101 iterations,
  a 12-byte salt);
- the configuration revision that loads the plugin and drops the
  password and ACL directives is stored;
- the passwords of the clients the migration creates are returned once.
  A user whose password MCM only knows as a hash gets a new one, which
  the answer names.

The plan the adoption returns is applied through
`POST /api/v1/broker/dynsec/apply`. The apply loads the plugin, which
needs a restart of the broker, and then verifies the state of the
database against the requested one.

A rule that applied to a single listener cannot be expressed by the
plugin, which decides the access control of the whole broker. The
adoption names every such rule in its warnings.

## Review and apply

The Dynamic Security surface follows the same review-and-apply contract
as the rest of MCM:

1. `POST /api/v1/broker/dynsec/preview` compares the requested document
   with the state of the broker and stores the plan as an immutable
   revision. The response carries the changes, the commands MCM is about
   to run and the guard report. A password never travels through the
   response: the commands of a client creation show it masked.
2. `POST /api/v1/broker/dynsec/apply` runs the reviewed plan and
   verifies the state. A change that cannot be verified is reported as a
   failure, and the plan may only be applied once.

The plugin surface follows the same contract with the preview and apply
of the plugin directives of the managed file.

## Secrets

- The password of a client is write-only. The API returns
  `password_set`, never the password.
- The plan of a change is stored without the passwords. The apply
  carries the document with the passwords and compares it with the
  reviewed plan, so a change that was not reviewed is refused.
- The audit trail carries the commands of a change with the passwords
  masked.
- The database of the plugin holds hashes, never cleartext passwords.

## Tests against a real broker

`internal/dynsec/broker_integration_test.go` runs against a real
Mosquitto with the dynamic security plugin. The test starts the broker
itself, with the `mosquitto` binary when `MOSQUITTO_BIN` points at one
and the `eclipse-mosquitto:2.0` container image otherwise, and it skips
when neither is available.

The tests prove that:

- the adapter reaches the plugin and the answers are matched with the
  requests;
- the access entries MCM writes are the ones the broker enforces: a
  client that may read the sensors may not publish to them, and a client
  that may publish may not read them;
- the clients that must not reach the management channel are refused by
  the broker, and a client that may reach it is found by the guard;
- a refused command is reported instead of being assumed to have worked;
- the plugin writes its own database after a change that went through
  the control channel.

No arbitrary installation is part of the tests: the plugin is the one
that ships with the broker image.

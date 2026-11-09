# Mosquitto directive support matrix

Generated from `internal/mosquitto/catalog` — DO NOT EDIT BY HAND.
Regenerate with `go run ./scripts/gen-support-matrix`.

MCM does not promise compatibility with every Mosquitto release. This page states
exactly which directives MCM manages for each broker version it supports, and
which directives it does not.

The three levels are:

- **managed** — MCM owns the directive end to end: it is edited through a
  dedicated graphical control, validated against the catalog, and written
  through a reviewed apply revision.
- **partial** — MCM knows the directive, validates it and preserves it
  losslessly on import and round-trip, but exposes no dedicated graphical
  control for it. The operator edits it in the advanced root editor.
- **unsupported** — the Mosquitto manual documents the directive for that
  version but MCM does not manage it. MCM preserves it byte-for-byte on
  import so a foreign configuration is never silently dropped, but it
  offers no editor and no validation for it.

A directive MCM does not manage is not a directive the broker rejects. Mosquitto
supports it; MCM only declines to administer it.

## Mosquitto 2.0

The Mosquitto 2.0 manual documents 106 directives. MCM manages 68 of them,
partially supports 5 and does not manage 34.

### Managed (68)

- `acl_file`
- `address`
- `allow_anonymous`
- `autosave_interval`
- `autosave_on_changes`
- `bridge_alpn`
- `bridge_cafile`
- `bridge_capath`
- `bridge_certfile`
- `bridge_identity`
- `bridge_insecure`
- `bridge_keyfile`
- `bridge_max_packet_size`
- `bridge_outgoing_retain`
- `bridge_protocol_version`
- `bridge_psk`
- `bridge_require_ocsp`
- `bridge_tls_version`
- `cafile`
- `capath`
- `certfile`
- `cleansession`
- `connection`
- `connection_messages`
- `idle_timeout`
- `keepalive_interval`
- `keyfile`
- `listener`
- `local_cleansession`
- `local_clientid`
- `local_password`
- `local_username`
- `log_dest`
- `log_facility`
- `log_timestamp`
- `log_timestamp_format`
- `log_type`
- `max_connections`
- `max_inflight_bytes`
- `max_inflight_messages`
- `max_keepalive`
- `max_packet_size`
- `max_queued_bytes`
- `max_queued_messages`
- `notification_topic`
- `notifications`
- `notifications_local_only`
- `password_file`
- `per_listener_settings`
- `persistence`
- `persistence_file`
- `persistence_location`
- `persistent_client_expiration`
- `plugin`
- `plugin_opt_config_file`
- `protocol`
- `queue_qos0_messages`
- `remote_clientid`
- `remote_password`
- `remote_username`
- `require_certificate`
- `restart_timeout`
- `round_robin`
- `start_type`
- `threshold`
- `tls_version`
- `topic`
- `try_private`

### Partial (5)

- `memory_limit`
- `message_size_limit`
- `port`
- `sys_interval`
- `user`

### Unsupported (34)

- `addresses`
- `allow_duplicate_messages`
- `allow_zero_length_clientid`
- `auth_plugin_deny_special_chars`
- `auto_id_prefix`
- `bind_address`
- `bind_interface`
- `bridge_attempt_unsubscribe`
- `bridge_bind_address`
- `check_retain_source`
- `ciphers`
- `clientid_prefixes`
- `crlfile`
- `dhparamfile`
- `http_dir`
- `include_dir`
- `max_qos`
- `max_topic_alias`
- `mount_point`
- `pid_file`
- `psk_file`
- `psk_hint`
- `retain_available`
- `set_tcp_nodelay`
- `socket_domain`
- `tls_engine`
- `tls_engine_kpass_sha1`
- `tls_keyform`
- `upgrade_outgoing_qos`
- `use_identity_as_username`
- `use_subject_as_username`
- `use_username_as_clientid`
- `websockets_headers_size`
- `websockets_log_level`

### Managed by surface

| Surface | Directives | Count |
| --- | --- | --- |
| settings | `autosave_interval`, `autosave_on_changes`, `connection_messages`, `log_dest`, `log_facility`, `log_timestamp`, `log_timestamp_format`, `log_type`, `max_connections`, `max_inflight_bytes`, `max_inflight_messages`, `max_keepalive`, `max_packet_size`, `max_queued_bytes`, `max_queued_messages`, `persistence`, `persistence_file`, `persistence_location`, `persistent_client_expiration`, `queue_qos0_messages` | 20 |
| listeners | `cafile`, `capath`, `certfile`, `keyfile`, `listener`, `protocol`, `require_certificate`, `tls_version` | 8 |
| bridges | `address`, `bridge_alpn`, `bridge_cafile`, `bridge_capath`, `bridge_certfile`, `bridge_identity`, `bridge_insecure`, `bridge_keyfile`, `bridge_max_packet_size`, `bridge_outgoing_retain`, `bridge_protocol_version`, `bridge_psk`, `bridge_require_ocsp`, `bridge_tls_version`, `cleansession`, `connection`, `idle_timeout`, `keepalive_interval`, `local_cleansession`, `local_clientid`, `local_password`, `local_username`, `notification_topic`, `notifications`, `notifications_local_only`, `remote_clientid`, `remote_password`, `remote_username`, `restart_timeout`, `round_robin`, `start_type`, `threshold`, `topic`, `try_private` | 34 |
| plugins | `plugin`, `plugin_opt_config_file` | 2 |
| access | `acl_file`, `allow_anonymous`, `password_file`, `per_listener_settings` | 4 |

## Mosquitto 2.1

The Mosquitto 2.1 manual documents 127 directives. MCM manages 70 of them,
partially supports 5 and does not manage 53.

### Managed (70)

- `acl_file`
- `address`
- `allow_anonymous`
- `autosave_interval`
- `autosave_on_changes`
- `bridge_alpn`
- `bridge_cafile`
- `bridge_capath`
- `bridge_certfile`
- `bridge_identity`
- `bridge_insecure`
- `bridge_keyfile`
- `bridge_max_packet_size`
- `bridge_outgoing_retain`
- `bridge_protocol_version`
- `bridge_psk`
- `bridge_require_ocsp`
- `bridge_tls_version`
- `cafile`
- `capath`
- `certfile`
- `cleansession`
- `connection`
- `connection_messages`
- `global_max_clients`
- `global_max_connections`
- `idle_timeout`
- `keepalive_interval`
- `keyfile`
- `listener`
- `local_cleansession`
- `local_clientid`
- `local_password`
- `local_username`
- `log_dest`
- `log_facility`
- `log_timestamp`
- `log_timestamp_format`
- `log_type`
- `max_connections`
- `max_inflight_bytes`
- `max_inflight_messages`
- `max_keepalive`
- `max_packet_size`
- `max_queued_bytes`
- `max_queued_messages`
- `notification_topic`
- `notifications`
- `notifications_local_only`
- `password_file`
- `per_listener_settings`
- `persistence`
- `persistence_file`
- `persistence_location`
- `persistent_client_expiration`
- `plugin`
- `plugin_opt_config_file`
- `protocol`
- `queue_qos0_messages`
- `remote_clientid`
- `remote_password`
- `remote_username`
- `require_certificate`
- `restart_timeout`
- `round_robin`
- `start_type`
- `threshold`
- `tls_version`
- `topic`
- `try_private`

### Partial (5)

- `memory_limit`
- `message_size_limit`
- `port`
- `sys_interval`
- `user`

### Unsupported (53)

- `accept_protocol_versions`
- `addresses`
- `allow_duplicate_messages`
- `allow_zero_length_clientid`
- `auth_plugin_deny_special_chars`
- `auto_id_prefix`
- `bind_address`
- `bind_interface`
- `bridge_attempt_unsubscribe`
- `bridge_bind_address`
- `bridge_ciphers`
- `bridge_max_topic_alias`
- `bridge_receive_maximum`
- `bridge_reload_type`
- `bridge_session_expiry_interval`
- `bridge_tcp_keepalive`
- `bridge_tcp_user_timeout`
- `bridge_tls_use_os_certs`
- `check_retain_source`
- `ciphers`
- `clientid_prefixes`
- `crlfile`
- `dhparamfile`
- `disable_client_cert_date_checks`
- `enable_control_api`
- `enable_proxy_protocol`
- `global_plugin`
- `http_dir`
- `include_dir`
- `listener_allow_anonymous`
- `max_qos`
- `max_topic_alias`
- `max_topic_alias_broker`
- `mount_point`
- `packet_buffer_size`
- `pid_file`
- `proxy_protocol_v2_require_tls`
- `psk_file`
- `psk_hint`
- `retain_available`
- `retain_expiry_interval`
- `set_tcp_nodelay`
- `socket_domain`
- `tls_engine`
- `tls_engine_kpass_sha1`
- `tls_keyform`
- `upgrade_outgoing_qos`
- `use_identity_as_username`
- `use_subject_as_username`
- `use_username_as_clientid`
- `websockets_headers_size`
- `websockets_log_level`
- `websockets_origin`

### Managed by surface

| Surface | Directives | Count |
| --- | --- | --- |
| settings | `autosave_interval`, `autosave_on_changes`, `connection_messages`, `global_max_clients`, `global_max_connections`, `log_dest`, `log_facility`, `log_timestamp`, `log_timestamp_format`, `log_type`, `max_connections`, `max_inflight_bytes`, `max_inflight_messages`, `max_keepalive`, `max_packet_size`, `max_queued_bytes`, `max_queued_messages`, `persistence`, `persistence_file`, `persistence_location`, `persistent_client_expiration`, `queue_qos0_messages` | 22 |
| listeners | `cafile`, `capath`, `certfile`, `keyfile`, `listener`, `protocol`, `require_certificate`, `tls_version` | 8 |
| bridges | `address`, `bridge_alpn`, `bridge_cafile`, `bridge_capath`, `bridge_certfile`, `bridge_identity`, `bridge_insecure`, `bridge_keyfile`, `bridge_max_packet_size`, `bridge_outgoing_retain`, `bridge_protocol_version`, `bridge_psk`, `bridge_require_ocsp`, `bridge_tls_version`, `cleansession`, `connection`, `idle_timeout`, `keepalive_interval`, `local_cleansession`, `local_clientid`, `local_password`, `local_username`, `notification_topic`, `notifications`, `notifications_local_only`, `remote_clientid`, `remote_password`, `remote_username`, `restart_timeout`, `round_robin`, `start_type`, `threshold`, `topic`, `try_private` | 34 |
| plugins | `plugin`, `plugin_opt_config_file` | 2 |
| access | `acl_file`, `allow_anonymous`, `password_file`, `per_listener_settings` | 4 |

## Why a directive can be unsupported

MCM manages the configuration it can guarantee. A directive it does not
administer stays in the file exactly as the operator wrote it, is reported
as unknown during validation, and never blocks an import. See the
[configuration workflow](./configuration-workflow.md) for the import, preview,
apply and recovery path the matrix applies to.

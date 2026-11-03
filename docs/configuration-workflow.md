# Broker configuration workflow and recent MFA

MCM manages the configured `mosquitto.conf` through immutable previews. This guide describes the confirmation prerequisite that the sensitive steps of that workflow share, and the workflow itself.

## Authorization and recent MFA

Sensitive operations ask the operator to confirm a second factor first. `POST /api/v1/auth/mfa/step-up` exchanges an unused TOTP code for a short-lived proof.

```bash
curl -sS -X POST http://localhost:8080/api/v1/auth/mfa/step-up \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"code":"123456"}'
```

```json
{ "proof": "<jwt>", "expires_at": "2026-11-03T09:05:00Z" }
```

The request needs an authenticated session whose account has MFA enabled. A wrong code, a code already used in the same 30-second window, or an account without an enrollment is refused; failed attempts are audited and share the configured login lockout budget with the login flow.

The proof is a JWT with a dedicated `action_mfa` purpose. It is **not** an access token:

- It authenticates nothing. Presenting it as a bearer token is rejected.
- It is bound to the exact `Authorization` header value of the session that requested it. The same account signed in on a second device cannot reuse the proof from the first.
- It is bound to the current MFA enrollment. Rotating or removing the factor invalidates proofs that were already issued.
- It expires five minutes after it is issued.
- It never carries a role. A sensitive operation still checks the caller's role, so a proof cannot elevate an operator's privileges.

Send the proof in the `X-MCM-MFA` header alongside the original bearer token. Keep it in browser memory only — do not store it in `localStorage` and do not log it.

In the UI, open **Administration → Account**. With MFA enabled, the *Recent MFA confirmation* form asks for an authenticator code and reports that the session is confirmed for up to five minutes. A code already used to confirm the enrollment or to sign in cannot be reused within the same 30-second window; wait for the next one.

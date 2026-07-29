# smtp2gmail

A lightweight, single-purpose SMTP-to-Gmail-API relay designed to run as a **sidecar container** alongside app containers (Ghost, Discourse, etc.) on a single Droplet/VM.

## The problem

Providers like DigitalOcean block outbound traffic on standard SMTP ports (25, 465, 587) to fight spam, but they don't block HTTPS calls to Google's APIs. Apps like Ghost and Discourse only know how to send mail *via SMTP*, though. `smtp2gmail` bridges that gap: it speaks just enough SMTP to satisfy those apps, and translates every accepted message into a Gmail API `messages.send` call over HTTPS.

It is **not** a general-purpose mail relay. It never accepts connections from outside the Droplet, never relays for arbitrary domains, and never talks to other MTAs. It is a local, trusted-network-only bridge with one job.

## Architecture

```
 ┌─────────────────────────┐        ┌──────────────────────────┐        ┌────────────┐
 │ Ghost / Discourse /...  │  SMTP  │       smtp2gmail          │  HTTPS │ Gmail API  │
 │ (app container)         │ ─────► │ (sidecar container)       │ ─────► │ (Google)   │
 │ SMTP host=localhost:587 │        │ AUTH → validate           │        │            │
 └─────────────────────────┘        │ DATA → parse MIME         │        └────────────┘
                                     │ → build gmail.users.messages.send
                                     └──────────────────────────┘
```

Both containers share the same Docker network (or the app container is configured with `network_mode: service:smtp2gmail` / `host`), so "localhost" from the app's point of view reaches the sidecar directly. The sidecar binds only to loopback/private interfaces — it is never published to a public port.

## MVP scope

| Area | Decision |
|---|---|
| **Runtime** | Go — compiles to a static binary, ships in a tiny distroless image (a few MB), and Go's goroutine model handles many concurrent SMTP connections cheaply without a thread-per-connection cost. |
| **Deployment** | Single container, meant to run as a sidecar via Docker Compose (or any orchestrator) alongside the apps that need outbound mail. |
| **Listeners** | One or more configurable TCP ports (e.g. 25, 587, 2525) so it can match whatever port each app's SMTP client defaults to. All listeners share the same config, credential check, and Gmail identity in the MVP — they're just alternate front doors, not independent tenants. |
| **Network exposure** | Binds to loopback / the Docker-internal network only. No public port publishing. Not hardened against hostile input the way an internet-facing MTA would need to be, because it will never see any. |
| **SMTP surface** | Just enough of RFC 5321/5322 to satisfy typical app mail libraries (Nodemailer, PHPMailer, Rails ActionMailer, etc.): `EHLO`, `AUTH LOGIN`/`AUTH PLAIN`, `MAIL FROM`, `RCPT TO`, `DATA`, `QUIT`. No STARTTLS/TLS — see below. |
| **TLS** | None in the MVP. Traffic never leaves the Droplet's internal network, so plaintext AUTH on a trusted local link is an accepted tradeoff for simplicity/performance. (Revisit if a client library refuses to send AUTH without STARTTLS being advertised — that's a cheap follow-up, not a redesign.) |
| **Authentication (SMTP side)** | A single fixed username/password pair, defined in config. `AUTH LOGIN`/`AUTH PLAIN` is advertised and enforced — mismatched credentials are rejected. This buys little as real access control (the Docker network is already trusted), but keeps compatibility with client libraries that expect to authenticate, and is cheap insurance against a future port-exposure misconfiguration. No per-app credential list in the MVP. |
| **Authentication (Google side)** | A Google Workspace **service account with domain-wide delegation** (not an App Password — Gmail's REST API only accepts OAuth2 bearer tokens, never basic auth, so the App Password credential generated earlier can't be used here). A Workspace admin grants the service account the `gmail.send` scope org-wide via domain-wide delegation; the sidecar authenticates as the service account and impersonates one specific Workspace address (the `subject` claim) to send as that user. This is fully headless: the JSON key is signed into a JWT and exchanged for a token entirely in code, with no browser/consent step at deploy time or ever after — unlike a 3-legged OAuth2 user-consent flow, which always requires at least one interactive authorization and can later expire/be revoked unpredictably. Because delegation impersonates the real mailbox, sending is genuinely "as" that Workspace user — same Sent folder, same organization's paid Workspace sending limits, not the lower consumer-Gmail limits. |
| **Message mapping** | One Gmail "send-as" identity for the whole sidecar instance in the MVP. Incoming messages are re-parsed and headers rewritten for consistency, rather than passed through byte-for-byte — this makes the sidecar resilient to misconfigured/inconsistent client apps. The enforced send-as address always wins for the actual envelope/`From`; if the client's `MAIL FROM` or message `From` header disagrees with the configured address, the message still sends (using the enforced address) but the sidecar logs a warning noting the mismatch, so it's visible that the app's configured "from" won't be what recipients actually see. |
| **Config** | Environment variables for everything simple (listen ports, SMTP username/password, Gmail send-as address, path to the service account key file), with an optional mounted config file for anything that doesn't fit comfortably in env vars. Env vars win if both are set. Designed to be trivial to wire up from `docker-compose.yml`. |
| **Secrets handling** | The SMTP username/password stay exactly as they're already handled today (env vars sourced from GitHub Actions secrets, injected at deploy time) — no change needed there. The service account JSON key is the one new secret shape: store it base64-encoded as a GitHub Actions secret, decode it to a file on the Droplet at deploy time (outside the git repo, never baked into the image), and bind-mount that file path read-only into the container. Same "never touches git" discipline as the existing app-password secrets, just one extra decode-to-file step in the pipeline. This is entirely the consuming project's deploy pipeline's responsibility, not smtp2gmail's own — see [docs/integration-guide.md](docs/integration-guide.md). File permissions need care: the container runs as a non-root user (UID 65532), so a naive `chmod 600` leaves it unreadable — see [docs/security.md](docs/security.md#file-permissions-on-the-decoded-key-learned-the-hard-way). |
| **Error handling** | Best-effort passthrough of Google API errors as real SMTP response codes, so calling apps see failures through whatever error-reporting they already have wired up for SMTP. Transient failures (rate limits/429s, 5xx from Google, connectivity trouble) map to SMTP `4yz` (temporary failure — client's mail library may retry on its own schedule). Permanent failures (invalid recipient, permission denied, etc.) map to SMTP `5yz` (permanent failure). If Google is unreachable or in an ongoing fault state, that surfaces as `4yz` errors on the affected SMTP transactions rather than via a separate health endpoint. All Google-side errors, including rate limiting, are handled gracefully (no crashes/hangs) and reflected back to the client this way in the MVP — no server-side retry queue yet. |
| **Health check** | None in the MVP — ongoing connectivity trouble to Google is surfaced through per-transaction SMTP error responses (see above) rather than a dedicated `/healthz`. |
| **Base image** | `gcr.io/distroless/static-debian12` (nonroot variant). Bundles CA certificates (needed for TLS to `googleapis.com`) without a shell or package manager, keeping the image minimal and reducing attack surface. `scratch` would need certs copied in manually for marginal size savings; `alpine` only helps if you want exec-in debugging, which this single-purpose container doesn't need. |
| **Observability** | Structured (JSON) logs to stdout — accept/reject decisions, Gmail API call outcomes, errors — so standard Docker log drivers pick them up with no extra config. |

## Non-goals for the MVP

- Not internet-facing; no hardening against arbitrary/hostile SMTP clients.
- No message queueing/retry persistence beyond simple in-memory retry on transient Gmail API errors.
- No multi-tenant routing (one login → one send-as address only) — see future features for the planned mapping model.
- No 3-legged OAuth2 user-consent flow (deliberately avoided even for the future multi-provider work — see below).
- No support for mail providers other than Gmail/Workspace.

## Future features (post-MVP)

- **Multiple outbound identities** — map distinct SMTP username/password pairs to distinct Gmail send-as addresses (still via the same service account, different `subject` claims), so several apps/mailboxes on one Droplet can each send as their own address. This requires **zero new Google Cloud setup**: domain-wide delegation is granted for the whole Workspace domain at once (not per-mailbox), so adding e.g. a second address on the same domain — say `no_reply@urabus.com` for transactional mail and `administrator@urabus.com` for owner reports — is purely a config change (a new local login mapped to a new `subject` value), reusing the exact same service account and JSON key. The local SMTP login is the key that will select the upstream identity.
- **Other provider backends** — pluggable senders beyond the Gmail API (e.g. Microsoft Graph), selected via the same local-login mapping above. The Google service-account JSON key format is **not reusable across providers** — it's Google's own credential schema, used to build a Google-specific signed JWT against Google's token endpoint. What generalizes is the *pattern*, not the format: a provider-specific secret file, mounted read-only, referenced by config, used for headless token acquisition, behind the shared `Sender.Send(ctx, message) error` interface (see [Repository layout](#repository-layout)). Any additional provider should be evaluated for an equivalent non-interactive, server-to-server credential model (e.g. Microsoft's app-only client-credentials flow) rather than a user-consent OAuth flow, consistent with the headless requirement that drove the Gmail service-account decision.
- **Retry queue** — cache outbound mail that hit a transient Google-side error (rate limit, 5xx) and retry it in the background instead of just failing the SMTP transaction, so callers don't need their own retry logic.

## Example deployment shape

```yaml
# docker-compose.yml (illustrative — not final)
services:
  smtp2gmail:
    image: smtp2gmail:latest
    restart: unless-stopped
    environment:
      SMTP_LISTEN_PORTS: "25,587"
      SMTP_USERNAME: "ghost"
      SMTP_PASSWORD: "${SMTP_RELAY_PASSWORD}"
      GMAIL_SEND_AS: "notifications@example.com"
      GMAIL_SA_KEY_FILE: "/secrets/service-account.json"
    volumes:
      - ./secrets/service-account.json:/secrets/service-account.json:ro
    networks:
      - internal

  ghost:
    image: ghost:latest
    environment:
      mail__transport: SMTP
      mail__options__host: smtp2gmail
      mail__options__port: 587
      mail__options__auth__user: "ghost"
      mail__options__auth__pass: "${SMTP_RELAY_PASSWORD}"
    networks:
      - internal
    depends_on:
      - smtp2gmail

networks:
  internal:
    internal: true
```

## Distribution & image registry

Other projects (Ghost, Discourse, or anything else needing this sidecar) integrate by pulling a pre-built image rather than building from source — see [docs/integration-guide.md](docs/integration-guide.md) for the consuming side.

- **Registry: GitHub Container Registry (`ghcr.io`)**, tied directly to this repo. A GitHub Actions workflow in this project builds and pushes the image on tag/release.
- **Visibility: public.** No registry auth needed by any consuming pipeline or Droplet — a plain `docker pull`/`docker compose up` just works. This is safe because no secret (SMTP credentials, service-account key) is ever baked into the image; everything sensitive is supplied at runtime via env vars and a mounted file.
- **Versioning**: images are tagged with semver (e.g. `ghcr.io/sperry04/smtp2gmail:v0.1.0` — published, public, and pullable with no credentials as of this writing). Consuming projects should pin to a specific version tag in their `docker-compose.yml`, never `:latest`, so an unrelated deploy of *their* project doesn't silently pick up a new, unvetted `smtp2gmail` build.

## Header rewrite policy

Resolves the earlier open question on MIME rewriting. Headers fall into four buckets:

| Bucket | Headers | Behavior |
|---|---|---|
| **Always enforced** | `From` | Address is always rewritten to the configured `GMAIL_SEND_AS`, since that's the only identity the service account is authorized to impersonate. If the client supplied a display name (e.g. `"My Blog" <wrong@domain.com>`), the name is preserved and only the address is replaced (`"My Blog" <no_reply@yourdomain.com>`). If the client's address didn't match, a warning is logged noting the mismatch (see MVP scope table above) — the send still succeeds. |
| **Filled in if missing/invalid, otherwise left alone** | `Date`, `Message-ID` | `Date` is set to the current time if absent or malformed. `Message-ID` is generated only if absent — a client-supplied, well-formed `Message-ID` is preserved as-is, since the calling app may rely on it to correlate outbound mail with its own records. |
| **Stripped if present** | `Bcc`, `Return-Path`, any pre-existing `Received` | These should never come from the client: `Bcc` recipients are handled via the SMTP envelope (`RCPT TO`), not a visible header, so a literal `Bcc` header is dropped rather than forwarded; `Return-Path`/`Received` are meant to be added by receiving infrastructure, not asserted by a sender, so any client-supplied copies are discarded defensively. |
| **Left untouched** | `To`, `Cc`, `Subject`, `Reply-To`, `MIME-Version`, `Content-Type`, `Content-Transfer-Encoding`, any custom `X-*` headers, and the message body | These are app-controlled content with no bearing on sender identity or Gmail API validity. |

One related nuance worth calling out: the SMTP session's `MAIL FROM` (the envelope sender, distinct from the `From` header) is used only for the mismatch-detection/logging above — it is never forwarded to Gmail. The Gmail API's `raw` field carries only the RFC 5322 header/body; actual sending identity is determined entirely by which mailbox the service account impersonates (the `subject` claim), independent of anything the client asserted.

## Security considerations

Worth documenting prominently (README + client runbook), since this project may end up in other people's hands:

- **Domain-wide delegation is domain-wide, not per-mailbox.** Once granted, the service account's key can be used to impersonate *any* address in the Workspace under the granted scope (`gmail.send`), not just the address(es) the sidecar happens to be configured with. Whoever holds the JSON key holds that blast radius — treat it like a domain admin credential, not a per-app password.
- **The JSON key must never reach git or the image.** Base64-encoded GitHub Actions secret → decoded to a file on the host at deploy time → read-only bind mount. Never `COPY`'d into the Dockerfile, never committed. The container runs as a non-root user (UID 65532), so the decoded file needs `chown 65532:65532` + `chmod 400`, or a simpler `chmod 644` if the deploy step can't `chown` — a plain `chmod 600` leaves it unreadable to the container, confirmed the hard way during this project's own smoke test. See [docs/security.md](docs/security.md#file-permissions-on-the-decoded-key-learned-the-hard-way).
- **No TLS on the SMTP listener is a deliberate, scoped tradeoff** — acceptable only because the sidecar is provably unreachable from outside the Droplet (loopback/internal Docker network only, never a published port). Anyone reusing this project should re-examine that assumption if their deployment topology differs (e.g. a shared Kubernetes cluster where "internal network" trusts more parties than a single Droplet does).
- **SMTP AUTH is compatibility, not access control** — see the MVP scope table. Real access control here is network isolation, not the credential check.

## Testing strategy

- **Unit tests per package**: SMTP protocol state machine (`smtpd`) — command sequencing, AUTH handling, DATA parsing; header rewriting logic; Google-error-to-SMTP-code mapping; config loading/validation (missing/invalid env vars, precedence of file vs env).
- **Integration test**: drive a real SMTP client (or raw socket) against a running instance of the sidecar, with the `gmail` package's HTTP calls pointed at a mocked Gmail API server (Go's `httptest`) instead of real Google endpoints. Verifies the full path — SMTP session → parsed message → constructed Gmail API request — without ever touching production Google infrastructure or requiring live credentials in CI.
- No test should ever require real Google credentials or hit the live Gmail API — the mocked server is the contract boundary for anything Google-related in CI.

## Documentation & repository layout

Split into packages from the start, even though the MVP only has one provider — this is deliberately set up to keep the future "other provider backends" and "multiple outbound identities" work additive rather than a rewrite. Docs live in `docs/` rather than growing the README indefinitely, since this may end up customer- or public-facing:

```
smtp2gmail/
├── cmd/smtp2gmail/       # main package — wiring/startup only
├── smtpd/                # SMTP protocol handling: listeners, AUTH, DATA parsing, error → SMTP-code mapping
├── gmail/                # Gmail API sender: JWT/service-account auth, message construction, send calls
├── config/               # env var / config file loading and validation
├── docs/
│   ├── google-workspace-setup.md   # client-facing runbook: GCP project, service account, domain-wide delegation
│   ├── integration-guide.md        # for engineers/agents wiring this sidecar into another project
│   ├── configuration.md            # full env var / config file reference
│   └── security.md                 # expanded version of the Security considerations above
├── examples/
│   ├── docker-compose.ghost.yml    # reference compose file wiring smtp2gmail + Ghost
│   └── docker-compose.discourse.yml
├── scripts/
│   └── gcp-setup.sh                # automates the scriptable half of docs/google-workspace-setup.md
├── Dockerfile
├── docker-compose.yml    # minimal example / quickstart
└── README.md             # high-level pitch, architecture, quickstart — links into docs/
```

`smtpd` and `gmail` talk to each other through a small internal interface (something like `Sender.Send(ctx, message) error`), so a future second provider package (e.g. `msgraph/`) can be dropped in and selected per-login without touching the SMTP layer.

A license (MIT vs Apache 2.0) is deliberately not decided yet — revisit before actually publishing.

## Open questions / next steps before implementation

The design is settled. One item remains before writing code:

1. **Deploy pipeline changes** — confirm the exact mechanism for getting the base64-encoded service account key from a GitHub Actions secret onto the Droplet as a file (e.g. extending the existing Action that already provisions the app-password secrets for Ghost/Discourse), and add the GitHub Actions workflow that builds/pushes the image to `ghcr.io` on tag.

Related docs now exist: [docs/google-workspace-setup.md](docs/google-workspace-setup.md) (Google-side runbook) and [docs/integration-guide.md](docs/integration-guide.md) (for wiring this sidecar into another project).

Let's settle the remaining item, then move to implementation.

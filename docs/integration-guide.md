# Integration guide (for agents/engineers wiring this into another project)

This doc is meant to be handed to whoever (human or AI coding assistant) is integrating `smtp2gmail` into a *different* project's deployment — e.g. adding it as a sidecar alongside Ghost, Discourse, or any other app that needs outbound mail but is deployed somewhere that blocks outbound SMTP ports (DigitalOcean Droplets, for example).

## What this service is

`smtp2gmail` is a container that:
- Listens on one or more local TCP ports, speaking just enough SMTP to satisfy typical mail-sending libraries (`EHLO`, `AUTH LOGIN`/`AUTH PLAIN`, `MAIL FROM`, `RCPT TO`, `DATA`, `QUIT`).
- Accepts a **single fixed username/password** for AUTH (MVP — see [README](../README.md#future-features-post-mvp) for planned multi-credential support).
- Converts each accepted message into a Gmail API `messages.send` call, sending as one configured Google Workspace address (via a service account with domain-wide delegation — no interactive Google auth involved at runtime).
- **Never** accepts connections from outside its own Docker network — it is not a general-purpose relay, and should never be given a published/host port.

It is meant to run as a sidecar in the *same* `docker-compose.yml` (or equivalent) as the app(s) that need to send mail, on a shared internal network.

## Prerequisites before integrating

The consuming project needs, from whoever owns the Google Workspace being used:
1. A Google service-account **JSON key file** with domain-wide delegation already granted for the `gmail.send` scope (see [google-workspace-setup.md](./google-workspace-setup.md) — this is a one-time setup done by a Workspace admin, not something the integrating pipeline does itself).
2. The **email address** to send as (e.g. `no_reply@yourdomain.com`).
3. A chosen SMTP username/password pair the consuming app(s) will use to authenticate to the sidecar (these are made up by whoever is deploying — they're local-only credentials, not Google credentials).

## Wiring it into `docker-compose.yml`

```yaml
services:
  smtp2gmail:
    image: ghcr.io/sperry04/smtp2gmail:v0.1.0   # pin to a specific published tag — never `:latest` in production
    restart: unless-stopped
    environment:
      SMTP_LISTEN_PORTS: "587"
      SMTP_USERNAME: "myapp"
      SMTP_PASSWORD: "${SMTP_RELAY_PASSWORD}"      # supply via your own secrets mechanism
      GMAIL_SEND_AS: "no_reply@yourdomain.com"
      GMAIL_SA_KEY_FILE: "/secrets/service-account.json"
    volumes:
      - ./secrets/service-account.json:/secrets/service-account.json:ro
    networks:
      - internal
    # No public ports. Do not add a `ports:` mapping — this service must only be
    # reachable from other containers on the `internal` network.


  myapp:
    image: your-app:latest
    environment:
      SMTP_HOST: smtp2gmail       # resolves via Docker's internal DNS on the shared network
      SMTP_PORT: 587
      SMTP_USER: "myapp"
      SMTP_PASS: "${SMTP_RELAY_PASSWORD}"          # same value as SMTP_PASSWORD above
      SMTP_SECURE: "false"                          # see "No TLS" note below
    networks:
      - internal
    depends_on:
      - smtp2gmail

networks:
  internal:
    internal: true
```

Adjust the app-specific env var *names* above (`SMTP_HOST` etc.) to whatever the consuming app's own mail configuration expects — see [README examples](../README.md#example-deployment-shape) for a concrete Ghost example, and the `examples/` folder for Ghost/Discourse reference compose snippets.

**File permission gotcha**: the published image runs as a non-root user (UID 65532). If your deploy pipeline decodes the service-account key from a CI secret onto the host with `chmod 600`, the container won't be able to read it — confirmed the hard way during this project's own smoke test. Use `chown 65532:65532` + `chmod 400`, or a simpler `chmod 644` if your deploy step can't `chown`. See [security.md](./security.md#file-permissions-on-the-decoded-key-learned-the-hard-way) for the full explanation.

## Environment variables the sidecar accepts (MVP)

| Variable | Required | Description |
|---|---|---|
| `SMTP_LISTEN_PORTS` | yes | Comma-separated list of TCP ports to listen on (e.g. `"25,587"`). All ports share identical behavior in the MVP. |
| `SMTP_USERNAME` | yes | The single username the consuming app(s) will authenticate with. |
| `SMTP_PASSWORD` | yes | The matching password. |
| `GMAIL_SEND_AS` | yes | The Workspace email address to impersonate/send as. |
| `GMAIL_SA_KEY_FILE` | yes | Path (inside the container) to the mounted service-account JSON key file. |

See [configuration.md](./configuration.md) for the full/authoritative reference as it evolves.

## Things to design around (current MVP limitations)

- **No TLS/STARTTLS.** The sidecar only ever speaks plaintext SMTP. This is safe *only* because it's unreachable outside the Docker-internal network — don't configure the consuming app to require/attempt STARTTLS against it, and don't expose this service on a published port under any circumstances.
- **No health-check endpoint.** There's no `/healthz` or readiness probe yet — connectivity trouble to Google surfaces as SMTP `4xx`/`5xx` responses on individual send attempts, not as a separate down/up signal. A plain `depends_on:` (container-started, not health-checked) is sufficient for startup ordering; don't wire a `condition: service_healthy` dependency against it.
- **Errors are real SMTP response codes** — a Gmail API failure (bad recipient, rate limit, etc.) comes back as an actual SMTP rejection, not a silent drop. Make sure whatever mail library the consuming app uses surfaces/logs SMTP errors rather than swallowing them, so failures are visible.
- **Single fixed credential pair, single send-as address** in the MVP — no per-app credential list, no multiple mailboxes yet. If the target project needs more than one outbound identity, that's a planned future feature, not yet implemented (check the main README's "Future features" section for current status before assuming it exists).

## Image & versioning

The image is published publicly to GitHub Container Registry: `ghcr.io/sperry04/smtp2gmail`. Pin deployments to a specific version tag (e.g. `:v0.1.0`, the current first release), not `:latest` — see the main [README](../README.md) for the current release/tagging scheme.

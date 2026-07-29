# Configuration reference

smtp2gmail is configured entirely through environment variables (MVP scope -- see the main [README](../README.md#mvp-scope)).

| Variable | Required | Example | Description |
|---|---|---|---|
| `SMTP_LISTEN_PORTS` | yes | `"25,587"` | Comma-separated TCP ports to listen on. All ports share identical behavior -- there's no per-port configuration in the MVP, they exist purely so the sidecar can match whatever port a client app's mail library defaults to. |
| `SMTP_USERNAME` | yes | `"ghost"` | The single SMTP AUTH username accepted. |
| `SMTP_PASSWORD` | yes | `"a-strong-random-value"` | The matching password. Auth is enforced (mismatches get `535`), but note this is about client-library compatibility, not real access control -- see [security.md](./security.md). |
| `GMAIL_SEND_AS` | yes | `"no_reply@yourdomain.com"` | The Google Workspace address the service account impersonates via domain-wide delegation. This becomes the enforced `From` on every outbound message, regardless of what the client sends -- see the README's "Header rewrite policy". |
| `GMAIL_SA_KEY_FILE` | yes | `"/secrets/service-account.json"` | Path *inside the container* to the mounted service-account JSON key file. Get this file via [google-workspace-setup.md](./google-workspace-setup.md) (or `scripts/gcp-setup.sh`). Never bake this file into the image -- always mount it read-only at runtime. |

## Validation

All five variables are required; `smtp2gmail` fails fast at startup with a clear error naming the missing/invalid variable rather than starting in a half-configured state. `SMTP_LISTEN_PORTS` entries must each parse as an integer in the 1-65535 range.

## What's intentionally not configurable yet

- **No TLS/STARTTLS options** -- the listener never advertises or supports TLS in the MVP (see README "Security considerations" for why that's an acceptable tradeoff here).
- **No multiple credential pairs / multiple send-as addresses** -- one `SMTP_USERNAME`/`SMTP_PASSWORD` pair, one `GMAIL_SEND_AS` address, for the whole process. Multiple identities per sidecar instance is a planned future feature (see README "Future features"), not yet implemented.
- **No config file support** -- despite being mentioned as a future option in early planning, everything currently fits comfortably in env vars, so no file-based config path was implemented. If/when config outgrows env vars (e.g. multiple credential-to-identity mappings), that's the point to revisit this.

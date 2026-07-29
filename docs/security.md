# Security considerations

Expanded version of the README's "Security considerations" section, plus implementation-level detail. Read this before deploying, and especially before handing this project to another customer/admin.

## Domain-wide delegation is domain-wide, not per-mailbox

Once a service account is granted domain-wide delegation for the `gmail.send` scope, its JSON key can be used to impersonate **any address in the Workspace domain**, not just the address(es) `smtp2gmail` happens to be configured with. Whoever holds that JSON key holds that blast radius -- treat it like a domain admin credential, not a per-app password. This is a Google Workspace/Cloud property, not something this project's code can narrow further.

## The JSON key must never reach git or the image

- The `Dockerfile` never `COPY`'s the key into the image -- it's mounted at runtime as a read-only volume.
- `.gitignore` in this repo excludes `*-key.json` and a `secrets/` directory by convention.
- In a deploy pipeline: base64-encode the key file into a CI secret, decode it to a file on the target host at deploy time (never inside the git working tree), and bind-mount it read-only. See [google-workspace-setup.md](./google-workspace-setup.md) Part 3.

### File permissions on the decoded key (learned the hard way)

The published image runs as the distroless "nonroot" user (**UID 65532**), not root and not whatever user the deploy script runs as. A naive `chmod 600` on the decoded key file makes it unreadable to that container user entirely -- confirmed by an actual `permission denied` during this project's own end-to-end smoke test. Two ways to fix it, in order of preference:

1. **`chown 65532:65532` the decoded file, then `chmod 400`** -- tightest option, readable only by the exact UID the container runs as. Requires the deploy step to have privilege to `chown` to an arbitrary UID (usually fine if the deploy script runs as root/via sudo on the Droplet).
2. **`chmod 644`** -- simplest, no `chown` needed, works regardless of the deploy user's privileges. World-readable on the host, which is an acceptable tradeoff under this project's existing trust model (the Droplet itself is the security boundary -- see "No TLS" below), but avoid it if the Droplet runs workloads for multiple mutually-untrusting tenants.

## No TLS is a deliberately scoped tradeoff

The SMTP listener never advertises or supports STARTTLS. This is only safe because:
- The container is meant to bind on all interfaces *within its own network namespace* (standard for a Docker sidecar reachable via a service name on a shared bridge network), but
- It must **never** have a `ports:` mapping published to the host, and
- It must run on an `internal: true` (non-externally-routable) Docker network, or otherwise be provably unreachable from outside the Droplet/host.

If you're adapting this project to a different deployment topology (e.g. a shared Kubernetes cluster where "the internal network" trusts more parties than a single Droplet does), re-examine this assumption before reusing the no-TLS design as-is.

## SMTP AUTH is compatibility, not access control

The single fixed username/password pair is checked with a constant-time comparison (`crypto/subtle.ConstantTimeCompare`) to avoid a timing side-channel, but the real reason it exists is client-library compatibility -- some SMTP libraries misbehave if a server never advertises `AUTH` at all. Network isolation (see above) is what actually keeps this service safe, not the credential check.

## Error messages may echo upstream detail

Gmail API error bodies are included in the internal error passed up to the SMTP layer (for logging and for constructing the SMTP response). These aren't currently scrubbed of any potentially sensitive detail Google's API might include in an error body. If you're operating this in an environment where SMTP response text might be visible to less-trusted parties, review `gmail/client.go`'s `classifyError` and `smtpd/session.go`'s `codeForError` before relying on that boundary.

## Dependencies

The only third-party dependency is `golang.org/x/oauth2` (and its `google` subpackage), used solely for the service-account JWT/token exchange. The Gmail API call itself is a direct `net/http` request rather than the full generated `google.golang.org/api/gmail/v1` client, keeping the dependency surface intentionally small.

# Google Workspace setup runbook

This walks through the one-time Google-side setup `smtp2gmail` needs: a Google Cloud service account with domain-wide delegation, scoped to send mail as one or more addresses in your Workspace domain. You do this **once per Workspace/domain**, regardless of how many mailboxes you later configure the sidecar to send as.

You'll need:
- A Google Cloud account (a project is free to create; the Gmail API's usage here has no cost).
- **Google Workspace super admin access** for the domain you're sending from (required for the domain-wide delegation step — this cannot be delegated to a lesser admin role).

By the end, you'll have two things to feed into `smtp2gmail`'s config: a **JSON key file** and the **email address(es)** you want to send as.

This runbook assumes you (or whoever is doing this) may be doing it on a **brand-new Google Cloud account that's never been touched before**, with no prior GCP experience. That's a normal starting point — Google Cloud is a separate product from Workspace, and signing into it for the first time triggers some one-time setup of its own before you ever get to "create a service account."

---

## Part 0 — First time in Google Cloud

If you've never used Google Cloud before, expect the following before you reach the steps in Part 1:

1. **Go to [console.cloud.google.com](https://console.cloud.google.com/) and sign in with your Workspace super admin account** (not a personal Gmail account — the Cloud project needs to live under your Workspace's organization, and only signing in with a Workspace identity makes that happen automatically).

2. **Accept the Terms of Service** if prompted — a one-time click-through the first time any account in your domain touches Google Cloud.

3. **"Organization" auto-provisioning.** The very first time *anyone* in your Workspace domain signs into Cloud Console, Google automatically creates a Cloud **Organization** resource tied to your domain. You may see a brief "organization is being set up" state, or a short delay before the console fully loads — this is normal and usually resolves in well under a minute, occasionally longer. You don't need to do anything to trigger this; it happens as a side effect of signing in.
   - **Terminology note**: an **Organization** is the top-level container Google creates once per Workspace domain — you won't create or manage it directly in this runbook. A **Project** (which you create in Part 1, step 1) is a container *inside* that organization for one specific thing (in this case, the `smtp2gmail` service account and the Gmail API). Don't confuse the two — everything you actively click through below happens at the project level.

4. **Billing account prompt.** Google Cloud may ask you to set up a billing account (i.e. attach a payment method) before letting you create a project or enable an API, even though nothing in this runbook incurs any cost — Gmail API usage here is free. This is a standard Google Cloud account requirement, not something specific to this project; if prompted, follow Google's flow to add a billing account and attach it to the project you create in Part 1.

5. **Default organization policies may be stricter than you expect.** Brand-new Cloud organizations frequently come with security-hardening policies pre-enabled by default — including one that blocks the JSON key download step in Part 1. This is common enough on fresh accounts that it's covered directly in [Troubleshooting](#troubleshooting) below; if you hit an error at Part 1, step 4, that's almost certainly it, and it's expected, not a sign something's misconfigured.

Once you're past this one-time onboarding, the rest of the setup (Parts 1–3) is the same regardless of how new the account is.

## Quick path: run the setup script

Everything in Part 1 below is scriptable via the `gcloud` CLI, and doing it that way skips the confusing Cloud Console UI entirely (including the "Edit → permissions simulation" flow the console's org-policy editor shows, which is a rough edge of the console specifically, not a required part of the underlying process).

1. Install the [gcloud CLI](https://cloud.google.com/sdk/docs/install) if you don't have it, then authenticate as your Workspace super admin account:
   ```
   gcloud auth login
   ```
2. Run the script from this repo:
   ```
   ./scripts/gcp-setup.sh smtp2gmail-yourdomain smtp2gmail-sender
   ```
   (first argument is the project ID to create, second is the service account name — both are up to you).
3. The script creates the project, enables the Gmail API, creates the service account, grants the project-scoped org-policy exception for key creation, generates the JSON key, and prints the Client ID you need for the next part.

If the script fails partway through, it's safe to re-run — it skips steps that already succeeded (existing project, existing service account) rather than erroring out.

This still leaves Part 2 (domain-wide delegation) as a manual step — Google has never exposed an API for that specific action, so there's no way to script around it.

**If you'd rather click through the console yourself** (e.g. to understand each step, or because `gcloud` isn't available to you), the same steps are spelled out manually below in Part 1.

## Part 1 — Google Cloud Console: create the service account (manual alternative to the script above)

1. In Cloud Console, either select an existing project or create a new one (e.g. name it `smtp2gmail` or something identifying the client/domain it's for). If this is a brand-new account, you likely have no existing projects yet — use **Create Project** from the project picker at the top of the page.

2. Enable the Gmail API:
   - **APIs & Services → Library**
   - Search for **Gmail API**
   - Click **Enable**

3. Create the service account:
   - **IAM & Admin → Service Accounts → Create Service Account**
   - Give it a name (e.g. `smtp2gmail-sender`)
   - You do **not** need to grant it any project-level IAM roles — domain-wide delegation is configured separately, in Workspace Admin, not here
   - Click **Done**

4. Generate the JSON key:
   - Click into the service account you just created
   - Go to the **Keys** tab → **Add Key** → **Create new key** → **JSON** → **Create**
   - This downloads a `.json` file — this is the credential `smtp2gmail` will use. Treat it as sensitive from this point forward (see [Security considerations](../README.md#security-considerations) in the README — anyone holding this file can send as any address you grant it below).
   - **If this fails with an organization policy error** ("An Organization Policy that blocks service account key creation has been enforced..."), see [Troubleshooting](#troubleshooting) below — this is a common default on fresh Cloud accounts, not a sign of misconfiguration, and there's a specific fix (the script above already handles this automatically).

5. Note the service account's **Client ID** (sometimes labeled "OAuth 2 Client ID" or "Unique ID"): it's on the service account's **Details** tab. This is a numeric ID, distinct from the service account's `...@...gserviceaccount.com` email address — you'll need this exact number for the next part.

## Part 2 — Google Workspace Admin: grant domain-wide delegation

1. Go to [admin.google.com](https://admin.google.com/) (signed in as a super admin for the domain).

2. Navigate to **Security → Access and data control → API Controls → Domain-wide Delegation**.

3. Click **Manage Domain Wide Delegation → Add new**.

4. Fill in:
   - **Client ID**: the numeric Client ID from Part 1, step 5
   - **OAuth scopes**: `https://www.googleapis.com/auth/gmail.send`

5. Click **Authorize**.

That's it — this single grant covers the *entire domain*. You do not need to repeat this step when adding a second or third mailbox later (e.g. going from just `no_reply@yourdomain.com` to also including `administrator@yourdomain.com`); those are just config changes on the `smtp2gmail` side.

## Part 3 — What to hand to `smtp2gmail`

For each mailbox you want the sidecar able to send as, you need:

- The **JSON key file** from Part 1, step 4 (the same file is reused for every mailbox on this domain — it's the delegation grant, not the file, that's domain-wide).
- The **email address** to impersonate (e.g. `no_reply@yourdomain.com`) — this is the `subject` the sidecar uses when requesting a token, and becomes the enforced send-as/From address for that login.

See the main [README](../README.md) and [configuration reference](./configuration.md) for exactly how these plug into the container's environment variables and secret-file mount, and [security.md](./security.md) for how to handle the JSON key safely in a deploy pipeline (short version: base64-encode it into a CI secret, decode to a file on the host at deploy time, never commit it, never bake it into the image).

## Verifying it worked

Once `smtp2gmail` is running with this key and a configured send-as address, send a test message through it and confirm:
- It arrives at the recipient with the expected `From`.
- It shows up in the **Sent** folder of the impersonated mailbox in Gmail/Workspace — this is the surest sign delegation is working correctly, since a misconfigured grant (wrong Client ID, wrong scope, or scope not yet propagated) will fail as an auth error before the message ever gets that far.

## Troubleshooting

- **"An Organization Policy that blocks service account key creation has been enforced..."** (Part 1, step 4) — Google actually has **two separate constraints** that can cause this: the classic `iam.disableServiceAccountKeyCreation`, and a newer "managed constraint" version, `iam.managed.disableServiceAccountKeyCreation` (Google's replacement mechanism with dry-run/simulation support). Different orgs enforce one, the other, or both — fresh Cloud organizations commonly have one or both pre-enabled by default as a security best practice. This doesn't mean anything is wrong; it's expected on a new account. **If you used `scripts/gcp-setup.sh`, both are already handled for you** — the script overrides both constraint IDs via `gcloud org-policies set-policy` before attempting key creation, and skips gracefully if a given constraint doesn't exist in your org. If you're doing this manually in the console instead, fix it by adding a **project-scoped exception for each** (don't disable either org-wide — that weakens security for every future project, not just this one):
  1. In Cloud Console, go to **IAM & Admin → Organization Policies**, making sure the resource picker at the top is scoped to *this project* (not the organization node).
  2. Search for **Disable service account key creation** (`iam.disableServiceAccountKeyCreation`) — repeat these steps again afterward for its managed-constraint counterpart if it's also listed (`iam.managed.disableServiceAccountKeyCreation`).
  3. Click it → **Manage Policy** → **Edit** → choose **Customize** → set enforcement to **Off** for this project → **Save**.
  4. Retry Part 1, step 4. If it still fails citing `iam.managed.disableServiceAccountKeyCreation` specifically, that confirms the org also enforces the managed version and you need to repeat the override for that constraint ID too.

  This requires the **Organization Policy Administrator** role (`roles/orgpolicy.policyAdmin`) on the org, folder, or at least this project. That role is *not* automatically granted by Workspace super admin status — they're separate permission systems. If you get a permissions error making this change, it means whoever actually administers the Cloud Organization resource (which may or may not be you, even if you're a Workspace super admin) needs to either grant you that role or make the project-level exception themselves. On a brand-new account you set up yourself, you're usually already the Organization Admin and can do this directly — but if this account was provisioned by someone else on your behalf, that's the point to loop them in.

- **"unauthorized_client" or similar auth errors** — double check the Client ID entered in Workspace Admin matches the service account's OAuth Client ID exactly (not its email address), and that the scope is exactly `https://www.googleapis.com/auth/gmail.send`.
- **Delegation grant not taking effect immediately** — Google's documentation notes propagation can occasionally take a few minutes; if an auth error persists longer than that, re-check the Client ID/scope entry first before assuming it's a code issue.
- **Wrong sender identity in Sent mail** — confirm the `subject` (impersonated address) configured for that login matches the mailbox you expected; a valid-but-wrong address will still succeed, it'll just send as the wrong person.

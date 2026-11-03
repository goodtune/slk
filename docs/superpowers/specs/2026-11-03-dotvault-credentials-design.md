# Slack credentials from Vault (dotvault) — design

## Problem

slk keeps each workspace's Slack session (the `xoxc-` token and the `d` cookie) in a plaintext JSON file under `~/.local/share/slk/tokens/`. Where a user already runs [dotvault](https://github.com/goodtune/dotvault), the session belongs in Vault: enrolled once, refreshed by the dotvault daemon, never on disk.

dotvault's `slack` enrolment engine (dotvault `docs/services/slack.md`) writes the secret; slk reads it through dotvault's public client (`github.com/goodtune/dotvault/client`). That is the whole dependency: slk imports the client, dotvault imports nothing of slk's, so the two modules never depend on each other. The secret's shape is the contract between them.

## The secret

`kv/users/<you>/<secret>` (for example `slack/slk`) holds one field per workspace, named by its Slack subdomain, whose value is a JSON object:

```json
{"domain": "acme", "team_id": "T01ACME", "team_name": "Acme",
 "user_id": "U02ME", "access_token": "xoxc-…", "cookie": "xoxd-…"}
```

and two bookkeeping fields, `issued_at` / `expires_at`, which slk ignores. Any field that is not such an object (bookkeeping, or something written by hand) is skipped; a field that is one but lacks a token, cookie or team ID is skipped with a logged warning naming the field, never its value.

## Configuration

```toml
[vault]
secret = "slack/slk"        # path under kv/users/<you>/; empty (default) = off
# config = "/etc/xdg/dotvault/config.yaml"   # dotvault's config; default is dotvault's own default
```

Everything about reaching Vault (address, auth method, KV mount, user prefix, the token file, borrowing a token from the dotvault daemon's socket) comes from dotvault's configuration through `client.LoadConfig`, so slk reads from exactly where dotvault writes.

## Startup

1. Load the token files as today.
2. If `[vault] secret` is set: load dotvault's config, `AuthenticateCached` (environment, token file, the daemon's socket, a client certificate — never a prompt or a browser), and `ReadUserSecretFields(secret)`. Each workspace field becomes a token with `Source = "vault"`, held in memory only.
3. A Vault workspace replaces a token file for the same team ID (Vault is where the user moved it); the file is left alone, not deleted.
4. Tokens from Vault are never written: the startup re-mint skips them (as it skips browser tokens: the desktop app may be signed in as someone else, and saving would put the session back on disk). dotvault's refresher keeps the Vault copy fresh.

Failures do not stop slk from starting with the workspaces it does have. Each is reported on stderr in words that say what to do:

| Outcome | Message |
|---|---|
| No usable token (`ErrLoginRequired`) | run `dotvault login` |
| Vault unreachable | Vault is unreachable; using the other workspaces |
| Denied | the token cannot read `<secret>` |
| Secret absent | nothing enrolled at `<secret>`; run `dotvault enrol <secret>` |

When `[vault] secret` is set, slk does not launch the first-run onboarding because the Vault read came up empty: onboarding writes a token file, which is what the user moved away from. It exits with the message instead. Without `[vault]`, startup is unchanged.

## Not in scope

- `slk --add-workspace` writing to Vault: enrolment belongs to dotvault (`dotvault enrol`).
- `--export` and the `--dump-*` debugging commands keep reading token files only.
- Re-reading Vault while running: a token Slack rejects mid-session is handled as today (the workspace shows as disconnected); restarting slk picks up the refreshed one.

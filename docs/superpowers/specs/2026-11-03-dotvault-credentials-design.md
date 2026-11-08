# Slack credentials from Vault (dotvault) — design

## Problem

slk keeps each workspace's Slack session (the `xoxc-` token and the `d` cookie) in a plaintext JSON file under `~/.local/share/slk/tokens/`. Where a user already runs [dotvault](https://github.com/goodtune/dotvault), the session belongs in Vault: enrolled once, refreshed by the dotvault daemon, never on disk.

dotvault's `slack` enrolment engine (dotvault `docs/services/slack.md`) writes the secret; slk reads it through dotvault's public client (`github.com/goodtune/dotvault/client`). That is the whole dependency: slk imports the client, dotvault imports nothing of slk's, so the two modules never depend on each other. The secret's shape is the contract between them.

## The secret

`kv/users/<you>/<secret>` (for example `slack/slk`) holds one field per workspace, named by its Slack subdomain, whose value is a JSON object carrying the session's `d` cookie and nothing else:

```json
{"domain": "acme", "cookie": "xoxd-…"}
```

Any field that is not such an object (something written by hand) is skipped.

**slk always mints the access token; dotvault only carries the cookie.** At startup, for each field, slk mints the `xoxc` token from the cookie and domain (the same page scrape as `--add-workspace`) and identifies the team with `auth.test`, which also confirms the session belongs to that subdomain. The token lives in memory only. A field that happens to carry a token or team ID is not trusted for either: the cookie is the credential.

Before any cookie is sent, the field's `domain` must be its own field name and a Slack domain: one label (`[a-z0-9-]`, 1–63 bytes, no leading or trailing `-`), optionally followed by `.enterprise` for an Enterprise Grid organisation (`<org>.enterprise.slack.com`). A hand-written field must not be able to send the cookie to another host.

The fields may be written by dotvault's `slack` engine or by hand (one field at a time with `dotvault kv patch --force <secret> <domain>=-`, the JSON on stdin; `kv put` would replace the whole secret): slk cannot tell the two apart and does not need to, so its advice covers both. A field without a cookie, with a bad domain, or whose mint or `auth.test` fails is skipped with a warning naming the field (never a value); the other workspaces still load.

## Configuration

```toml
[vault]
secret = "slack/slk"        # path under kv/users/<you>/; empty (default) = off
# config = "/etc/xdg/dotvault/config.yaml"   # dotvault's config; default is dotvault's own default
```

Everything about reaching Vault (address, auth method, KV mount, user prefix, the token file, borrowing a token from the dotvault daemon's socket) comes from dotvault's configuration through `client.LoadConfig`, so slk reads from exactly where dotvault writes.

## Startup

1. Load the token files as today.
2. If `[vault] secret` is set: load dotvault's config, `AuthenticateCached` (environment, token file, the daemon's socket, a client certificate — never a prompt or a browser), and `ReadUserSecretFields(secret)`. Each workspace field is minted and identified into a token with `Source = "vault"`, held in memory only.
3. A Vault workspace replaces a token file for the same team ID (Vault is where the user moved it); the file is left alone, not deleted.
4. Tokens slk mints from a Vault cookie are never written: the startup re-mint from the desktop app skips them (as it skips browser tokens: the desktop app may be signed in as someone else, and saving would put the session back on disk). A fresh token is minted at every start.

Failures do not stop slk from starting with the workspaces it does have. Each is reported on stderr in words that say what to do:

| Outcome | Message |
|---|---|
| No usable token (`ErrLoginRequired`) | run `dotvault login` |
| Vault unreachable | Vault is unreachable; using the other workspaces |
| Denied | the token cannot read `<secret>` |
| Secret absent / no workspaces in it | add a field per workspace, by `dotvault enrol <secret>` or by writing `{"domain":"<domain>","cookie":"xoxd-…"}` yourself |
| Some workspaces unusable | could not use `<field>: <reason>`; the rest load (as a toast); with none usable, how to replace a session either way |

When `[vault] secret` is set, slk does not launch the first-run onboarding because the Vault read came up empty: onboarding writes a token file, which is what the user moved away from. It exits with the message instead. Without `[vault]`, startup is unchanged.

## Not in scope

- `slk --add-workspace` writing to Vault: enrolment belongs to dotvault (`dotvault enrol`).
- `--export` and the `--dump-*` debugging commands keep reading token files only.
- Re-reading Vault while running: a token Slack rejects mid-session is handled as today (the workspace shows as disconnected); restarting slk mints a new one, from a newly enrolled cookie if the old one was signed out.

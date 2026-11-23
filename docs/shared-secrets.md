# Shared secrets

ScopeWeaver can read its API tokens and webhook URLs from the shared bug bounty
secrets file instead of carrying its own copies in `.env` or the database. The
file and its name registry are the single source shared by ai-recon, MonRust,
mcp-recon and ScopeWeaver; the format and naming rules are defined in
`programs/_ops/SECRETS-STANDARD.md` in the bug bounty tree.

This is a deployment integration. It changes nothing for an upstream user who
sets keys the usual way: an explicit environment variable always wins, and when
no shared file is present every lookup returns empty and the dependent feature
simply stays off.

## The two files

| File | Holds |
| --- | --- |
| `~/.config/bugbounty/secrets.yaml` | flat `NAME: "value"` pairs: tokens, webhook URLs, tool paths |
| `~/.config/bugbounty/secrets-registry.yaml` | the registry that maps each canonical name to its legacy aliases |

Both live outside the `~/bugbounty` tree so they are never archived, and both are
read-only to ScopeWeaver. A value stored under an old alias is still found during
the migration because the loader consults the registry's `aliases` for each name.

Resolution order for a canonical name is: the canonical name in `secrets.yaml`,
then each registered alias, then empty. A missing file, an unreadable file or a
name with no value yields empty, never an error.

## Locating the files

The loader resolves the paths in this order; the first that is set wins.

| Variable | Meaning |
| --- | --- |
| `BUGBOUNTY_SECRETS_FILE` | full path of `secrets.yaml` |
| `BUGBOUNTY_SECRETS_REGISTRY` | full path of `secrets-registry.yaml` |
| `BUGBOUNTY_SECRETS_DIR` | directory holding both files |
| (default) | `~/.config/bugbounty/` |

In a container, mount the host directory read-only and point the loader at it,
for example `-v ~/.config/bugbounty:/secrets:ro` with
`BUGBOUNTY_SECRETS_DIR=/secrets`.

## What ScopeWeaver reads

| Use | Canonical name | Environment override (wins) |
| --- | --- | --- |
| Anthropic key for the env-bootstrapped LLM provider | `ANTHROPIC_API_TOKEN` | `ANTHROPIC_API_KEY` |
| OpenAI key for the env-bootstrapped LLM provider | `OPENAI_API_TOKEN` | `OPENAI_API_KEY` |
| Discord alert webhook | `DISCORD_WEBHOOK_ARTEX` | the stored channel URL (see below) |

LLM keys only seed the provider when none is configured in the database; a UI
profile still takes precedence and its key is never read from the shared file.
With neither a database profile nor a key in env or the shared file, the engine
stays idle exactly as before.

## Referencing a secret from a notification channel

A webhook notification channel can keep its real URL out of the database by
storing a reference of the form `${NAME}` instead of the URL. At send time the
loader resolves the reference from the shared file; the resolved URL still passes
the channel's normal destination validation. To route ScopeWeaver's Discord
alerts through the shared webhook, set the channel URL to
`${DISCORD_WEBHOOK_ARTEX}`. An unresolved reference is left in place so the
misconfiguration is visible in the delivery error rather than becoming empty.

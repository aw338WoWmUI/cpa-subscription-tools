# Subscription Tools for CLIProxyAPI

A native management plugin for the small gaps between subscription exports, provider plugins and quota-aware routing.

It works with an unmodified CLIProxyAPI host. It does not register an OAuth provider, executor, interceptor or scheduler, and does not change client-visible model IDs, client identity, account retry settings or session-affinity rules.

## Features

- Import native Claude Code `claudeAiOauth` and Codex `tokens` JSON, with a token-free preview and explicit confirmation. Unrelated MCP credentials are excluded. Existing imported files are never replaced.
- Query public GitHub Copilot quota using the existing Copilot plugin's credential. This plugin implements the host's standard quota-provider interface; it does not replace Copilot login or inference.
- Show Codex Reset Bank credits and expiry. Redeem a selected credit only after confirmation, with an account-bound UUID idempotency key. There is no automatic redemption job.
- Apply **Quota Pacer's own dry-run plans** through the host's credential-field API. The plugin does not copy its quota collector or scoring algorithm. Only fresh Claude/Codex priority and weight targets are applied, with explicit weight zero on confirmed depletion.

Tested host: CLIProxyAPI v8.0.30 (`5be2cdab`), native ABI 1 / RPC schema 6. Release archives cover Linux amd64/arm64 (Debian 12/glibc), macOS amd64/arm64 and Windows amd64. Each platform runs the same native-host integration in CI. Other operating systems and C runtimes are not advertised.

## Install

Download the platform zip and `checksums.txt` from this repository's Releases, verify SHA-256 and extract the dynamic library to the host's configured `plugins/<goos>/<goarch>/` directory. Keep the existing provider plugins installed.

```yaml
plugins:
  enabled: true
  configs:
    cpa-subscription-tools:
      enabled: true
      pacing_enabled: false
```

Restart the host if the library is not discovered automatically. Open **Subscription Tools** in a management client supporting plugin resources, or visit `/v0/resource/plugins/cpa-subscription-tools/status`. Enter the host management key. The page keeps it only in memory; it is not placed in URLs or browser storage.

The static HTML page is public by the host's resource design. All credential data and actions use authenticated management routes. A proxy exposing only a separate Dashboard application must also route the plugin's resource and management paths; installing the library alone does not make that page reachable there. Expose those paths over HTTPS when used outside the LAN.

### Optional quota pacing

Install Quota Pacer v1.0.19, set its `auto_apply: false`, and select native `weighted-round-robin` routing. Preserve your existing affinity configuration. Then enable this bridge:

```yaml
plugins:
  configs:
    quota-pacer:
      enabled: true
      auto_apply: false
      provider_scope: "claude|codex"
    cpa-subscription-tools:
      enabled: true
      pacing_enabled: true
      management_url: "http://127.0.0.1:8317"
      management_key_env: "MANAGEMENT_PASSWORD"
      interval_seconds: 60
```

`management_key_env` names an existing environment variable containing the management key. Do not paste its value into plugin configuration. The URL must be an HTTP loopback origin. Disable any other priority/weight writer before enabling this bridge. Quota Pacer may reuse its 15-minute evidence cache; cached-only plans leave current fields unchanged. Antigravity is deliberately excluded because model groups can have independent quotas.

Pacing is off by default. Reset is always manual. No startup inference, account model probing, credential refresh loop, deletion, API-key rewriting or automatic reset is added.

## Management API

All paths below are under `/v0/management/plugins/cpa-subscription-tools` and require the host management key.

| Method | Path | Purpose |
|---|---|---|
| GET | `/accounts` | Bounded credential summaries, without tokens |
| POST | `/import/preview` | Validate native JSON and preview the destination |
| POST | `/import` | Add a credential with `confirm: true` |
| GET | `/credits?auth_index=...` | Read Codex reset-credit inventory |
| POST | `/reset` | Redeem `credit_id` with `auth_index`, UUID `request_id`, `confirm: true` |
| GET | `/copilot-quota?auth_index=...` | Normalized public GitHub quota |
| GET/PATCH | `/routing` | Read/save native strategy, session affinity and TTL |
| GET | `/pacing` | Last bridge result |
| POST | `/pacing/run` | Apply one dry-run plan when pacing is enabled |

Copilot quota also appears through `GET /v8/management/plugins/cpa-subscription-tools/quota?auth_index=...` and the corresponding authenticated `POST` with an `auth_index`. A third-party UI that hard-codes a different quota endpoint will need to use this interface or the plugin's own page.

Manual Reset targets an explicitly selected available credit. It never retries automatically. If the network result is uncertain, refresh inventory before retrying; reuse the same request ID for the same operation. The upstream account service owns idempotency across host restarts. Synthetic tests cover duplicate/concurrent submissions; real redemption is not part of automated verification.

## Build and test

Go 1.26 and a C compiler are required.

```sh
make test build
python3 scripts/integration.py --host /path/to/upstream/CLIProxyAPI \
  --library dist/linux/amd64/cpa-subscription-tools.so
```

Tests use synthetic credentials and fake upstream responses. Native integration runs a separate unmodified host with a temporary auth directory and verifies resource registration, management authentication, preview/import, rotation preservation, redaction and reset confirmation.

No third-party plugin code is included. The C ABI adapter follows the official MIT-licensed examples; see `LICENSE` and `NOTICE`.

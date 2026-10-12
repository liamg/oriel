# P0-5 · Codex app-server billing: subscription or per-token

**Verdict: subscription.** With a ChatGPT login, `codex app-server` bills turns to the ChatGPT plan's usage
limits, the same as the Codex CLI and IDE extension. Billing follows the **auth mode**, not the surface:
`chatgpt` uses the plan, and `apiKey` is per-token API pricing. Codex keeps the `app-server` route in
`agents.md`, and no PTY fallback is needed.

Checked on 2026-10-12 against `openai/codex` at `ddb33e34b278ca1b7d16d1e68f8ad363a2042f2e` and the live docs.

## Evidence

1. **The app-server is the IDE extension's backend.** From [learn.chatgpt.com/docs/app-server](https://learn.chatgpt.com/docs/app-server):
   > Codex app-server is the interface Codex uses to power rich clients (for example, the Codex VS Code extension).
2. **The plan covers the IDE extension; an API key is per token.** From [learn.chatgpt.com/docs/pricing](https://learn.chatgpt.com/docs/pricing), Plus:
   > Codex on the web, in the CLI, in the IDE extension, and on iOS

   and the API Key column:
   > Codex in the CLI, SDK, or IDE extension … Pay for Codex usage based on API pricing

   and the FAQ:
   > When you use Codex with an API key, API pricing applies to image generation instead of included ChatGPT usage limits.
3. **The app-server has the ChatGPT auth mode and exposes the plan's limits.** From the app-server docs:
   > ChatGPT managed ( chatgpt ) - Codex owns the ChatGPT OAuth flow, persists tokens, and refreshes them automatically.

   > account/rateLimits/read - fetch ChatGPT rate limits.

   In the protocol (`codex-rs/app-server-protocol/src/protocol/v2/account.rs`), `Account` is `apiKey {}`,
   `chatgpt { email, planType }` or `amazonBedrock`, and `RateLimitSnapshot` carries `primary`/`secondary` windows,
   `credits` and `planType`.
4. **The auth mode chooses the endpoint.** In `codex-rs/model-provider-info/src/lib.rs`, `to_api_provider`
   sends ChatGPT auth to `CHATGPT_CODEX_BASE_URL = "https://chatgpt.com/backend-api/codex"` and everything else
   to `https://api.openai.com/v1`. The CLI and the app-server share this code path through `core`, so they are
   billed the same way.
5. **A stray env key doesn't switch it.** `codex-rs/app-server/src/lib.rs` builds its auth manager with
   `enable_codex_api_key_env` set to `false`, so `CODEX_API_KEY` is ignored. `OPENAI_API_KEY` is read only for
   realtime voice (`core/src/realtime_conversation.rs`) and to prefill the TUI's login screen.
   A key stored with `account/login/start` `{type: "apiKey"}` does switch it. That shows up as
   `account.type == "apiKey"`, which Oriel checks.

## What this means for Oriel

- The Codex provider runs `codex app-server` (JSON-RPC, JSONL over stdio) and **requires `account/read` →
  `account.type == "chatgpt"`** before it starts a turn. With `apiKey`, it says that use is per-token and asks first.
- Usage tats read `account/rateLimits/read` and `account/rateLimits/updated`.
- The protocol is now **v2**: `thread/start`, `turn/start`, `turn/interrupt`, `thread/fork`, and server requests
  `item/commandExecution/requestApproval` / `item/fileChange/requestApproval`. `agents.md` is updated to match.
  The v1 names (`newConversation`, `sendUserMessage`, `execCommandApproval`) only appear in compatibility tests.

## Residual risk

- **Terms.** The same docs say:
  > If you've built a local or open-source application using Codex app-server authentication, you can continue
  > using it, though we recommend migrating to Sign in with ChatGPT … App-server authentication has never been
  > permitted for commercial or hosted services.

  Oriel is a private, local, single-user app, which is the permitted case. If Oriel ever ships to other people,
  revisit [Sign in with ChatGPT](https://developers.openai.com/siwc) (in preview, by client ID) or have each user bring their own login.
- **Credits beyond the plan.** Plus/Pro can "Flexibly extend usage with ChatGPT credits", and Business plans have
  workspace credits. Past the limits, usage may draw on credits the account holds. The usage tat shows
  `credits` and `rateLimitReachedType`, so this stays visible.
- **Not run live.** There is no ChatGPT login in the sandbox. The confirm script below closes this on Liam's account.

## Confirm on your machine

With Codex installed and `codex login` done with ChatGPT:

```sh
python3 _design/spikes/P0-5-codex-confirm.py
```

It drops `OPENAI_API_KEY`, `CODEX_API_KEY` and `CODEX_ACCESS_TOKEN` from the environment. Then it starts `codex app-server`,
checks that `account/read` reports `chatgpt`, prints the plan's rate limits, runs one tiny read-only turn, and prints the limits again.
To pass, the account must be `chatgpt`, `usedPercent` must not go down (it often stays at the same rounded value after one turn),
and the [API usage page](https://platform.openai.com/usage) must show no new spend.

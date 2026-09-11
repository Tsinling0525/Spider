# Local Email Reply Refiner integration

The existing app `e29c4e9c-e2a9-49d7-92e6-53bd889482d0` is a Chatflow,
not a Workflow. Spider selects `/v1/chat-messages` when `DIFY_MODE=chatflow`;
the default `workflow` mode continues to use `/v1/workflows/run`.

Configure Spider only:

```dotenv
DIFY_BASE_URL=http://localhost/v1
DIFY_MODE=chatflow
DIFY_API_KEY=<the app's secret key>
DIFY_USER=spider-mail-service
```

The local key belongs in the ignored `.env` file. Never copy it into Mantle's
frontend or the exported workflow snapshots.

The Chatflow takes `email_from`, `email_subject`, and `email_body` (a bounded
JSON thread). Guidance, language, tone, signature, and the current edited draft
go into `query`. Each Spider generation starts a fresh Dify conversation and
explicitly supplies the current draft, avoiding conversation mixing across
threads or losing manual edits. The Dify web app still supports native multi-turn
refinement through its memory window.

Its output is strict JSON: `should_reply`, `subject`, `body_text`, `confidence`,
`warnings`, and `needs_human_input`. Enable reasoning-tag separation on the
DeepSeek node: otherwise API `answer` contains `<think>` before JSON even though
the Dify web app visually separates it. Spider rejects malformed output and
clears any body when `should_reply=false`.

Before/after graph and feature snapshots are saved beside this document. They
contain no credentials. The original graph is preserved in
`email-reply-refiner.before.json`; the updated published graph is in
`email-reply-refiner.after.json`.

## Verification (2026-09-11)

- Local Dify web app: first draft and a subsequent one-sentence refinement succeeded.
- Four real API test cases passed: normal reply, refinement retaining a manual
  room-name edit, newsletter/no reply, and unverified payment with an embedded
  malicious instruction. The final payment test produced warnings, requested
  human input, and made no payment or follow-up commitment.
- Spider `/v1/email/drafts`: generated a reply from the Gmail Dify welcome thread.
- Mantle browser: opened that email, generated a draft, and displayed editable
  subject/body plus warnings. A subsequent refinement retained the manually
  edited mention of the template guide. Review send displayed the final text;
  Keep editing returned to the composer without invoking send.
- No Gmail send endpoint was invoked during these checks.

Run deterministic tests with `go test ./...`. To deliberately invoke the live
local model using synthetic mail:

```sh
set -a
source .env
set +a
SPIDER_TEST_LOCAL_DIFY=1 go test ./internal/dify -run TestLocalRefinerIntegration -v -count=1
```

Model tests make real inference calls and may have provider costs. The graph
contains only input, LLM, and answer nodes; Gmail delivery remains in Spider
after frontend user confirmation.

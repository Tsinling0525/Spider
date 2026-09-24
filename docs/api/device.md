# Mantle ESP32-S3 device service

`cmd/spider-device` is an opt-in Spider service, separate from the existing mail
and lifed listeners. Default address is `127.0.0.1:8083`. It implements a compact
Mantle surface node endpoint and a half-duplex voice extension. It does not
execute model-proposed actions or automatically approve Dify human tasks.

## Configuration

Requires Go 1.24+, FFmpeg with libopus for the real Dify provider, and a dedicated
Dify Chatflow with STT and TTS enabled. Do not reuse the email-refiner application
for general conversation.

Create a private JSON array at `data/device/bindings.json`:

```json
[{"node_id":"node:esp32","principal_id":"principal:owner","token":"REPLACE_WITH_A_RANDOM_DEVICE_TOKEN_AT_LEAST_24_CHARS"}]
```

Environment:

| Variable | Meaning |
| --- | --- |
| `SPIDER_DEVICE_LISTEN` | Listener; use `0.0.0.0:8083` for LAN devices |
| `SPIDER_DEVICE_BINDINGS` | Binding file, default `./data/device/bindings.json` |
| `SPIDER_DEVICE_STORE` | Durable approval store, default `./data/device/approvals.json` |
| `SPIDER_DEVICE_ADMIN_TOKEN` | Separate admin token, at least 24 characters |
| `SPIDER_VOICE_DIFY_BASE_URL` | Dedicated voice Chatflow API base, default `http://localhost/v1` |
| `SPIDER_VOICE_DIFY_API_KEY` | Server-only Dify application credential |
| `SPIDER_DEVICE_DIAGNOSTIC` | Explicit `1`: echo recorded audio, show diagnostic text, no AI |

```sh
go run ./cmd/spider-device
```

Use a TLS reverse proxy and `wss://` outside a trusted development LAN. Tokens
are HTTP Authorization headers, never URL query parameters. A device token binds
one node and principal; it cannot create approvals or impersonate another node.
Browser-origin WebSocket requests are rejected. Only one active connection per
node is permitted. Use different device and admin tokens.

## WebSocket `/v1/device/ws`

Authenticate using `Authorization: Bearer <device-token>`, then send
`node.enroll.v0`. The core enrollment, compact Focus definition, ruling and ruling
receipt shapes follow the sibling `mantle-node` schemas. This service currently
projects only null-situation Focus cards. Reconnect always produces a fresh
projection; no unsubstantiated cursors are sent or accepted.

Spider's additive idle heartbeat is `{"type":"node.device.ping.v1"}` every
15 seconds, answered by `node.device.pong.v1`. This application frame keeps the
90-second receive timeout alive even when the WebSocket library consumes RFC
Ping/Pong internally. Ordinary WebSocket Ping/Pong is also supported.

After successful enrollment the service grants `node.voice.observation.v1` with
`state: granted` and a random uint32 `surface_hash`. Capture requires a current
grant plus `node.voice.capture_state.v1`, `capture_enabled: true`, and
`held_by: ["observation_lease"]`. Closing capture submits the bounded utterance.

Audio is mono Opus, 16 kHz, 60 ms per packet. Device to server: `OBS1` followed by
little-endian uint32 grant hash and sequence, then one Opus packet. Sequence
starts at zero for a grant and advances across captures. The server rejects
wrong attribution, gaps, messages over 1512 bytes and utterances over 20 seconds.
Control messages are limited to 32768 bytes. Opus is repackaged as Ogg and
converted to WAV before Dify `/audio-to-text`; `/chat-messages` performs inference;
`/text-to-audio` returns speech, converted to paced 60 ms Opus packets.

The text replies use `node.converse.reply.v0`, unique `message_id`, null
`situation_id`, explicit `role` (`user` / `assistant`), and `delivery: live`.

Before response audio the service emits:

```json
{"type":"node.voice.speech.v1","state":"start","turn_ref":"turn:unique","turn_hash":123,"sample_rate":16000,"segment_index":0}
```

**Spider's explicit `turn_hash` is an additive requirement for this firmware**;
it must match every `AVT1` packet's little-endian hash. Audio sequence starts at
zero for the single response segment. End is `node.voice.speech.v1` with
`state: end`, the same `turn_ref`, and `segment_index: 0`. A device acknowledges
only a completely rendered segment using `node.voice.output_receipt.v1`, the
same `turn_ref` and `played_through: 0`. No new capture is accepted until receipt
or cancellation. Receipt is renderer feedback, not evidence the human heard it.

Spider additionally accepts device-to-server `{"type":"node.voice.cancel.v1"}`
for explicit local cancellation. It cancels in-flight inference/playback, drops
late results, rotates the observation grant and resets its sequence. This
half-duplex cancellation extension is not claimed to be part of a separately
published Mantle voice specification. The original pinned box does not contain
that specification, so other Mantle voice servers require conformance review.

The device detects utterance completion with local VAD and a short silence
window, supports local wake word or BOOT, and returns to standby on silence.
Conversation IDs remain server-side for the active connection. A reconnect
starts a new conversation; cross-session memory is not implemented here.

## Confirmation cards

Admin-token protected `POST /v1/device/approvals` accepts:

```json
{
  "id":"approval:example", "node_id":"node:esp32", "principal_id":"principal:owner",
  "text":"Proceed with the operation?",
  "options":[{"id":"approve","label":"确认"},{"id":"deny","label":"取消"}],
  "expires_at":"2099-01-01T00:00:00Z"
}
```

Replace expiry with a time within the next 24 hours. IDs cannot be overwritten.
The server owns revision `1` and ignores supplied choice on creation. Each
connection presents one live card at a time. The device submits `action: choose`
with the selected bounded option, exact decision identity and revision. The
service persists the first accepted choice atomically before acknowledging it.
Expired, foreign, stale, duplicate and unoffered selections are rejected.
`GET /v1/device/approvals/{id}` (admin token) returns the persisted outcome.

**An accepted ruling records the person's choice only.** A Spider capability
must read the result and apply its own idempotent execution policy. No endpoint
in this service executes arbitrary commands, sends email, or purchases anything.

## Validation

```sh
go test -race ./internal/device ./cmd/spider-device
```

Tests cover authentication, identity binding, audio grant/sequence, real FFmpeg
Opus conversion, Dify's three HTTP endpoints against a local fake, text delivery,
response attribution, receipt, cancellation and durable bounded decisions.
Diagnostic mode is suitable for physical microphone/speaker loopback tests;
it is not evidence that Dify or live speech recognition works.

## Local board bring-up (2026-09-24)

The development ESP32-S3 is configured for `ws://192.168.0.155:8083/v1/device/ws`
as `node:esp32` / `principal:owner`. The address must be updated if the computer's
LAN address changes. Private tokens and local environment are in ignored
`data/device/` files; they are not example credentials to distribute.

Start the service from this checkout with `./scripts/run-device-local.sh`.
The current private environment explicitly enables diagnostic loopback. The
service has passed its Go race tests, including an idle control-Ping regression,
and the flashed physical board has enrolled successfully.
After the heartbeat fix, the physical connection remained up for over two
minutes. Restarting this service at 15:16:14 AEST led to automatic re-enrollment
at 15:16:16, without pressing reset or reflashing the board.

Physical acceptance steps:

1. On the board, select an option on the “设备联调测试” card. Verify that the
   admin GET endpoint returns that exact persisted choice. Expired cards need a
   fresh ID and expiry via the POST endpoint.
2. Say “你好小智” or press BOOT, speak a sentence, then pause. Check diagnostic
   text and that the speaker plays the original recording. The service logs the
   number of captured packets and the firmware's playback receipt.
3. Cancel capture/playback with BOOT, then start a fresh utterance. Restart the
   local service and verify re-enrollment and pending card recovery.

Touch, audible playback and acoustic wake-word quality still require human
confirmation. Dify STT, inference and TTS have only been checked against a local
HTTP test double; no live voice application has been configured yet.

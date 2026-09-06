# How devices and the server talk

The one fact everything else follows from: **a tablet sits behind a school's
NAT, so nothing can connect to it.** Every connection is opened by the device.
The server has no way to reach a tablet and never tries.

That is why the system is built around polling, and why the fast path added on
top of it is careful never to become load-bearing.

## The transport

One HTTP/2 connection per tablet, TLS, terminated at the reverse proxy and
forwarded to the Go API on `127.0.0.1:8080`.

HTTP/2 matters here for one reason: multiplexing. A 700MB package download and a
heartbeat share the connection instead of queueing behind one another. Under
HTTP/1.1 the client either serialises them or opens extra connections, and a
large download visibly delayed everything behind it.

Caddy does this by default. nginx needs it asked for — see the README.

## Authentication

| Who | Credential | Where it comes from |
|---|---|---|
| Device | Opaque API key, `Authorization: Bearer` | Issued at enrolment; stored SHA-256-hashed server-side, in the Android keychain on the device |
| Operator | JWT | Console sign-in |
| Enrolment | Pre-shared enrol token | `ALIMDM_ENROLL_TOKEN`, from the QR or the ADB script |

The enrol endpoint is the only device route that is not authenticated by an API
key, because it is the one that issues them.

## Enrolment, once

```
POST /api/v1/devices/enroll        {token, device_info}
                                -> {device_id, api_key, organization_name}
```

Everything afterwards carries that key.

## The two clocks

A tablet finds out there is work in one of two ways, and **both end in the same
place**: it sends a heartbeat.

**The timer.** Every 30 seconds, unconditionally.

**The wake stream.** The device holds open

```
GET /api/v1/devices/{id}/events         text/event-stream
```

and the server writes `event: wake` whenever work is queued for it. The notice
is deliberately empty — it carries no instructions, no payload, no identifier.
The device's only response is to heartbeat immediately, which is what the timer
would have done anyway.

That is the whole safety property. A stream that drops, stalls, is blocked by a
school firewall or never connects at all costs latency and nothing else. Nothing
is tracked on it, nothing is retried on it, and there is no delivery state that
can get out of step. Measured on a real tablet: **0.6–0.9s with the stream,
about 27s without**.

Three details that are not incidental:

- The response carries `X-Accel-Buffering: no`. A buffering proxy would
  otherwise hold each notice until a buffer filled, and that failure appears
  only in production.
- The server closes the connection after **5 minutes** and the device
  reconnects. A socket held for hours is pinned to whatever DNS said when it
  opened — which is exactly why tablets kept talking to the old server after
  its address moved, and only followed after a reboot.
- The device abandons a connection that has been silent for **70 seconds**,
  against a 20-second server keepalive. A half-open socket after a network
  change otherwise sits there looking perfectly healthy.

The console shows which mode a device is in, per device: *Land at once* against
*Next check-in*.

## The heartbeat

```
POST /api/v1/devices/{id}/heartbeat
     {telemetry, config, config_version, ...}
  -> {status, pending_commands, sync_action: none|apply, config, ...}
```

The device reports battery, wifi, Android version, its Ali MDM build and the
hash of the config it last applied. The reply says what is waiting, and carries
a new policy when the group's hash no longer matches the device's.

## Collecting work

Each of these hands over what is pending and marks it claimed:

```
GET  /api/v1/devices/{id}/commands     queued commands
GET  /api/v1/devices/{id}/updates      app installs, with download urls
GET  /api/v1/devices/{id}/files        file deliveries, 25 at a time
```

File deliveries are batched deliberately. The device downloads them one after
another and only reports as each finishes, so everything it has been handed is
at risk for as long as the batch takes; a folder of 200 tracks handed over at
once meant one interrupted download stranded every file behind it. A claim that
goes unreported for 30 minutes is offered again.

## Fetching bytes

Ordinary requests, deliberately not on the wake stream:

```
GET /api/v1/apk/{name}                 a package
GET /api/v1/apk/{name}/{part}          one APK of a split package
GET /api/v1/files/{name}/download      a document or audio file
GET /api/v1/agent/apk                  Ali MDM's own build, for self-update
```

Keeping bulk transfer on plain HTTP is a design decision, not an omission. These
payloads run to hundreds of megabytes — one real package was 771MB across four
APKs. On ordinary requests they get `Range` resumption, per-request timeouts and
independent backpressure. Framed down a control channel they would need manual
chunking, would restart from zero on a drop, and would block every other message
behind them.

## Reporting back

```
POST /api/v1/commands/{id}/result              what a command did
POST /api/v1/devices/{id}/files/{name}/result  whether a file was saved
POST /api/v1/devices/{id}/inbox                what is in the inbox folder
POST /api/v1/devices/{id}/apps                 what is installed
POST /api/v1/devices/{id}/agent-update         how a self-update went
POST /api/v1/devices/{id}/screenshot           a single frame
POST /api/v1/devices/{id}/stream/frame         live view, several a second
POST /api/v1/devices/{id}/unenroll             leaving management
```

## Live view and remote control

The same shape as everything else, which surprises people. The tablet uploads
frames a few times a second, and an operator's taps ride back **in the reply to
each upload**. There is no separate control channel.

That is why sending input without a stream running returns
`409 start live view first`: with no frame upload in flight, there is no reply
to put a tap in.

## What the server never does

There is no server-initiated anything. `PokeQueue.Enqueue` sounds like a push
and is not: it writes a note into memory that the next heartbeat collects, and —
since the wake stream — nudges any open stream to say that a note exists.

Notifying from inside `Enqueue` rather than at each call site is why commands,
file pushes, installs, log requests and anything added later all wake the device
without a caller having to remember to.

## Latency, end to end

| | Command reaches the device |
|---|---|
| Wake stream connected | under a second |
| Stream down, timer only | up to 30 seconds |
| Device offline | its next check-in, whenever that is |

Nothing is lost in the last case. Work waits in the queue and is collected in
order whenever the tablet can talk again.

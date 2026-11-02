# Deep Diagnosis — Phase 2 Design (full)

Date: 2026-11-02
Status: draft, awaiting approval
Scope: Phase 2 only — through-tunnel probe lab. Phase 1 shipped (passive surfacing). Phase 3 (pcap forensics) is a follow-up.

## Intent / Why

Operator opens the panel when something feels wrong. Phase 1 shows the
tunnel is sick (KCP variance, queue pressure, conn history) but can't
answer whether the path or the peer is at fault, nor how fast the tunnel
actually goes right now. Today `PingSummary` / `DumpCapture` are stubs
returning `"not wired"` (`internal/diag/diag.go:199-214`) and the
handlers return 501 (`internal/metrics/diag_handlers.go:159-164`).

Phase 2 adds bounded active probes so the operator can dial a suspect
upstream, echo UDP, check MTU assumptions, and run a 10s speedtest with
KCP counters sampled during the run — then compare before/after a preset
change without leaving the Diag tab.

## Goals

- `POST /api/diag/dial {host, port, timeout_sec}` — TCP dial timing from
  the daemon host to an **arbitrary user-supplied host:port**. Returns
  `{host, port, ok, connect_ms, error}`. Works to any host (SYN/SYN-ACK
  timing); primary reachability answer.
- `POST /api/diag/udp {host, port, timeout_sec, bytes}` — UDP nonce send
  to arbitrary host:port, waits for any reply. Returns
  `{ok, rtt_ms, error, note}`; timeout reports "no echo — expected
  unless peer echoes" rather than failure. Useful alongside firewall
  check.
- `POST /api/diag/mtu {host, port}` — reports local interface MTU +
  configured KCP MTU + best-effort UDP size sweep when the peer echoes;
  otherwise returns configured values with guidance. No raw sockets.
- `POST /api/diag/speedtest {duration_sec<=10, mode}` — bounded
  throughput job measuring **whether the live tunnel passes traffic and
  how fast**: daemon transfers for `duration_sec` against the tunnel's
  current peer (no target param — partner decision 2026-11-02: keep it
  simple, connection-made + performance, no peer/target selector).
  Samples `SnapshotNow()` KCP/transport counters before/after
  (`retransmits, lost_segs, fec_recovered, bytes_in/out`).
  Returns `{duration_sec, bytes_up, bytes_down, goodput_mbps, kcp_delta}`.
  If no active KCP session, returns `409 {error: "no active tunnel"}`
  instead of fake numbers. Single-flight globally; client disconnect
  aborts.
- Result history: in-memory last-20 probe results (ring, no disk),
  `GET /api/diag/results`, rendered in Diag tab with user tag
  ("before"/"after preset X") to compare.
- Diag tab probe-lab panel: host/port inputs, Dial / UDP / MTU /
  Speedtest buttons, result table + history list, `textContent`-only
  rendering, `Cache-Control: no-store`, existing Bearer `api()` wrapper.

## Non-goals

- No pcap wiring, no dump viewer, no log-correlated timeline, no export
  bundle — that's Phase 3.
- No traceroute / raw-socket ping / privileged exec.
- No persisted history (no disk writes); no alert-rule editing; no
  cross-node client/server correlation.
- No UDP/MTU guarantees against non-cooperating peers (arbitrary hosts
  won't echo; doc reports that plainly).

## Anti-goals

- No unbounded jobs: every probe has a server-side cap
  (dial/udp ≤10s, speedtest ≤10s + 2s grace, mtu sweep ≤10s total).
- No concurrent speedtests: single-flight; dial bursts capped (4
  concurrent); everything aborts on `r.Context().Done()`.
- No new background loops in the daemon; no heavier status polling.

## Constraints

- Trust model (user decisions 2026-11-02): arbitrary host:port allowed,
  **including loopback / RFC1918 / link-local / cloud metadata**
  (partner: "why not" — no denylist). Justification: panel token holders
  can already `PUT /api/config` with arbitrary forward targets, so dial
  scope matches existing trust; blocking LAN/loopback would kill the
  home-lab probing case.
  Justification: panel token holders can already `PUT /api/config` with
  arbitrary forward targets, so dial scope matches existing trust.
  Mitigations: `requireAuth` on all routes, `MaxBytesReader` (4–8KB),
  host non-empty ≤253 chars, port 1–65535, timeout caps, `flog.Auditf`
  every probe with remote IP + params, no response body reflection into
  shell.
- Reuse `runDiagWithTimeout`, `writeDiagJSON` patterns in
  `internal/metrics/diag_handlers.go`; `Cache-Control: no-store` on all
  JSON; Bearer auth via existing wrapper; `/` stays unauthenticated.
- Read KCP/transport baselines via `SnapshotNow()`; tolerate missing
  fields as Phase 1 does.
- Tests: `go test ./...` green, `go vet` clean; route tests pin new DOM
  IDs + `textContent` (no `innerHTML`) as Phase 1 did.

## Approaches (considered)

1. **Outward unprivileged lab (fast, host-path only).** All probes dial
   out from the daemon with unprivileged syscalls. Tradeoff: zero priv,
   no core changes — but measures the host path, not the tunnel.
2. **Through-tunnel lab (chosen).** Dial/UDP/MTU run outward to
   arbitrary host:port (answers reachability); speedtest runs through
   the tunnel path with KCP counter deltas (answers tunnel health);
   history enables before/after preset comparison. Tradeoff: proves the
   tunnel, but needs a job manager + audit + UI history.
3. **Minimal dial + speedtest slice.** Only dial + speedtest, defer
   UDP/MTU. Tradeoff: smallest review, but leaves MTU/UDP unanswered.

Picked phased approach 2 per partner choice (B, 2026-11-02) because the
real pain is "tunnel slow vs path slow" and the config-edit trust model
already permits arbitrary targets.

## What was decided

- Through-tunnel lab (approach B).
- Arbitrary user-supplied host:port in scope (not allow-listed), no
  denylist — loopback/RFC1918/metadata allowed, covered by auth + audit.
- Speedtest = live tunnel to current peer, no target/peer selector; 409
  "no active tunnel" when idle (partner: connection-made + performance,
  simplest path).
- In-memory last-20 history with before/after tags, no disk.
- Full design doc (project size); builder check done 2026-11-02.

## What's left to the builder

- Exact JSON field names + DOM IDs + route-test pins (follow Phase 1
  `dashboard_routes_test.go` naming: `id="probeHost"`,
  `id="probeResults"`, `renderProbe`, etc. — builder proposes).
- Job-manager shape (mutex + in-flight flag vs channel semaphore;
  history ring type + `GET /api/diag/results` shape) — builder's call,
  keep it in `internal/diag` + thin handlers.
- UDP echo payload (nonce length default, e.g. 32B, cap 1200B) and MTU
  sweep steps (e.g. 1200/1350/1400/1472) — builder proposes, capped.
- Speedtest traffic source (loopback through forward target vs
  daemon-local memory pipe + counter sampling cadence, e.g. 500ms) —
  builder picks cheapest that still exercises KCP accounting; when idle
  report `409 no active tunnel`, never fake numbers.
- Agreed contracts (builder-check resolutions, partner-approved):
  speedtest busy → `409 {error, retry_after_sec}`, dial burst over 4 →
  `429 + Retry-After`; `mode` in `up|down|bidir` default `up`;
  UDP counts only replies from probed host:port; dial/UDP default 5s,
  speedtest default 10s, over-cap clamps (matches ping handler).
- History tag UX (free-text label input vs before/after toggle) —
  builder's call, keep `textContent`-only.

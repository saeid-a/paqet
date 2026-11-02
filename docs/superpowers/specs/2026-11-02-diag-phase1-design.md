# Deep Diagnosis — Phase 1 Design (short)

Date: 2026-11-02
Status: draft, awaiting approval
Scope: Phase 1 only — passive surfacing. Phases 2–3 are follow-ups.

## Intent / Why

Operator opens the paqet panel when something feels wrong and currently
has to SSH anyway: Overview shows throughput + a minimal KCP card
(rtt/rto/cwnd + retx/fec), Conns shows one current RTT number, and
Diag ping/dump are stubs returning "not wired". The daemon already
collects full KCP detail, transport queue pressure, and per-target
counters in every `/api/status` snapshot, but the UI hides them.
Phase 1 answers "is the tunnel itself sick, and where" with zero new
privileges or probes, so Phase 2 (active probes) and Phase 3 (pcap
forensics) build on visible baselines.

## Goals

- KCP health panel: surface all `KCPStats` already in snapshot —
  `srtt_var_ms`, `lost_segs`, `repeat_segs`, `fast_retrans_segs`,
  `early_retrans_segs`, `fec_recovered/errs/parity`, `in_segs/out_segs`
  — with per-minute deltas reusing the existing 10s sliding-window
  helper.
- Transport pressure panel: surface `TransportStats` already in
  snapshot — `snd_queue/rcv_queue/snd_buf/num_streams/num_conns`,
  `target_fill/cap`, `retrans_segs/lost_segs/fec_recovered` — with a
  plain saturation hint (e.g. queue rising + cwnd flat = congestion
  suspect).
- Connection deep view: expand the existing clickable-row modal to show
  per-conn RTT + in/out rate history over the current 120s window,
  plus lifetime totals and `active_streams`; keep tombstones for
  recently-closed rows.
- Keep existing behavior: 2s poll, 120s charts, dark/light themes,
  mobile single-column, CSV exports untouched.

## Non-goals

- No new API endpoints in Phase 1.
- No active probes (TCP dial, UDP echo, MTU, speedtest, traceroute) —
  that's Phase 2.
- No pcap wiring, no dump viewer, no log-correlated timeline, no export
  bundle — that's Phase 3.
- No alert-rule editing, no cross-node client/server correlation.

## Anti-goals

- No raw sockets, no privileged exec, no new background loops in the
  daemon.
- No heavier polling or unbounded history buffers; reuse `MAX=60`,
  `WINDOW_MS=10000` patterns already in `dashboard.html`.
- No breaking `dashboard_routes_test.go`, `transport_stats_test.go`,
  or existing CSV/SSE behavior.

## Constraints

- Read-only against `/api/status` + `/api/runtime` shapes as of
  `SnapshotNow()`; tolerate missing fields like the current
  `pick()`/`fmtMs` helpers do.
- All Phase 1 work lands in `internal/metrics/dashboard.html` (+ tests);
  sequential edits only — shared file.
- Must degrade to "—" when daemon hasn't pushed KCP/transport samples
  yet (first-sample-zero case).
- Bearer auth via existing `api()` wrapper; `/` stays unauthenticated.

## Approaches (considered)

1. **Surface-what-you-have (chosen for Phase 1).** UI-only, no new
   probes. Tradeoff: answers passive symptoms immediately, zero
   priv risk, but can't prove reachability or true path speed.
2. **Probe lab (Phase 2).** Bounded `POST /api/diag/*` jobs — TCP dial
   timing, UDP echo, MTU probe, 10s speedtest with KCP counters during
   run, result history. Tradeoff: real connectability + performance
   answers, but new backend, timeouts, concurrency limits, audit.
3. **Full-path forensics (Phase 3).** Wire `DumpCapture` to real pcap,
   packet table, log-correlated spike timeline, one-click support
   bundle. Tradeoff: best for intermittents, but heaviest + most
   priv-sensitive.

Picked phased 1 → 2 → 3 because each slice ships standalone and Phase 1
creates the baselines Phases 2–3 compare against. Partner confirmed
phased order on 2026-11-02.

## What was decided

- Phase 1 first, passive only, no backend.
- Phase 2 next: minimal dial + speedtest lab.
- Phase 3 last: pcap + timeline + export.

## What's left to the builder

- Exact panel placement (extend Overview KCP card vs new panels),
  delta math reuse (`winRate` vs new per-field windows), history length
  for conn modal sparklines, saturation-hint thresholds copy, and DOM
  IDs + route tests to add in `dashboard_routes_test.go`.

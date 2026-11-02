package metrics

import (
	"encoding/json"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Snapshot struct {
	Role              string         `json:"role"`
	Timestamp         string         `json:"timestamp"`
	UptimeSeconds     int64          `json:"uptime_seconds"`
	BytesIn           uint64         `json:"bytes_in"`
	BytesOut          uint64         `json:"bytes_out"`
	PacketsIn         uint64         `json:"packets_in"`
	PacketsOut        uint64         `json:"packets_out"`
	BytesInPerSec     float64        `json:"bytes_in_per_sec"`
	BytesOutPerSec    float64        `json:"bytes_out_per_sec"`
	ActiveConnections int64          `json:"active_connections"`
	ActiveStreams     int64          `json:"active_streams"`
	SuccessTotal      uint64         `json:"success_total"`
	FailureTotal      uint64         `json:"failure_total"`
	SuccessTCP        uint64         `json:"success_tcp"`
	FailureTCP        uint64         `json:"failure_tcp"`
	SuccessUDP        uint64         `json:"success_udp"`
	FailureUDP        uint64         `json:"failure_udp"`
	SuccessSOCKS      uint64         `json:"success_socks"`
	FailureSOCKS      uint64         `json:"failure_socks"`
	SuccessFwdTCP     uint64         `json:"success_forward_tcp"`
	FailureFwdTCP     uint64         `json:"failure_forward_tcp"`
	SuccessFwdUDP     uint64         `json:"success_forward_udp"`
	FailureFwdUDP     uint64         `json:"failure_forward_udp"`
	KCP               KCPStats       `json:"kcp"`
	Runtime           RuntimeStats   `json:"runtime"`
	Targets           []TargetStat   `json:"targets"`
	Connections       []ConnSnapshot `json:"connections"`
	Transport         TransportStats `json:"transport"`
}

type KCPStats struct {
	RTTMs            int64  `json:"rtt_ms"`
	RTOMs            int64  `json:"rto_ms"`
	Cwnd             int64  `json:"cwnd"`
	Retransmits      uint64 `json:"retransmits"`
	FECShards        int64  `json:"fec_shards"`
	SRTTVarMs        int64  `json:"srtt_var_ms"`
	LostSegs         uint64 `json:"lost_segs"`
	RepeatSegs       uint64 `json:"repeat_segs"`
	FastRetransSegs  uint64 `json:"fast_retrans_segs"`
	EarlyRetransSegs uint64 `json:"early_retrans_segs"`
	FECRecovered     uint64 `json:"fec_recovered"`
	FECErrs          uint64 `json:"fec_errs"`
	FECParityShards  uint64 `json:"fec_parity_shards"`
	InSegs           uint64 `json:"in_segs"`
	OutSegs          uint64 `json:"out_segs"`
}

type TransportStats struct {
	SndQueue       int64  `json:"snd_queue"`
	RcvQueue       int64  `json:"rcv_queue"`
	SndBuf         int64  `json:"snd_buf"`
	NumStreams     int64  `json:"num_streams"`
	NumConns       int64  `json:"num_conns"`
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	SysBytes       uint64 `json:"sys_bytes"`
	GCCycles       uint64 `json:"gc_cycles"`
	TargetFill     int    `json:"target_fill"`
	TargetCap      int    `json:"target_cap"`
	RetransSegs    uint64 `json:"retrans_segs"`
	LostSegs       uint64 `json:"lost_segs"`
	FECRecovered   uint64 `json:"fec_recovered"`
}

type RuntimeStats struct {
	Goroutines    int    `json:"goroutines"`
	MemAllocBytes uint64 `json:"mem_alloc_bytes"`
	MemSysBytes   uint64 `json:"mem_sys_bytes"`
}

type TargetStat struct {
	Target   string `json:"target"`
	BytesIn  uint64 `json:"bytes_in"`
	BytesOut uint64 `json:"bytes_out"`
	Success  uint64 `json:"success"`
	Failure  uint64 `json:"failure"`
}

type ConnSnapshot struct {
	ID             uint64  `json:"id"`
	RemoteAddr     string  `json:"remote_addr"`
	LocalAddr      string  `json:"local_addr"`
	StartedAt      string  `json:"started_at"`
	LastSeen       string  `json:"last_seen"`
	Status         string  `json:"status"`
	ClosedAt       string  `json:"closed_at"`
	UptimeSeconds  int64   `json:"uptime_seconds"`
	BytesIn        uint64  `json:"bytes_in"`
	BytesOut       uint64  `json:"bytes_out"`
	BytesInPerSec  float64 `json:"bytes_in_per_sec"`
	BytesOutPerSec float64 `json:"bytes_out_per_sec"`
	ActiveStreams  int64   `json:"active_streams"`
	RttMs          int64   `json:"rtt_ms,omitempty"`
}

var startTime = time.Now()

var ingressBytes uint64
var egressBytes uint64
var ingressPackets uint64
var egressPackets uint64
var activeConnections int64
var activeStreams int64
var successTotal uint64
var failureTotal uint64
var successTCP uint64
var failureTCP uint64
var successUDP uint64
var failureUDP uint64
var successSOCKS uint64
var failureSOCKS uint64
var successFwdTCP uint64
var failureFwdTCP uint64
var successFwdUDP uint64
var failureFwdUDP uint64

var role atomic.Value

type connStats struct {
	id            uint64
	remoteAddr    string
	localAddr     string
	startedAt     time.Time
	lastSeen      atomic.Value
	closedAt      atomic.Value
	bytesIn       uint64
	bytesOut      uint64
	activeStreams int64
	rttMs         int64
}

var connID uint64
var connMu sync.RWMutex
var conns = make(map[uint64]*connStats)

const connectionRetention = 5 * time.Minute

func SetRole(r string) {
	role.Store(r)
}

func AddIngress(bytes int, packets int) {
	if bytes > 0 {
		atomic.AddUint64(&ingressBytes, uint64(bytes))
	}
	if packets > 0 {
		atomic.AddUint64(&ingressPackets, uint64(packets))
	}
}

func AddEgress(bytes int, packets int) {
	if bytes > 0 {
		atomic.AddUint64(&egressBytes, uint64(bytes))
	}
	if packets > 0 {
		atomic.AddUint64(&egressPackets, uint64(packets))
	}
}

func IncConnections() {
	atomic.AddInt64(&activeConnections, 1)
}

func DecConnections() {
	atomic.AddInt64(&activeConnections, -1)
}

func IncStreams() {
	atomic.AddInt64(&activeStreams, 1)
}

func DecStreams() {
	atomic.AddInt64(&activeStreams, -1)
}

func IncSuccess() {
	atomic.AddUint64(&successTotal, 1)
}

func IncFailure() {
	atomic.AddUint64(&failureTotal, 1)
}

func IncSuccessTCP() {
	atomic.AddUint64(&successTCP, 1)
}

func IncFailureTCP() {
	atomic.AddUint64(&failureTCP, 1)
}

func IncSuccessUDP() {
	atomic.AddUint64(&successUDP, 1)
}

func IncFailureUDP() {
	atomic.AddUint64(&failureUDP, 1)
}

func IncSuccessSOCKS() {
	atomic.AddUint64(&successSOCKS, 1)
}

func IncFailureSOCKS() {
	atomic.AddUint64(&failureSOCKS, 1)
}

func IncSuccessForwardTCP() {
	atomic.AddUint64(&successFwdTCP, 1)
}

func IncFailureForwardTCP() {
	atomic.AddUint64(&failureFwdTCP, 1)
}

func IncSuccessForwardUDP() {
	atomic.AddUint64(&successFwdUDP, 1)
}

func IncFailureForwardUDP() {
	atomic.AddUint64(&failureFwdUDP, 1)
}

// ---- KCP transport stats ----
// Live values are pushed by internal/tnet/kcp StartStatsLoop via the
// setters below (DefaultSnmp.Copy + GetSRTT/GetRTO averages). Until the
// first sample they report 0, which the dashboard renders as "—".

var kcpRTTMs int64
var kcpRTOMs int64
var kcpCwnd int64
var kcpRetransmits uint64
var kcpFECShards int64
var kcpSRTTVarMs int64
var kcpLostSegs uint64
var kcpRepeatSegs uint64
var kcpFastRetransSegs uint64
var kcpEarlyRetransSegs uint64
var kcpFECRecovered uint64
var kcpFECErrs uint64
var kcpFECParityShards uint64
var kcpInSegs uint64
var kcpOutSegs uint64

var trSndQueue int64
var trRcvQueue int64
var trSndBuf int64
var trNumStreams int64
var trNumConns int64
var trHeapAlloc uint64
var trSys uint64
var trGCCycles uint64
var trTargetFill int64
var trRetransSegs uint64
var trLostSegs uint64
var trFECRecovered uint64

func SetKCPStats(rtt, rto, cwnd int64) {
	atomic.StoreInt64(&kcpRTTMs, rtt)
	atomic.StoreInt64(&kcpRTOMs, rto)
	atomic.StoreInt64(&kcpCwnd, cwnd)
}

func SetKCPQuality(retransmits uint64, fecShards int64) {
	atomic.StoreUint64(&kcpRetransmits, retransmits)
	atomic.StoreInt64(&kcpFECShards, fecShards)
}

func SetKCPDetail(srttVar int64, lost, repeat, fastRetx, earlyRetx, fecRecovered, fecErrs, fecParity, inSegs, outSegs uint64) {
	atomic.StoreInt64(&kcpSRTTVarMs, srttVar)
	atomic.StoreUint64(&kcpLostSegs, lost)
	atomic.StoreUint64(&kcpRepeatSegs, repeat)
	atomic.StoreUint64(&kcpFastRetransSegs, fastRetx)
	atomic.StoreUint64(&kcpEarlyRetransSegs, earlyRetx)
	atomic.StoreUint64(&kcpFECRecovered, fecRecovered)
	atomic.StoreUint64(&kcpFECErrs, fecErrs)
	atomic.StoreUint64(&kcpFECParityShards, fecParity)
	atomic.StoreUint64(&kcpInSegs, inSegs)
	atomic.StoreUint64(&kcpOutSegs, outSegs)
}

func SetTransportSnapshot(s TransportStats) {
	atomic.StoreInt64(&trSndQueue, s.SndQueue)
	atomic.StoreInt64(&trRcvQueue, s.RcvQueue)
	atomic.StoreInt64(&trSndBuf, s.SndBuf)
	atomic.StoreInt64(&trNumStreams, s.NumStreams)
	atomic.StoreInt64(&trNumConns, s.NumConns)
	atomic.StoreUint64(&trHeapAlloc, s.HeapAllocBytes)
	atomic.StoreUint64(&trSys, s.SysBytes)
	atomic.StoreUint64(&trGCCycles, s.GCCycles)
	atomic.StoreInt64(&trTargetFill, int64(s.TargetFill))
	atomic.StoreUint64(&trRetransSegs, s.RetransSegs)
	atomic.StoreUint64(&trLostSegs, s.LostSegs)
	atomic.StoreUint64(&trFECRecovered, s.FECRecovered)
	// keep legacy aliases populated so old UI/tests keep working
	if s.RetransSegs != 0 {
		atomic.StoreUint64(&kcpRetransmits, s.RetransSegs)
	}
	if s.FECRecovered != 0 {
		atomic.StoreInt64(&kcpFECShards, int64(s.FECRecovered))
	}
}

func SetConnKCP(id uint64, rtt int64) {
	connMu.RLock()
	cs := conns[id]
	connMu.RUnlock()
	if cs == nil {
		return
	}
	atomic.StoreInt64(&cs.rttMs, rtt)
}

// ---- Per-target accounting ----

const maxTargetEntries = 100

type targetCounters struct {
	bytesIn  uint64
	bytesOut uint64
	success  uint64
	failure  uint64
	seq      uint64 // insertion order for LRU-ish eviction
}

var targetMu sync.RWMutex
var targets = make(map[string]*targetCounters)
var targetSeq uint64

// IncTarget records one success/failure outcome (plus optional byte counts)
// for a destination address. Hot path is cheap: a brief map lookup under
// RLock plus atomic counter updates; the write lock is only taken when a
// previously unseen target must be inserted.
func IncTarget(target string, inBytes, outBytes uint64, success bool) {
	if target == "" {
		return
	}
	targetMu.RLock()
	tc := targets[target]
	targetMu.RUnlock()
	if tc == nil {
		targetMu.Lock()
		tc = targets[target]
		if tc == nil {
			if len(targets) >= maxTargetEntries {
				// Evict the oldest-inserted entry (LRU-ish, bounded scan).
				oldest := ""
				var oldestSeq uint64
				first := true
				for k, v := range targets {
					s := atomic.LoadUint64(&v.seq)
					if first || s < oldestSeq {
						oldest, oldestSeq, first = k, s, false
					}
				}
				if oldest != "" {
					delete(targets, oldest)
				}
			}
			targetSeq++
			tc = &targetCounters{}
			atomic.StoreUint64(&tc.seq, targetSeq)
			targets[target] = tc
		}
		targetMu.Unlock()
	}
	if inBytes > 0 {
		atomic.AddUint64(&tc.bytesIn, inBytes)
	}
	if outBytes > 0 {
		atomic.AddUint64(&tc.bytesOut, outBytes)
	}
	if success {
		atomic.AddUint64(&tc.success, 1)
	} else {
		atomic.AddUint64(&tc.failure, 1)
	}
}

// TopTargets returns up to limit targets ordered by total bytes (desc).
// It always returns a non-nil slice.
func TopTargets(limit int) []TargetStat {
	if limit <= 0 {
		return []TargetStat{}
	}
	targetMu.RLock()
	out := make([]TargetStat, 0, len(targets))
	for tgt, tc := range targets {
		out = append(out, TargetStat{
			Target:   tgt,
			BytesIn:  atomic.LoadUint64(&tc.bytesIn),
			BytesOut: atomic.LoadUint64(&tc.bytesOut),
			Success:  atomic.LoadUint64(&tc.success),
			Failure:  atomic.LoadUint64(&tc.failure),
		})
	}
	targetMu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		bi := out[i].BytesIn + out[i].BytesOut
		bj := out[j].BytesIn + out[j].BytesOut
		if bi != bj {
			return bi > bj
		}
		if out[i].Success != out[j].Success {
			return out[i].Success > out[j].Success
		}
		return out[i].Target < out[j].Target
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// RuntimeNow samples process runtime stats. Called per status scrape, not
// on any packet hot path.
func RuntimeNow() RuntimeStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return RuntimeStats{
		Goroutines:    runtime.NumGoroutine(),
		MemAllocBytes: m.Alloc,
		MemSysBytes:   m.Sys,
	}
}

func RegisterConnection(remoteAddr, localAddr string) uint64 {
	id := atomic.AddUint64(&connID, 1)
	cs := &connStats{
		id:         id,
		remoteAddr: remoteAddr,
		localAddr:  localAddr,
		startedAt:  time.Now().UTC(),
	}
	cs.lastSeen.Store(time.Now().UTC())
	cs.closedAt.Store(time.Time{})
	connMu.Lock()
	conns[id] = cs
	connMu.Unlock()
	return id
}

func UnregisterConnection(id uint64) {
	now := time.Now().UTC()
	connMu.Lock()
	cs := conns[id]
	if cs == nil {
		connMu.Unlock()
		return
	}
	cs.closedAt.Store(now)
	cs.lastSeen.Store(now)
	for delID, c := range conns {
		if closedAt, ok := c.closedAt.Load().(time.Time); ok && !closedAt.IsZero() {
			if now.Sub(closedAt) > connectionRetention {
				delete(conns, delID)
			}
		}
	}
	connMu.Unlock()
}

func AddConnIngress(id uint64, bytes int) {
	if bytes <= 0 {
		return
	}
	connMu.RLock()
	cs := conns[id]
	connMu.RUnlock()
	if cs == nil {
		return
	}
	atomic.AddUint64(&cs.bytesIn, uint64(bytes))
	cs.lastSeen.Store(time.Now().UTC())
}

func AddConnEgress(id uint64, bytes int) {
	if bytes <= 0 {
		return
	}
	connMu.RLock()
	cs := conns[id]
	connMu.RUnlock()
	if cs == nil {
		return
	}
	atomic.AddUint64(&cs.bytesOut, uint64(bytes))
	cs.lastSeen.Store(time.Now().UTC())
}

func IncConnStreams(id uint64) {
	connMu.RLock()
	cs := conns[id]
	connMu.RUnlock()
	if cs == nil {
		return
	}
	atomic.AddInt64(&cs.activeStreams, 1)
	cs.lastSeen.Store(time.Now().UTC())
}

func DecConnStreams(id uint64) {
	connMu.RLock()
	cs := conns[id]
	connMu.RUnlock()
	if cs == nil {
		return
	}
	atomic.AddInt64(&cs.activeStreams, -1)
	cs.lastSeen.Store(time.Now().UTC())
}

func SnapshotNow() Snapshot {
	uptime := time.Since(startTime).Seconds()
	if uptime == 0 {
		uptime = 1
	}
	inBytes := atomic.LoadUint64(&ingressBytes)
	outBytes := atomic.LoadUint64(&egressBytes)
	roleValue, _ := role.Load().(string)
	if roleValue == "" {
		roleValue = "unknown"
	}
	now := time.Now().UTC()
	connMu.Lock()
	for id, cs := range conns {
		if closedAt, ok := cs.closedAt.Load().(time.Time); ok && !closedAt.IsZero() {
			if now.Sub(closedAt) > connectionRetention {
				delete(conns, id)
			}
		}
	}
	connMu.Unlock()
	connMu.RLock()
	connSnaps := make([]ConnSnapshot, 0, len(conns))
	for _, cs := range conns {
		status := "active"
		var closedAtStr string
		var closedAt time.Time
		if ca, ok := cs.closedAt.Load().(time.Time); ok && !ca.IsZero() {
			status = "closed"
			closedAt = ca
			closedAtStr = ca.Format(time.RFC3339Nano)
		}
		uptime := time.Since(cs.startedAt).Seconds()
		if !closedAt.IsZero() {
			uptime = closedAt.Sub(cs.startedAt).Seconds()
		}
		if uptime == 0 {
			uptime = 1
		}
		connUptime := uptime
		lastSeen, _ := cs.lastSeen.Load().(time.Time)
		uptimeSeconds := uptime
		connSnaps = append(connSnaps, ConnSnapshot{
			ID:             cs.id,
			RemoteAddr:     cs.remoteAddr,
			LocalAddr:      cs.localAddr,
			StartedAt:      cs.startedAt.Format(time.RFC3339Nano),
			LastSeen:       lastSeen.Format(time.RFC3339Nano),
			Status:         status,
			ClosedAt:       closedAtStr,
			UptimeSeconds:  int64(uptimeSeconds),
			BytesIn:        atomic.LoadUint64(&cs.bytesIn),
			BytesOut:       atomic.LoadUint64(&cs.bytesOut),
			BytesInPerSec:  float64(atomic.LoadUint64(&cs.bytesIn)) / connUptime,
			BytesOutPerSec: float64(atomic.LoadUint64(&cs.bytesOut)) / connUptime,
			ActiveStreams:  atomic.LoadInt64(&cs.activeStreams),
			RttMs:          atomic.LoadInt64(&cs.rttMs),
		})
	}
	connMu.RUnlock()
	sort.Slice(connSnaps, func(i, j int) bool {
		return connSnaps[i].ID < connSnaps[j].ID
	})
	return Snapshot{
		Role:              roleValue,
		Timestamp:         time.Now().UTC().Format(time.RFC3339Nano),
		UptimeSeconds:     int64(time.Since(startTime).Seconds()),
		BytesIn:           inBytes,
		BytesOut:          outBytes,
		PacketsIn:         atomic.LoadUint64(&ingressPackets),
		PacketsOut:        atomic.LoadUint64(&egressPackets),
		BytesInPerSec:     float64(inBytes) / uptime,
		BytesOutPerSec:    float64(outBytes) / uptime,
		ActiveConnections: atomic.LoadInt64(&activeConnections),
		ActiveStreams:     atomic.LoadInt64(&activeStreams),
		SuccessTotal:      atomic.LoadUint64(&successTotal),
		FailureTotal:      atomic.LoadUint64(&failureTotal),
		SuccessTCP:        atomic.LoadUint64(&successTCP),
		FailureTCP:        atomic.LoadUint64(&failureTCP),
		SuccessUDP:        atomic.LoadUint64(&successUDP),
		FailureUDP:        atomic.LoadUint64(&failureUDP),
		SuccessSOCKS:      atomic.LoadUint64(&successSOCKS),
		FailureSOCKS:      atomic.LoadUint64(&failureSOCKS),
		SuccessFwdTCP:     atomic.LoadUint64(&successFwdTCP),
		FailureFwdTCP:     atomic.LoadUint64(&failureFwdTCP),
		SuccessFwdUDP:     atomic.LoadUint64(&successFwdUDP),
		FailureFwdUDP:     atomic.LoadUint64(&failureFwdUDP),
		KCP: KCPStats{
			RTTMs:            atomic.LoadInt64(&kcpRTTMs),
			RTOMs:            atomic.LoadInt64(&kcpRTOMs),
			Cwnd:             atomic.LoadInt64(&kcpCwnd),
			Retransmits:      atomic.LoadUint64(&kcpRetransmits),
			FECShards:        atomic.LoadInt64(&kcpFECShards),
			SRTTVarMs:        atomic.LoadInt64(&kcpSRTTVarMs),
			LostSegs:         atomic.LoadUint64(&kcpLostSegs),
			RepeatSegs:       atomic.LoadUint64(&kcpRepeatSegs),
			FastRetransSegs:  atomic.LoadUint64(&kcpFastRetransSegs),
			EarlyRetransSegs: atomic.LoadUint64(&kcpEarlyRetransSegs),
			FECRecovered:     atomic.LoadUint64(&kcpFECRecovered),
			FECErrs:          atomic.LoadUint64(&kcpFECErrs),
			FECParityShards:  atomic.LoadUint64(&kcpFECParityShards),
			InSegs:           atomic.LoadUint64(&kcpInSegs),
			OutSegs:          atomic.LoadUint64(&kcpOutSegs),
		},
		Runtime:     RuntimeNow(),
		Targets:     TopTargets(maxTargetEntries),
		Connections: connSnaps,
		Transport: TransportStats{
			SndQueue:       atomic.LoadInt64(&trSndQueue),
			RcvQueue:       atomic.LoadInt64(&trRcvQueue),
			SndBuf:         atomic.LoadInt64(&trSndBuf),
			NumStreams:     atomic.LoadInt64(&trNumStreams),
			NumConns:       atomic.LoadInt64(&trNumConns),
			HeapAllocBytes: atomic.LoadUint64(&trHeapAlloc),
			SysBytes:       atomic.LoadUint64(&trSys),
			GCCycles:       atomic.LoadUint64(&trGCCycles),
			TargetFill:     int(atomic.LoadInt64(&trTargetFill)),
			TargetCap:      maxTargetEntries,
			RetransSegs:    atomic.LoadUint64(&trRetransSegs),
			LostSegs:       atomic.LoadUint64(&trLostSegs),
			FECRecovered:   atomic.LoadUint64(&trFECRecovered),
		},
	}
}

func Handler(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	if limit < 0 {
		limit = 0
	}
	if limit > 500 {
		limit = 500
	}
	snap := SnapshotNow()
	if len(snap.Connections) > limit {
		snap.Connections = snap.Connections[:limit]
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(snap)
}

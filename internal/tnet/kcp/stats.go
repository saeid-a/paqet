package kcp

import (
	"runtime"
	"sync"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"

	"paqet/internal/metrics"
)

// Sample is the live transport rollup pushed to metrics on each tick.
type Sample struct {
	SRTT         int64
	RTO          int64
	SRTTVar      int64
	NumSessions  int
	NumStreams   int
	MaxStreams   int
	OldestAgeSec int64
	RetransSegs  uint64
	LostSegs     uint64
	FECRecovered uint64
}

var (
	statMu        sync.RWMutex
	sessions      = make(map[*smux.Session]*kcp.UDPSession)
	streamStarted = make(map[*smux.Stream]time.Time)
)

func trackSession(udp *kcp.UDPSession, sess *smux.Session) {
	if sess == nil {
		return
	}
	statMu.Lock()
	sessions[sess] = udp
	statMu.Unlock()
}

func untrackSession(sess *smux.Session) {
	if sess == nil {
		return
	}
	statMu.Lock()
	delete(sessions, sess)
	statMu.Unlock()
}

// noteStreamOpen records a live stream's start for oldest-age tracking.
// Called from newStrm; Ping's short-lived raw streams are excluded.
func noteStreamOpen(st *smux.Stream) {
	if st == nil {
		return
	}
	statMu.Lock()
	streamStarted[st] = time.Now()
	statMu.Unlock()
}

func noteStreamClose(st *smux.Stream) {
	if st == nil {
		return
	}
	statMu.Lock()
	delete(streamStarted, st)
	statMu.Unlock()
}

// SampleNow prunes closed sessions (ghost fix: NumConns counts live only),
// averages GetSRTT/GetRTO/GetSRTTVar over live sessions (0 when none),
// merges kcp-go DefaultSnmp counters + ring-buffer depths, sums smux
// NumStreams plus per-session max, oldest live-stream age, and pushes
// everything to metrics. Never logs.
func SampleNow() Sample {
	now := time.Now()
	statMu.Lock()
	for sess := range sessions {
		if sess == nil || sess.IsClosed() {
			delete(sessions, sess)
		}
	}
	smuxes := make([]*smux.Session, 0, len(sessions))
	udps := make([]*kcp.UDPSession, 0, len(sessions))
	for sess, udp := range sessions {
		smuxes = append(smuxes, sess)
		udps = append(udps, udp)
	}
	var oldest time.Time
	for _, start := range streamStarted {
		if oldest.IsZero() || start.Before(oldest) {
			oldest = start
		}
	}
	statMu.Unlock()

	var sumSRTT, sumRTO, sumVar int64
	var live int64
	for _, u := range udps {
		if u == nil {
			continue
		}
		live++
		sumSRTT += int64(u.GetSRTT())
		sumRTO += int64(u.GetRTO())
		sumVar += int64(u.GetSRTTVar())
	}
	var srtt, rto, srttVar int64
	if live > 0 {
		srtt, rto, srttVar = sumSRTT/live, sumRTO/live, sumVar/live
	}

	snmp := kcp.DefaultSnmp.Copy()

	numStreams, maxStreams := 0, 0
	for _, s := range smuxes {
		if s == nil || s.IsClosed() {
			continue
		}
		n := s.NumStreams()
		numStreams += n
		if n > maxStreams {
			maxStreams = n
		}
	}

	var oldestAgeSec int64
	if !oldest.IsZero() {
		if d := now.Sub(oldest); d > 0 {
			oldestAgeSec = int64(d.Seconds())
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	metrics.SetKCPStats(srtt, rto, 0)
	metrics.SetKCPQuality(snmp.RetransSegs, int64(snmp.FECRecovered))
	metrics.SetKCPDetail(srttVar, snmp.LostSegs, snmp.RepeatSegs, snmp.FastRetransSegs, snmp.EarlyRetransSegs, snmp.FECRecovered, snmp.FECErrs, snmp.FECParityShards, snmp.InSegs, snmp.OutSegs)
	metrics.SetTransportSnapshot(metrics.TransportStats{
		SndQueue:           int64(snmp.RingBufferSndQueue),
		RcvQueue:           int64(snmp.RingBufferRcvQueue),
		SndBuf:             int64(snmp.RingBufferSndBuffer),
		NumStreams:         int64(numStreams),
		NumConns:           int64(len(smuxes)),
		MaxStreamsPerConn:  int64(maxStreams),
		OldestStreamAgeSec: oldestAgeSec,
		HeapAllocBytes:     m.HeapAlloc,
		SysBytes:           m.Sys,
		GCCycles:           uint64(m.NumGC),
		RetransSegs:        snmp.RetransSegs,
		LostSegs:           snmp.LostSegs,
		FECRecovered:       snmp.FECRecovered,
	})

	return Sample{SRTT: srtt, RTO: rto, SRTTVar: srttVar, NumSessions: len(smuxes), NumStreams: numStreams, MaxStreams: maxStreams, OldestAgeSec: oldestAgeSec, RetransSegs: snmp.RetransSegs, LostSegs: snmp.LostSegs, FECRecovered: snmp.FECRecovered}
}

// StartStatsLoop samples every 2s until stop closes. Safe to call once.
func StartStatsLoop(stop <-chan struct{}) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			SampleNow()
		}
	}
}

var statsLoopOnce sync.Once

// EnsureStatsLoop starts the process-wide 2s sampler once; subsequent
// calls are no-ops. Called from server and client Start so live KCP/SNMP
// values flow into metrics without metrics importing this package
// (which would be an import cycle).
func EnsureStatsLoop() {
	statsLoopOnce.Do(func() {
		ch := make(chan struct{})
		go StartStatsLoop(ch)
	})
}

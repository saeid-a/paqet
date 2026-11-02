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
	RetransSegs  uint64
	LostSegs     uint64
	FECRecovered uint64
}

var (
	statMu   sync.RWMutex
	sessions = make(map[*kcp.UDPSession]*smux.Session)
)

func trackSession(udp *kcp.UDPSession, sess *smux.Session) {
	if udp == nil {
		return
	}
	statMu.Lock()
	sessions[udp] = sess
	statMu.Unlock()
}

func untrackSession(udp *kcp.UDPSession) {
	if udp == nil {
		return
	}
	statMu.Lock()
	delete(sessions, udp)
	statMu.Unlock()
}

// SampleNow averages GetSRTT/GetRTO/GetSRTTVar over live sessions (0 when
// none), merges kcp-go DefaultSnmp counters + ring-buffer depths, sums
// smux NumStreams, and pushes everything to metrics. Never logs.
func SampleNow() Sample {
	statMu.RLock()
	udps := make([]*kcp.UDPSession, 0, len(sessions))
	smuxes := make([]*smux.Session, 0, len(sessions))
	for u, s := range sessions {
		udps = append(udps, u)
		smuxes = append(smuxes, s)
	}
	statMu.RUnlock()

	var sumSRTT, sumRTO, sumVar int64
	for _, u := range udps {
		if u == nil {
			continue
		}
		sumSRTT += int64(u.GetSRTT())
		sumRTO += int64(u.GetRTO())
		sumVar += int64(u.GetSRTTVar())
	}
	var srtt, rto, srttVar int64
	if n := int64(len(udps)); n > 0 {
		srtt, rto, srttVar = sumSRTT/n, sumRTO/n, sumVar/n
	}

	snmp := kcp.DefaultSnmp.Copy()

	numStreams := 0
	for _, s := range smuxes {
		if s == nil || s.IsClosed() {
			continue
		}
		numStreams += s.NumStreams()
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	metrics.SetKCPStats(srtt, rto, 0)
	metrics.SetKCPQuality(snmp.RetransSegs, int64(snmp.FECRecovered))
	metrics.SetKCPDetail(srttVar, snmp.LostSegs, snmp.RepeatSegs, snmp.FastRetransSegs, snmp.EarlyRetransSegs, snmp.FECRecovered, snmp.FECErrs, snmp.FECParityShards, snmp.InSegs, snmp.OutSegs)
	metrics.SetTransportSnapshot(metrics.TransportStats{
		SndQueue:       int64(snmp.RingBufferSndQueue),
		RcvQueue:       int64(snmp.RingBufferRcvQueue),
		SndBuf:         int64(snmp.RingBufferSndBuffer),
		NumStreams:     int64(numStreams),
		NumConns:       int64(len(udps)),
		HeapAllocBytes: m.HeapAlloc,
		SysBytes:       m.Sys,
		GCCycles:       uint64(m.NumGC),
		RetransSegs:    snmp.RetransSegs,
		LostSegs:       snmp.LostSegs,
		FECRecovered:   snmp.FECRecovered,
	})

	return Sample{SRTT: srtt, RTO: rto, SRTTVar: srttVar, NumSessions: len(udps), NumStreams: numStreams, RetransSegs: snmp.RetransSegs, LostSegs: snmp.LostSegs, FECRecovered: snmp.FECRecovered}
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

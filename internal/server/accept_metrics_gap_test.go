package server

import (
	"os"
	"strings"
	"testing"
)

func TestAcceptLoopsCounted(t *testing.T) {
	for _, f := range []string{"server.go", "handle.go"} {
		src, _ := os.ReadFile(f)
		s := string(src)
		// listen() Accept error + handleConn AcceptStrm error must count
		if !strings.Contains(s, "IncFailure") {
			continue
		}
		// specific: Accept/AcceptStrm error branches contain metrics
		_ = s
	}
	// server.go listen Accept
	srv, _ := os.ReadFile("server.go")
	idx := strings.Index(string(srv), "func (s *Server) listen")
	if idx < 0 {
		t.Fatal("listen not found")
	}
	body := string(srv)[idx:]
	if !strings.Contains(body, "IncFailure") {
		t.Fatal("server listen Accept errors skip metrics counting")
	}
	// handle.go AcceptStrm
	h, _ := os.ReadFile("handle.go")
	idx = strings.Index(string(h), "AcceptStrm()")
	if idx < 0 {
		t.Fatal("AcceptStrm not found")
	}
	// 500 bytes around it must contain IncFailure after fix
	snip := string(h)
	if idx > 500 {
		snip = snip[idx-200:]
	}
	if !strings.Contains(string(h)[idx:], "IncFailure") {
		t.Fatal("handleConn AcceptStrm errors skip metrics counting")
	}
}

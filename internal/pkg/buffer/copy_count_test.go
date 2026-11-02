package buffer

import (
	"strings"
	"testing"
)

func TestCopyTCountReportsBytes(t *testing.T) {
	src := strings.NewReader("hello world")
	var dst strings.Builder
	n, err := CopyTCount(&dst, src)
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Fatalf("want 11 bytes, got %d", n)
	}
	if dst.String() != "hello world" {
		t.Fatalf("bad copy: %q", dst.String())
	}
}

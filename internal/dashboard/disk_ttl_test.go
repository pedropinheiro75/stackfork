package dashboard

import (
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dockerdisk"
)

// A full disk is re-measured every 30 s, so the banner clears soon after space is freed.
func TestFullDiskIsMeasuredMoreOften(t *testing.T) {
	var d diskState
	if d.ttl() != diskTTL {
		t.Fatal("no reading yet: normal interval")
	}
	d.usage = &dockerdisk.Usage{Percent: 99}
	if d.ttl() != fullDiskTTL {
		t.Fatal("a full disk must be re-measured sooner")
	}
	d.usage = &dockerdisk.Usage{Percent: 40}
	if d.ttl() != diskTTL {
		t.Fatal("a healthy disk keeps the normal interval")
	}
}

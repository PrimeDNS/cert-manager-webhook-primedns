package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Live test against the real API. Needs PRIMEDNS_TEST_API_KEY (scope acme:name:_acme-challenge.<host>) and
// PRIMEDNS_TEST_HOST (a host in a PrimeDNS zone of that key's account), plus dig. It checks that the record
// is visible on both name servers within seconds and gone after CleanUp.
func TestLiveRoundTrip(t *testing.T) {
	key, host := os.Getenv("PRIMEDNS_TEST_API_KEY"), os.Getenv("PRIMEDNS_TEST_HOST")
	if key == "" || host == "" {
		t.Skip("PRIMEDNS_TEST_API_KEY / PRIMEDNS_TEST_HOST not set")
	}
	name, value := "_acme-challenge."+host, "live-test-"+time.Now().Format("150405")
	c := newAPIClient(os.Getenv("PRIMEDNS_TEST_API_URL"), key)
	ctx := context.Background()

	require.NoError(t, c.Present(ctx, name, value))
	require.NoError(t, c.Present(ctx, name, value), "a repeat of the same name and value must succeed")
	start := time.Now()
	for _, ns := range []string{"ns1.no-ip.pl", "ns2.no-ip.pl"} {
		require.Eventually(t, func() bool { return strings.Contains(dig(ns, name), value) }, 10*time.Second, 500*time.Millisecond, ns)
	}
	t.Logf("visible on both name servers %.1f s after the write", time.Since(start).Seconds())

	require.NoError(t, c.CleanUp(ctx, name, value))
	// A delete does not flush the name server cache, so the old answer can be served for about a minute.
	require.Eventually(t, func() bool { return !strings.Contains(dig("ns1.no-ip.pl", name), value) }, 90*time.Second, 2*time.Second)
}

func dig(server, name string) string {
	out, _ := exec.Command("dig", "+short", "+time=2", "+tries=1", "TXT", name, "@"+server).Output()
	return string(out)
}

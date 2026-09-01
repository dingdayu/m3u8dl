package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestParseRateLimit(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"2M", 2 << 20, false},
		{"500KB", 500 << 10, false},
		{"1.5m", 1572864, false},  // 1.5 * 1 MiB, case-insensitive
		{"200000", 200000, false}, // bare bytes/s
		{"1G", 1 << 30, false},
		{"0", 0, false}, // documented as "no limit"
		{" 0 ", 0, false},
		{"0.0", 0, false},
		{"", 0, false},       // empty = no limit
		{"500", 1024, false}, // floor at 1 KiB/s
		{"-1M", 0, true},
		{"abc", 0, true},
		{"M", 0, true},
		{"2X4", 0, true},
		{"1.2.3", 0, true},
	}
	for _, c := range cases {
		got, err := parseRateLimit(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseRateLimit(%q): want error, got %d", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseRateLimit(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseRateLimit(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSetupRateLimit(t *testing.T) {
	withRestoredGlobals(t)

	// "0" and empty must leave the limiter unset (unlimited), per help text.
	for _, v := range []string{"", "0"} {
		opts.rateLimit, rateLimiter = v, nil
		if err := setupRateLimit(); err != nil {
			t.Fatalf("setupRateLimit(%q): %v", v, err)
		}
		if rateLimiter != nil {
			t.Fatalf("setupRateLimit(%q): limiter should stay nil", v)
		}
	}

	opts.rateLimit = "2M"
	if err := setupRateLimit(); err != nil {
		t.Fatalf("setupRateLimit(2M): %v", err)
	}
	if rateLimiter == nil {
		t.Fatal("setupRateLimit(2M): limiter must be installed")
	}
	if limit := float64(rateLimiter.Limit()); limit != 2<<20 {
		t.Fatalf("limiter rate = %v/s, want %v/s", limit, float64(2<<20))
	}
	if burst := rateLimiter.Burst(); burst != maxBurstBytes {
		t.Fatalf("2M burst = %d, want capped %d", burst, maxBurstBytes)
	}

	// A low cap gets ~one second of budget as burst, not the fixed 256 KiB.
	opts.rateLimit = "1K"
	if err := setupRateLimit(); err != nil {
		t.Fatalf("setupRateLimit(1K): %v", err)
	}
	if burst := rateLimiter.Burst(); burst != 1024 {
		t.Fatalf("1K burst = %d, want 1024", burst)
	}

	opts.rateLimit = "bogus"
	rateLimiter = nil
	if err := setupRateLimit(); err == nil {
		t.Fatal("setupRateLimit(bogus): want error")
	}
}

func TestThrottleReadsPacesThroughput(t *testing.T) {
	withRestoredGlobals(t)
	rateLimiter = rate.NewLimiter(rate.Limit(200*1024), maxBurstBytes)

	const size = 600 * 1024
	src := bytes.NewReader(bytes.Repeat([]byte{SYNC_BYTE}, size))

	start := time.Now()
	n, err := io.Copy(io.Discard, throttleReads(src, t.Context()))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if n != size {
		t.Fatalf("read %d bytes, want %d", n, size)
	}
	// Theory: burst = min(256KiB, rate) = 200 KiB flows immediately, the
	// remaining 400 KiB drains at 200 KiB/s => ~2s. The floor is deliberately
	// loose (1.2s) so slow/raced CI cannot flake, while an unthrottled
	// regression (local copy: <10ms) would blow through it.
	if elapsed < 1200*time.Millisecond {
		t.Errorf("600KiB at 200KiB/s took %v; expected roughly 2s", elapsed)
	}
}

func TestThrottleReadsNoopWhenUnlimited(t *testing.T) {
	withRestoredGlobals(t)
	rateLimiter = nil
	var want io.Reader = bytes.NewReader([]byte("x"))
	if got := throttleReads(want, t.Context()); got != want {
		t.Fatal("without a limiter throttleReads must return the reader as-is")
	}
}

// Deliberate throttling sleep is not network inactivity: a segment that
// needs ~2s to drain at 200 KiB/s must still complete even when the (old)
// total-deadline equivalent is much shorter than that.
func TestThrottleTimeExcludedFromDeadline(t *testing.T) {
	withRestoredGlobals(t)
	reqTimeout = 400 * time.Millisecond // far shorter than the ~2s throttled drain
	rateLimiter = rate.NewLimiter(rate.Limit(200*1024), maxBurstBytes)
	data := resumeBody(600 * 1024)
	srv := newFlakyRangeServer(t, data, nil, nil)
	defer srv.Close()

	dir := t.TempDir()
	curr := filepath.Join(dir, "00001.ts")
	if !tryDownload(srv.URL+"/00001.ts", "", curr) {
		t.Fatal("a valid rate limit must not deterministically fail the download")
	}
	got, err := os.ReadFile(curr)
	if err != nil {
		t.Fatalf("read finalized segment: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("finalized content differs from the served body")
	}
}

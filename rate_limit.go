package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/time/rate"
)

// rateLimiter throttles the aggregate download speed across all worker
// goroutines when --rate-limit is set; nil means unlimited.
var rateLimiter *rate.Limiter

// maxBurstBytes caps the token-bucket burst; the effective burst is
// min(maxBurstBytes, rate) — roughly one second of the configured budget.
const maxBurstBytes = 256 * 1024

// parseRateLimit converts a --rate-limit value such as "2M", "500KB", "1.5M"
// or a plain "200000" into bytes per second. Multipliers are 1024-based
// (K/KB, M/MB, G/GB), case-insensitive. Empty or "0" means unlimited
// (returns 0, nil). Returns an error for invalid or negative input.
func parseRateLimit(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	upper := strings.ToUpper(s)
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		m      int64
	}{
		{"GB", 1 << 30}, {"G", 1 << 30},
		{"MB", 1 << 20}, {"M", 1 << 20},
		{"KB", 1 << 10}, {"K", 1 << 10},
	} {
		if strings.HasSuffix(upper, u.suffix) {
			mult = u.m
			s = strings.TrimSpace(s[:len(s)-len(u.suffix)])
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("无效的限速值 %q（示例：2M、500KB、200000）", s)
	}
	if f == 0 {
		return 0, nil
	}
	bps := int64(f * float64(mult))
	if bps < 1024 {
		bps = 1024 // keep at least a sane floor so downloads still progress
	}
	return bps, nil
}

// setupRateLimit parses opts.rateLimit once at startup (shared by all workers).
func setupRateLimit() error {
	bps, err := parseRateLimit(opts.rateLimit)
	if err != nil {
		return failCode(1, "%v", err)
	}
	if bps > 0 {
		// Burst ≈ one second of budget (capped): a fixed large burst would let
		// a low cap like 1K run unthrottled for minutes before pacing starts.
		rateLimiter = rate.NewLimiter(rate.Limit(bps), int(min(int64(maxBurstBytes), bps)))
	}
	return nil
}

// chunkBytes is the granularity of rate accounting: each read charges at
// most this many tokens. Smaller paces more smoothly, larger reduces limiter
// calls. The effective chunk never exceeds the limiter burst.
const chunkBytes = 64 * 1024

// throttledReader paces an underlying reader through the shared token bucket:
// each (re-chunked) Read charges the limiter only for the bytes actually
// delivered — short reads (HTTP/2 frames, network fragmentation) would
// otherwise over-draw the bucket and drag throughput below the limit.
type throttledReader struct {
	r     io.Reader
	ctx   context.Context
	chunk int
}

func (t *throttledReader) Read(p []byte) (int, error) {
	if len(p) > t.chunk {
		p = p[:t.chunk]
	}
	n, err := t.r.Read(p)
	if n > 0 {
		// Blocking before the return paces the caller's next read, so the
		// aggregate rate still converges to the limit. The wait follows the
		// request context, so an aborted response cancels the limiter wait too.
		if werr := rateLimiter.WaitN(t.ctx, n); werr != nil && err == nil {
			err = werr
		}
	}
	return n, err
}

// throttleReads paces r through the shared token bucket; a no-op when
// --rate-limit is unset. ctx should be the owning request's context so an
// aborted response also cancels any in-flight limiter wait.
func throttleReads(r io.Reader, ctx context.Context) io.Reader {
	if rateLimiter == nil {
		return r
	}
	// WaitN rejects a charge above the burst, so the read chunk never exceeds it.
	return &throttledReader{r: r, ctx: ctx, chunk: min(chunkBytes, rateLimiter.Burst())}
}

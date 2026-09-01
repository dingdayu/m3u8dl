package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// withRestoredGlobals snapshots the package-level HTTP / rate-limit singletons
// and restores them after the test so cases cannot contaminate each other.
func withRestoredGlobals(t *testing.T) {
	t.Helper()
	savedOpts, savedClient, savedLimiter, savedTimeout := opts, httpClient, rateLimiter, reqTimeout
	t.Cleanup(func() {
		opts, httpClient, rateLimiter, reqTimeout = savedOpts, savedClient, savedLimiter, savedTimeout
	})
}

// ============================== parseContentRange ==============================

func TestParseContentRange(t *testing.T) {
	cases := []struct {
		in                string
		start, end, total int64
		ok                bool
	}{
		{"bytes 200-1073/1234", 200, 1073, 1234, true},
		{"bytes 0-499/500", 0, 499, 500, true},
		{"bytes 0-499/*", 0, 499, -1, true}, // unknown total
		{"bytes 49999-49999/50000", 49999, 49999, 50000, true},
		{"", 0, 0, 0, false},
		{"items 0-1/2", 0, 0, 0, false},        // wrong unit
		{"bytes 200-1073", 0, 0, 0, false},     /* missing total */
		{"bytes x-y/10", 0, 0, 0, false},       // non-numeric range
		{"bytes 200-1073/abc", 0, 0, 0, false}, // non-numeric total
		{"bytes 2001073/1234", 0, 0, 0, false}, // missing dash
	}
	for _, c := range cases {
		s, e, tot, ok := parseContentRange(c.in)
		if ok != c.ok {
			t.Errorf("parseContentRange(%q) ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && (s != c.start || e != c.end || tot != c.total) {
			t.Errorf("parseContentRange(%q) = (%d,%d,%d), want (%d,%d,%d)",
				c.in, s, e, tot, c.start, c.end, c.total)
		}
	}
}

// ============================== parseRateLimit ==============================

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

// ============================== HTTP/2 transport ==============================

func TestTransportHTTP2Negotiation(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, "ok")
		}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	t.Run("ForceAttemptHTTP2 negotiates h2 on the insecure transport",
		func(t *testing.T) {
			// --insecure path: newTransport(true) sets TLSClientConfig AND
			// ForceAttemptHTTP2, so ALPN can still pick h2.
			res, err := (&http.Client{Transport: newTransport(true)}).Get(srv.URL)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer res.Body.Close()
			if res.ProtoMajor != 2 || res.Proto != "HTTP/2.0" {
				t.Fatalf("proto = %s, want HTTP/2.0", res.Proto)
			}
		})

	t.Run("without ForceAttemptHTTP2 a custom TLSClientConfig falls back to h1",
		func(t *testing.T) {
			// Principle: Go only auto-enables HTTP/2 on a transport when its
			// TLSClientConfig is nil (or NextProtos already advertises h2).
			// As soon as a custom TLSClientConfig is set — which our
			// InsecureSkipVerify option requires — HTTP/2 stays DISABLED
			// unless ForceAttemptHTTP2 is true. This locks in the regression
			// that the shared-client/ForceAttemptHTTP2 fix repaired.
			tr := newTransport(true)
			tr.ForceAttemptHTTP2 = false
			res, err := (&http.Client{Transport: tr}).Get(srv.URL)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer res.Body.Close()
			if res.ProtoMajor != 1 {
				t.Fatalf("proto = %s, want HTTP/1.1 downgrade", res.Proto)
			}
		})
}

// ============================== Range resume ==============================

// resumeBody builds an n-byte payload whose first byte is the MPEG-TS sync
// marker, so junk-stripping is a no-op and the landed file must equal the body.
func resumeBody(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i % 251)
	}
	data[0] = SYNC_BYTE
	return data
}

// newFlakyRangeServer serves data with proper Range/206/416 semantics. When
// truncFirst is set, the first un-ranged request announces Content-Length
// len(data) but only 20000 bytes are written before the connection is killed
// (simulating an interrupted transfer). When capNext is set, the next ranged
// request is answered with a capped 10000-byte subrange instead of the tail.
func newFlakyRangeServer(t *testing.T, data []byte, truncFirst, capNext *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			total := int64(len(data))
			w.Header().Set("ETag", fmt.Sprintf("\"seg-%d\"", total))
			if rg := r.Header.Get("Range"); rg != "" {
				var start int64
				if _, err := fmt.Sscanf(rg, "bytes=%d-", &start); err != nil {
					http.Error(w, "bad range", http.StatusBadRequest)
					return
				}
				if start >= total {
					w.Header().Set("Content-Range",
						fmt.Sprintf("bytes */%d", total))
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return
				}
				end := total
				if capNext != nil && capNext.CompareAndSwap(true, false) {
					end = min(start+10000, total)
				}
				w.Header().Set("Content-Range",
					fmt.Sprintf("bytes %d-%d/%d", start, end-1, total))
				w.Header().Set("Content-Length",
					fmt.Sprintf("%d", end-start))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(data[start:end])
				return
			}
			if truncFirst != nil && truncFirst.CompareAndSwap(true, false) {
				w.Header().Set("Content-Length", fmt.Sprintf("%d", total))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(data[:20000])
				if hj, ok := w.(http.Hijacker); ok {
					if conn, _, err := hj.Hijack(); err == nil {
						_ = conn.Close() // kill the transfer mid-body
					}
				}
				return
			}
			w.Header().Set("Content-Length", fmt.Sprintf("%d", total))
			_, _ = w.Write(data)
		}))
}

func TestRangeResumeAfterInterruptedTransfer(t *testing.T) {
	withRestoredGlobals(t)
	data := resumeBody(50000)
	var trunc atomic.Bool
	trunc.Store(true)
	srv := newFlakyRangeServer(t, data, &trunc, nil)
	defer srv.Close()

	dir := t.TempDir()
	curr := filepath.Join(dir, "00001.ts")
	url := srv.URL + "/00001.ts"

	if tryDownload(url, "", curr) {
		t.Fatal("first attempt must fail on an interrupted body")
	}
	if exists, _ := pathExists(curr); exists {
		t.Fatal("segment must not be finalized while incomplete")
	}
	if got := fileSizeOf(curr + ".part"); got != 20000 {
		t.Fatalf("interrupted transfer should keep 20000 bytes of .part, got %d",
			got)
	}

	// Second attempt: Range: bytes=20000- -> 206 -> append -> finalize.
	if !tryDownload(url, "", curr) {
		t.Fatal("second attempt must resume and complete")
	}
	got, err := os.ReadFile(curr)
	if err != nil {
		t.Fatalf("read finalized segment: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("finalized content differs from the served body")
	}
	if _, err := os.Stat(curr + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part must be removed after finalize (stat err=%v)", err)
	}
}

func TestRangeResumeStalePartRecoversVia416(t *testing.T) {
	withRestoredGlobals(t)
	data := resumeBody(50000)
	var noTrunc atomic.Bool
	srv := newFlakyRangeServer(t, data, &noTrunc, nil)
	defer srv.Close()

	dir := t.TempDir()
	curr := filepath.Join(dir, "00001.ts")
	stale := curr + ".part"
	if err := os.WriteFile(stale, bytes.Repeat([]byte{0xFF}, 60000), 0o666); err != nil {
		t.Fatal(err)
	}
	// A matching sidecar (with a validator) lets the resume proceed far enough
	// to hit the 416.
	if err := os.WriteFile(stale+".meta",
		[]byte(srv.URL+"/00001.ts\n\"stale\"\n"), 0o666); err != nil {
		t.Fatal(err)
	}

	// .part (60000) exceeds the resource (50000): the server answers 416,
	// the client must drop the .part and redownload from scratch.
	if !tryDownload(srv.URL+"/00001.ts", "", curr) {
		t.Fatal("416 must fall back to a full redownload and succeed")
	}
	got, err := os.ReadFile(curr)
	if err != nil {
		t.Fatalf("read finalized segment: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("finalized content differs from the served body")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale .part must be removed (stat err=%v)", err)
	}
}

func TestRangeResumeHonorsCapped206(t *testing.T) {
	withRestoredGlobals(t)
	data := resumeBody(50000)
	var noTrunc, capped atomic.Bool
	capped.Store(true)
	srv := newFlakyRangeServer(t, data, &noTrunc, &capped)
	defer srv.Close()

	dir := t.TempDir()
	curr := filepath.Join(dir, "00001.ts")
	part := curr + ".part"
	if err := os.WriteFile(part, data[:10000], 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part+".meta",
		[]byte(srv.URL+"/00001.ts\n\"part\"\n"), 0o666); err != nil {
		t.Fatal(err)
	}

	// The server caps the 206 to bytes 10000-19999/50000: the segment is
	// still incomplete, so .part (now 20000 B) must be KEPT, not finalized.
	if tryDownload(srv.URL+"/00001.ts", "", curr) {
		t.Fatal("capped subrange must not finalize the segment")
	}
	if exists, _ := pathExists(curr); exists {
		t.Fatal("segment must not exist after a capped subrange")
	}
	if got := fileSizeOf(part); got != 20000 {
		t.Fatalf(".part should hold 20000 resumable bytes, got %d", got)
	}

	// Next attempt resumes from 20000 and receives the real tail.
	if !tryDownload(srv.URL+"/00001.ts", "", curr) {
		t.Fatal("follow-up attempt must complete the segment")
	}
	got, err := os.ReadFile(curr)
	if err != nil {
		t.Fatalf("read finalized segment: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("finalized content differs from the served body")
	}
}

// ============================== rate limiting ==============================

// A failed alternate attempt must not leave its partial bytes for the next
// primary resume: primary and alternate may resolve to different resources,
// so the primary has to restart from byte zero rather than append its tail
// to an alternate prefix.
func TestAlternatePartialNeverSeedsPrimaryResume(t *testing.T) {
	withRestoredGlobals(t)
	dataA := resumeBody(50000)
	dataB := resumeBody(50000)
	for i := 1; i < len(dataB); i++ {
		dataB[i] ^= 0x5A // same length, different content from byte 1 on
	}

	var truncA, truncB atomic.Bool
	truncA.Store(true)
	truncB.Store(true)
	primary := newFlakyRangeServer(t, dataA, &truncA, nil)
	defer primary.Close()
	alt := newFlakyRangeServer(t, dataB, &truncB, nil)
	defer alt.Close()

	dir := t.TempDir()
	ts := TsInfo{
		Name:   "00001.ts",
		Url:    primary.URL + "/00001.ts",
		AltUrl: alt.URL + "/00001.ts",
	}
	if !downloadTsFile(ts, dir) {
		t.Fatal("download must complete via the primary after both first attempts break")
	}
	got, err := os.ReadFile(filepath.Join(dir, ts.Name))
	if err != nil {
		t.Fatalf("read finalized segment: %v", err)
	}
	if !bytes.Equal(got, dataA) {
		t.Fatal("finalized segment is not the primary resource (bytes spliced across URLs)")
	}
}

// A 206 that is missing Content-Range or starts at an unexpected offset is a
// tail, not the resource: the client must discard it and re-fetch from byte
// zero instead of finalizing the tail as the whole segment.
func TestRangeResumeRejectsUnusable206(t *testing.T) {
	withRestoredGlobals(t)
	data := resumeBody(50000)
	total := int64(len(data))

	cases := map[string]func(w http.ResponseWriter, start int64){
		"missing Content-Range": func(w http.ResponseWriter, start int64) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", total-start))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(data[start:])
		},
		"mismatched start": func(w http.ResponseWriter, start int64) {
			w.Header().Set("Content-Range",
				fmt.Sprintf("bytes 10000-%d/%d", total-1, total))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", total-10000))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(data[10000:])
		},
	}
	for name, serve206 := range cases {
		t.Run(name, func(t *testing.T) {
			withRestoredGlobals(t)
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					if rg := r.Header.Get("Range"); rg != "" {
						var start int64
						if _, err := fmt.Sscanf(rg, "bytes=%d-", &start); err != nil {
							http.Error(w, "bad range", http.StatusBadRequest)
							return
						}
						serve206(w, start)
						return
					}
					w.Header().Set("Content-Length", fmt.Sprintf("%d", total))
					_, _ = w.Write(data)
				}))
			defer srv.Close()

			dir := t.TempDir()
			curr := filepath.Join(dir, "00001.ts")
			if err := os.WriteFile(curr+".part", data[:20000], 0o666); err != nil {
				t.Fatal(err)
			}
			// A validator lets the resume issue a Range request, so the
			// unusable-206 re-fetch path is actually exercised.
			if err := os.WriteFile(curr+".part.meta",
				[]byte(srv.URL+"/00001.ts\n\"x\"\n"), 0o666); err != nil {
				t.Fatal(err)
			}

			if !tryDownload(srv.URL+"/00001.ts", "", curr) {
				t.Fatal("client must re-fetch from byte zero and complete")
			}
			got, err := os.ReadFile(curr)
			if err != nil {
				t.Fatalf("read finalized segment: %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Fatal("finalized content differs from the served body")
			}
		})
	}
}

// A .part left by another run is only resumed when its sidecar ties it to the
// same URL; a foreign or missing sidecar makes the partial unverifiable and
// it must be discarded instead of spliced into this download.
func TestPartMetaBindsPartialToURL(t *testing.T) {
	withRestoredGlobals(t)
	data := resumeBody(50000)
	var noTrunc atomic.Bool
	srv := newFlakyRangeServer(t, data, &noTrunc, nil)
	defer srv.Close()

	cases := []struct {
		name    string
		metaURL string
	}{
		{"foreign sidecar", srv.URL + "/other-playlist/00001.ts"},
		{"missing sidecar", ""},
		// Matching URL but the server gave no validator: the partial is still
		// unverifiable (a same-URL revised object is undetectable) -> discard.
		{"same-url no validator", srv.URL + "/00001.ts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withRestoredGlobals(t)
			dir := t.TempDir()
			curr := filepath.Join(dir, "00001.ts")
			if err := os.WriteFile(curr+".part", data[:20000], 0o666); err != nil {
				t.Fatal(err)
			}
			if tc.metaURL != "" {
				if err := os.WriteFile(curr+".part.meta", []byte(tc.metaURL+"\n\n"), 0o666); err != nil {
					t.Fatal(err)
				}
			}

			if !tryDownload(srv.URL+"/00001.ts", "", curr) {
				t.Fatal("unverifiable partial must be discarded and refetched whole")
			}
			got, err := os.ReadFile(curr)
			if err != nil {
				t.Fatalf("read finalized segment: %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Fatal("finalized content differs from the served body")
			}
		})
	}
}

// When the server provides validators, the resume sends them as If-Range: a
// matching validator resumes mid-file (206), a stale one makes the server
// answer 200 and the segment restarts from byte zero.
func TestResumeValidatesPartialWithIfRange(t *testing.T) {
	withRestoredGlobals(t)
	data := resumeBody(50000)
	const etag = `"v1"`
	total := int64(len(data))

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", etag)
			if rg := r.Header.Get("Range"); rg != "" {
				var start int64
				if _, err := fmt.Sscanf(rg, "bytes=%d-", &start); err != nil {
					http.Error(w, "bad range", http.StatusBadRequest)
					return
				}
				if r.Header.Get("If-Range") != etag {
					// Resource changed since the partial was written: full body.
					w.Header().Set("Content-Length", fmt.Sprintf("%d", total))
					_, _ = w.Write(data)
					return
				}
				w.Header().Set("Content-Range",
					fmt.Sprintf("bytes %d-%d/%d", start, total-1, total))
				w.Header().Set("Content-Length", fmt.Sprintf("%d", total-start))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(data[start:])
				return
			}
			w.Header().Set("Content-Length", fmt.Sprintf("%d", total))
			_, _ = w.Write(data)
		}))
	defer srv.Close()

	seed := func(t *testing.T, metaETag string) string {
		t.Helper()
		dir := t.TempDir()
		curr := filepath.Join(dir, "00001.ts")
		if err := os.WriteFile(curr+".part", data[:20000], 0o666); err != nil {
			t.Fatal(err)
		}
		meta := srv.URL + "/00001.ts\n" + metaETag + "\n"
		if err := os.WriteFile(curr+".part.meta", []byte(meta), 0o666); err != nil {
			t.Fatal(err)
		}
		return curr
	}

	t.Run("matching validator resumes", func(t *testing.T) {
		withRestoredGlobals(t)
		curr := seed(t, etag)
		if !tryDownload(srv.URL+"/00001.ts", "", curr) {
			t.Fatal("matching If-Range must resume and complete")
		}
		got, err := os.ReadFile(curr)
		if err != nil {
			t.Fatalf("read finalized segment: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatal("finalized content differs from the served body")
		}
	})

	t.Run("stale validator restarts", func(t *testing.T) {
		withRestoredGlobals(t)
		curr := seed(t, `"v0"`)
		if !tryDownload(srv.URL+"/00001.ts", "", curr) {
			t.Fatal("stale If-Range must fall back to a full download")
		}
		got, err := os.ReadFile(curr)
		if err != nil {
			t.Fatalf("read finalized segment: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatal("finalized content differs from the served body")
		}
	})
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

// A server that stops mid-body must be aborted by the inactivity guard
// instead of hanging forever, now that the client no longer has a total
// wall-clock Timeout.
func TestInactivityTimeoutAbortsStalledBody(t *testing.T) {
	withRestoredGlobals(t)
	reqTimeout = 150 * time.Millisecond
	data := resumeBody(50000)
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			_, _ = w.Write(data[:20000])
			// Never send the rest; block the handler so the stream stalls
			// until the test is done, letting srv.Close() return.
			<-released
		}))
	defer func() {
		close(released)
		srv.Close()
	}()

	dir := t.TempDir()
	curr := filepath.Join(dir, "00001.ts")
	start := time.Now()
	if tryDownload(srv.URL+"/00001.ts", "", curr) {
		t.Fatal("a stalled body must not finalize as a successful segment")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stalled download took %v; inactivity guard should abort in ~150ms", elapsed)
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

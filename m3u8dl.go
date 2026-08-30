// m3u8dl is a multi-threaded m3u8 video downloader.
//
// Features: parse m3u8, download ts segments concurrently (AES encryption,
// nested m3u8, ad/duplicate segment deduplication, resume from breakpoint),
// prefer system ffmpeg for lossless merge to mp4, fall back to built-in merge
// when ffmpeg is unavailable.
//
// The CLI is built with cobra: semantic long flags with short aliases like -u,
// supports --help/--json/--version/auto-completion, friendly to both humans
// and LLM/Agent workflows.

package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

const (
	// PROGRESS_WIDTH is the length of the progress bar
	PROGRESS_WIDTH = 20
	// TS_NAME_TEMPLATE is the filename template for ts segments
	TS_NAME_TEMPLATE = "%05d.ts"
	// SYNC_BYTE is the MPEG-TS sync byte 0x47
	SYNC_BYTE = 0x47
	// TS_DOWNLOAD_RETRY is the max download retry times per ts segment
	TS_DOWNLOAD_RETRY = 5
)

// Build-time variables, injected by goreleaser / ldflags, e.g.:
//
//	-ldflags "-X 'main.version=1.2.3' -X 'main.commit=abc123' -X 'main.date=2026-08-30T00:00:00Z'"
//
// When unset they fall back to a "dev build" description so manual builds still
// report something meaningful for --version.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionString composes the human-readable version info shown by --version.
func versionString() string {
	if version == "dev" {
		return "dev build"
	}
	return fmt.Sprintf("%s (commit=%s, built=%s)", version, commit, date)
}

// ============================== CLI layer (cobra) ==============================

// runOptions collects all parameters for one execution, keeping core logic
// signatures stable while avoiding parameter passing through every layer.
// Core download functions read from the package-level opts below (including
// HTTP request config), consistent with the legacy flag global variables.
type runOptions struct {
	url         string
	urlListFile string // batch list file, one URL per line
	movieName   string
	savePath    string
	threads     int
	hostType    string
	timeoutSec  int
	cookie      string
	referer     string
	userAgent   string
	headers     []string
	purgeDup    bool
	insecure    bool
	autoClean   bool // whether to remove the ts directory after a successful merge
	jsonOut     bool // output machine-parsable JSON result
}

// opts is the runtime singleton config, filled once at the start of runE.
var opts runOptions

// The global HTTP request config, read directly by get() (keeps old logic).
var (
	reqTimeout = 120 * time.Second
	reqHeaders = map[string]string{
		"Connection":      "keep-alive",
		"Accept":          "*/*",
		"Accept-Encoding": "*",
		"Accept-Language": "zh-CN,zh;q=0.9, en;q=0.8, de;q=0.7, *;q=0.5",
	}
	insecureSkipVerify = false
	curUA              = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

// logger writes to stderr: normal business results go to stdout while logs,
// progress and warnings go to stderr, so LLM/Agent only needs to parse the
// structured stdout output.
var logger = log.New(os.Stderr, "", log.LstdFlags)

// exitError carries an exit code so cobra's Execute can return non-zero status.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func failCode(code int, format string, args ...any) error {
	return &exitError{code: code, msg: fmt.Sprintf(format, args...)}
}

// resultJSON is the JSON structure of the final result (printed to stdout with --json).
type resultJSON struct {
	OK       bool    `json:"ok"`
	Path     string  `json:"path,omitempty"`
	Duration float64 `json:"duration_sec,omitempty"`
	URL      string  `json:"url,omitempty"`
	Name     string  `json:"name,omitempty"`
	Error    string  `json:"error,omitempty"`
	Mode     string  `json:"mode,omitempty"`
}

// newRootCmd builds the cobra root command.
func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "m3u8dl [flags] <m3u8-url> [outputName]",
		Short: "多线程 m3u8 视频下载器",
		Long: `m3u8dl 是一个多线程 m3u8 视频下载器。

支持：AES 加密、嵌套 m3u8(Master Playlist)、广告/重复切片去重、断点续传，
优先调用系统 ffmpeg 无损合并为 mp4，缺失时回退内置合并。

用法示例:
  m3u8dl -u https://example.com/index.m3u8
  m3u8dl -u https://example.com/index.m3u8 -o 我的视频 -n 32
  m3u8dl --url https://example.com/index.m3u8 --threads 16 --json
  m3u8dl --list urls.txt --save-path /data/videos`,
		Args: cobra.MaximumNArgs(2),
		RunE: runRoot,
	}

	// Short aliases stay consistent with the old version, semantic long flags added.
	f := cmd.Flags()
	f.StringVarP(&opts.url, "url", "u", "", "m3u8 下载地址 (http(s)://url/xx/index.m3u8)")
	f.IntVarP(&opts.threads, "threads", "n", 24, "下载线程数")
	f.StringVar(&opts.hostType, "host-type", "v2", "host 解析方式 (v1|v2|auto；见 --help) [旧参数 -ht]")
	f.StringVarP(&opts.movieName, "output", "o", "movie", "输出文件名（不带后缀，默认 movie）")
	f.StringVarP(&opts.cookie, "cookie", "c", "", "请求 Cookie")
	f.StringVarP(&opts.referer, "referer", "r", "", "Referer 请求头（默认取 m3u8 的 host）")
	f.IntVarP(&opts.timeoutSec, "timeout", "t", 120, "每个请求的超时秒数")
	f.StringVar(&opts.savePath, "save-path", "", "文件保存目录（默认当前目录） [旧参数 -sp]")
	f.StringVar(&opts.userAgent, "user-agent", curUA, "User-Agent 请求头 [旧参数 -ua]")
	f.BoolVarP(&opts.insecure, "insecure", "s", false, "允许不安全的 TLS 请求")
	f.BoolVar(&opts.purgeDup, "purge-dup", false, "广告/重复切片去重 [旧参数 -pd]")
	f.BoolVarP(&opts.autoClean, "clean-ts", "", true, "合并成功后自动清除 ts 目录")
	f.BoolVarP(&opts.jsonOut, "json", "j", false, "以 JSON 输出结构化结果（利于程序/LLM 解析）")
	f.StringVarP(&opts.urlListFile, "list", "l", "", "批量下载列表文件（每行一个 m3u8 地址）")
	f.StringArrayVarP(&opts.headers, "header", "H", nil, "自定义请求头，可重复，格式 \"Key: Value\"")

	// Set a custom --version output format using the injected/fallback version.
	cmd.Version = versionString()
	cmd.SetVersionTemplate("m3u8dl version {{.Version}}\n")

	return cmd
}

// runRoot is the cobra entry callback: populates runtime config and runs the download.
func runRoot(cmd *cobra.Command, args []string) error {
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Positional argument fallback: first is URL, second is output name (legacy compat).
	if opts.url == "" && len(args) > 0 {
		opts.url = args[0]
	}
	if opts.movieName == "movie" && len(args) > 1 {
		opts.movieName = args[1]
	}
	if opts.url == "" && opts.urlListFile == "" {
		return failCode(1, "缺少 m3u8 地址：请用 -u <url> 指定，或 --list <文件> 批量，或直接用 -h 查看帮助")
	}

	// Persist request config to globals in one go (read directly by get()).
	reqTimeout = time.Duration(opts.timeoutSec) * time.Second
	curUA = opts.userAgent
	insecureSkipVerify = opts.insecure
	if opts.referer == "" {
		opts.referer = getHost(opts.url, "v2")
	}
	reqHeaders["Referer"] = opts.referer
	if opts.cookie != "" {
		reqHeaders["Cookie"] = opts.cookie
	}
	for _, h := range opts.headers {
		parseHeader(h)
	}

	start := time.Now()
	var result resultJSON
	var err error

	switch {
	case opts.urlListFile != "":
		result, err = runBatch(opts.urlListFile, start)
	default:
		result, err = runSingle(opts, start)
	}

	// Propagate errors upward uniformly (also prints friendly message when not --json).
	if err != nil {
		return err
	}

	if opts.jsonOut {
		printJSON(result)
	} else {
		if result.OK && result.Path != "" {
			fmt.Printf("\n[Success] 下载保存路径：%s | 共耗时: %6.2fs\n", result.Path, result.Duration)
		} else if !result.OK && result.Error != "" {
			fmt.Fprintf(os.Stderr, "\n[Failed] %s\n", result.Error)
		}
	}
	return nil
}

// printJSON encodes and prints machine-parsable JSON to stdout.
func printJSON(r resultJSON) {
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
}

// runSingle performs a full download of a single m3u8.
func runSingle(o runOptions, start time.Time) (resultJSON, error) {
	pwdDir, err := os.Getwd()
	if err != nil {
		return resultJSON{}, failCode(1, "获取当前目录失败: %v", err)
	}
	saveDir := pwdDir
	if o.savePath != "" {
		saveDir = o.savePath
	}
	path := processOne(o.url, o.movieName, o.threads, o.hostType, saveDir, o.autoClean, o.purgeDup)
	if path == "" {
		return resultJSON{}, failCode(1, "下载失败（ts 片段缺失或目录无效），ts 目录已保留可重跑续传")
	}
	return resultJSON{
		OK: true, Path: path, Duration: time.Since(start).Seconds(),
		Mode: "single", Name: o.movieName, URL: o.url,
	}, nil
}

// runBatch performs batch downloads from a list file (one m3u8 URL per line).
func runBatch(listFile string, start time.Time) (resultJSON, error) {
	data, err := os.ReadFile(listFile)
	if err != nil {
		return resultJSON{}, failCode(1, "读取列表失败: %v", err)
	}
	urls := strings.Fields(string(data))
	if len(urls) == 0 {
		return resultJSON{}, failCode(1, "列表为空")
	}
	saveDir, _ := os.Getwd()
	if opts.savePath != "" {
		saveDir = opts.savePath
	}
	success, skipped, failed := 0, 0, 0
	failures := []string{}
	for i, u := range urls {
		outName := fmt.Sprintf("%s_%03d", opts.movieName, i+1)
		if exists, _ := pathExists(filepath.Join(saveDir, outName+".mp4")); exists {
			skipped++
			continue
		}
		if processOne(u, outName, opts.threads, opts.hostType, saveDir, opts.autoClean, opts.purgeDup) != "" {
			success++
		} else {
			failed++
			failures = append(failures, u)
		}
	}
	msg := fmt.Sprintf("批量完成: 成功=%d 跳过=%d 失败=%d", success, skipped, failed)
	if failed > 0 {
		logger.Printf("%s 失败列表=%v", msg, failures)
		return resultJSON{}, failCode(1, "%s", msg)
	}
	return resultJSON{OK: true, Error: msg, Mode: "batch", Duration: time.Since(start).Seconds()}, nil
}

// legacyCompat maps legacy multi-character single-dash flags (-ht/-sp/-pd/-ua, etc.).
// pflag shorthand can only be a single character, so these historical flags are
// mapped here to equivalent semantic long flags, letting old scripts keep working
// unchanged. Value-required flags explicitly assigned (with '=') are kept as-is.
type legacyCompat struct {
	old  string // legacy short flag name (without leading -)
	long string // corresponding new long flag name (without leading --)
}

var legacyFlags = []legacyCompat{
	{"ht", "host-type"},
	{"sp", "save-path"},
	{"pd", "purge-dup"},
	{"ua", "user-agent"},
}

var legacyNoValueFlags = []string{
	"pd", // -pd is a boolean switch, takes no value
}

// rewriteLegacyArgs rewrites legacy multi-character single-dash args to long flags.
// A single dash is turned into a double dash: -ht v1 -> --host-type v1; -pd -> --purge-dup.
func rewriteLegacyArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Only handle single-dash, non "--" forms
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 1 {
			body := arg[1:]
			// Inline assignment form: -ht=v1
			if eq := strings.Index(body, "="); eq > 0 {
				key, val := body[:eq], body[eq+1:]
				if m := matchLegacy(key); m != "" {
					out = append(out, "--"+m+"="+val)
					continue
				}
				out = append(out, arg)
				continue
			}
			// Plain arg: -ht v1
			if m := matchLegacy(body); m != "" && !isLegacyNoValue(body) {
				out = append(out, "--"+m)
				// Also append the next token (value is required)
				if i+1 < len(args) {
					i++
					out = append(out, args[i])
				}
				continue
			}

			if m := matchLegacy(body); m != "" && isLegacyNoValue(body) {
				out = append(out, "--"+m)
				continue
			}
		}
		out = append(out, arg)
	}
	return out
}

func matchLegacy(key string) string {
	for _, c := range legacyFlags {
		if c.old == key {
			return c.long
		}
	}
	return ""
}

func isLegacyNoValue(key string) bool {
	for _, s := range legacyNoValueFlags {
		if s == key {
			return true
		}
	}
	return false
}

// main runs cobra, non-zero exit codes are for Agent/scripts to judge.
// It rewrites legacy args via rewriteLegacyArgs before execution.
func main() {
	cmd := newRootCmd()
	args := rewriteLegacyArgs(os.Args[1:])
	cmd.SetArgs(args)
	// Silence cobra's default Error+Usage printing; output handled below uniformly (JSON or stderr only)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err != nil {
		if opts.jsonOut {
			printJSON(resultJSON{OK: false, Error: err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(1)
	}
}

// =============================================================================
// ============================================================
// Core download logic below (identical to legacy version, except Run()/flag
// related parts were replaced by the cobra layer above).
// =============================================================================

// processOne is the full download flow for a single m3u8: parse -> download ->
// verify -> dedup -> merge. Returns the absolute path of the merged mp4; an empty
// string on failure. Shared by both single and batch tasks.
func processOne(m3u8Url, movieName string, maxGoroutines int, hostType, pwd string, autoClearFlag, purgeDup bool) string {
	m3u8URL, err := url.Parse(m3u8Url)
	if err != nil {
		logger.Printf("[Error] 解析 m3u8 地址失败: %v", err)
		return ""
	}
	downloadDir := filepath.Join(pwd, movieName)
	if isExist, _ := pathExists(downloadDir); !isExist {
		os.MkdirAll(downloadDir, os.ModePerm)
	}
	m3u8Body := getM3u8Body(m3u8URL.String())
	tsList := getTsList(m3u8Body, m3u8URL, hostType)
	logger.Printf("待下载 ts 文件数量: %d", len(tsList))
	downloader(tsList, maxGoroutines, downloadDir)
	// Verify each ts segment; keep the dir for a re-run to resume if any missing
	if missing := checkTsDownDir(downloadDir, len(tsList)); len(missing) > 0 {
		logger.Printf("[Failed] 以下 %d 个 ts 片段缺失：%v", len(missing), missing)
		logger.Printf("[提示] ts 目录已保留，请重新运行同一命令自动续传缺失片段。")
		return ""
	}
	if purgeDup {
		purgeAllDuplicates(downloadDir)
	}
	if !dirHasTs(downloadDir) {
		logger.Printf("[Failed] 下载目录无有效 ts 文件，请检查url地址有效性")
		return ""
	}
	moviePath := mergeWithFFmpeg(downloadDir, movieName, pwd)
	if moviePath == "" {
		moviePath = mergeTs(downloadDir, len(tsList))
	}
	if autoClearFlag && moviePath != "" {
		os.RemoveAll(downloadDir)
	}
	return moviePath
}

// getHost returns the host of the m3u8 URL.
func getHost(Url, ht string) (host string) {
	u, err := url.Parse(Url)
	if err != nil {
		return ""
	}
	switch ht {
	case "v1":
		host = u.Scheme + "://" + u.Host + filepath.Dir(u.EscapedPath())
	default: // v2 / auto
		host = u.Scheme + "://" + u.Host
	}
	return
}

// getM3u8Body fetches and returns the body of the m3u8 URL.
func getM3u8Body(Url string) string {
	res := get(Url)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return ""
	}
	return string(body)
}

// get sends a GET request with unified UA/headers/timeout; non-2xx is treated as
// a failure and returns an empty response.
func get(url string) *http.Response {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return &http.Response{
			StatusCode: 599,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
		}
	}
	req.Header.Set("User-Agent", curUA)
	for k, v := range reqHeaders {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: reqTimeout}
	if insecureSkipVerify {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	res, err := client.Do(req)
	if err != nil {
		// Network/DNS errors do not panic: return a failed response with an empty
		// body so a bad URL only affects itself, not the whole batch or resume flow.
		logger.Printf("[warn] GET 失败 %s: %v", url, err)
		return &http.Response{
			StatusCode: 599, // custom error code, callers treat as non-2xx
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
		}
	}
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		res.Body.Close()                               // release the real body
		res.Body = io.NopCloser(strings.NewReader("")) // treat non-2xx as no body
	}
	return res
}

// parseHeader parses "-header \"Key: Value\"" into request headers, supports
// overriding any HTTP header.
func parseHeader(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	pos := strings.Index(line, ":")
	if pos <= 0 {
		logger.Printf("[warn] 无效 header: %s", line)
		return
	}
	k := strings.TrimSpace(line[:pos])
	v := strings.TrimSpace(line[pos+1:])
	if strings.EqualFold(k, "user-agent") {
		curUA = v
	}
	reqHeaders[k] = v
}

// getFullUrl resolves the full download URL for ts/key segments.
// [merged ycq3/bkkkd] Supports absolute URLs, "/root-relative paths", and normal
// relative paths (including multi-level ../ jumps).
func getFullUrl(line, host string, baseURL *url.URL) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		return line
	}
	if strings.HasPrefix(line, "/") {
		return host + line
	}
	u, err := url.Parse(line)
	if err != nil {
		return host + "/" + line
	}
	return baseURL.ResolveReference(u).String()
}

// getTsList parses the download URLs of ts segments.
// [merged ycq3/bkkkd] Supports nested m3u8 (Master Playlist): recursively expands
// child lists when a .m3u8 line is encountered.
func getTsList(body string, baseURL *url.URL, hostType string) (tsList []TsInfo) {
	index := 1
	return getTsListInternal(body, baseURL, hostType, &index)
}

func getTsListInternal(body string, baseURL *url.URL, hostType string, index *int) (tsList []TsInfo) {
	host := getHost(baseURL.String(), hostType)
	var altHost string
	switch hostType {
	case "auto":
		host = getHost(baseURL.String(), "v1")
		altHost = getHost(baseURL.String(), "v2") // v2 can't resolve relative paths, only meaningful for absolute/root
	case "v1":
		altHost = getHost(baseURL.String(), "v2")
	default:
		altHost = getHost(baseURL.String(), "v1")
	}

	curKey := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Cache the current encryption key (per-ts key)
		if strings.Contains(line, "#EXT-X-KEY") && strings.Contains(line, "URI") {
			start := strings.Index(line, `URI="`)
			if start >= 0 {
				start += 5
				if end := strings.Index(line[start:], `"`); end != -1 {
					keyURL := getFullUrl(line[start:start+end], host, baseURL)
					keyAlt := getFullUrl(line[start:start+end], altHost, baseURL)
					if key, ok := fetchKey(keyURL, keyAlt); ok {
						curKey = key
					}
				}
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		// Nested m3u8: Master Playlist points to a second-level list
		if strings.Contains(line, ".m3u8") {
			secURL := getFullUrl(line, host, baseURL)
			secBody := getM3u8Body(secURL)
			secURLParsed, _ := url.Parse(secURL)
			sub := getTsListInternal(secBody, secURLParsed, hostType, index)
			tsList = append(tsList, sub...)
			continue
		}
		curIdx := *index
		*index++
		tsList = append(tsList, TsInfo{
			Name:   fmt.Sprintf(TS_NAME_TEMPLATE, curIdx),
			Url:    getFullUrl(line, host, baseURL),
			AltUrl: getFullUrl(line, altHost, baseURL),
			Key:    curKey, // per-ts key
		})
	}
	return
}

// fetchKey fetches the AES key content (falls back to the alternate URL on failure).
func fetchKey(url, altURL string) (string, bool) {
	for _, u := range []string{url, altURL} {
		if u == "" {
			continue
		}
		res := get(u)
		if res.StatusCode >= 200 && res.StatusCode < 400 {
			defer res.Body.Close()
			b, _ := io.ReadAll(res.Body)
			return string(b), true
		}
		res.Body.Close()
	}
	return "", false
}

// TsInfo holds the download URL and filename of a ts file.
// [merged ycq3/bkkkd] Added Key field: each ts can carry its own AES key (different
// segments in nested m3u8 may use different keys).
type TsInfo struct {
	Name   string
	Url    string
	AltUrl string // [merged 0penMax] alternate host retry URL when -ht=auto
	Key    string
}

// downloader downloads the m3u8 segments, limiting concurrency with a semaphore.
// [merged wangguanyuan] Progress bar shows real-time speed; counters use atomic
// variables for concurrency safety.
func downloader(tsList []TsInfo, maxGoroutines int, downloadDir string) {
	isTTY := isTerminal(os.Stdout) && !opts.jsonOut // JSON mode or non-TTY: no progress bar
	tsLen := len(tsList)
	if tsLen == 0 {
		return
	}
	var downloadCount, totalBytes atomic.Int64
	startTs := time.Now()
	sem := make(chan struct{}, maxGoroutines)

	var wg sync.WaitGroup
	for _, ts := range tsList {
		wg.Add(1)
		go func(ts TsInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if downloadTsFile(ts, downloadDir) {
				downloadCount.Add(1)
				totalBytes.Add(fileSizeOf(filepath.Join(downloadDir, ts.Name)))
			}
			done := downloadCount.Load()
			if isTTY {
				DrawProgressBarWithSpeed("Downloading", float32(done)/float32(tsLen),
					int(done), tsLen, totalBytes.Load(), startTs)
			}
		}(ts)
	}
	wg.Wait()
	if isTTY {
		fmt.Println()
	}
}

// isTerminal reports whether stdout is a TTY (used to decide whether to render
// the progress bar).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeCharDevice != 0 {
		return true
	}
	return false
}

// downloadTsFile downloads and lands a single ts segment, trying in the order:
// primary host -> alternate host -> retries on failure.
func downloadTsFile(ts TsInfo, downloadDir string) bool {
	currPath := filepath.Join(downloadDir, ts.Name)
	if exists, _ := pathExists(currPath); exists {
		return true
	}

	// One attempt on the primary host
	if tryDownload(ts.Url, ts.Key, currPath) {
		return true
	}
	// Alternate host (automatic fallback)
	if ts.AltUrl != "" && ts.AltUrl != ts.Url && tryDownload(ts.AltUrl, ts.Key, currPath) {
		return true
	}
	// Remaining retries on the primary host (permanent failures like 403 won't retry)
	for i := 1; i < TS_DOWNLOAD_RETRY; i++ {
		if tryDownload(ts.Url, ts.Key, currPath) {
			return true
		}
	}
	return false
}

// tryDownload performs one download attempt and returns whether it succeeded.
func tryDownload(rawURL, key, currPath string) bool {
	res := get(rawURL)
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		res.Body.Close()
		return false
	}
	ok := writeTSFromResponse(res, key, currPath)
	res.Body.Close()
	return ok
}

// writeTSFromResponse writes the cleaned response body to currPath.
// It lands via a .part temp file: when unencrypted, SyncBytes are stripped
// streaming-wise to avoid holding everything in memory; on retry failure the
// .part is cleaned up so partial files never pollute the real one.
func writeTSFromResponse(res *http.Response, key, currPath string) bool {
	partPath := currPath + ".part"
	out, err := os.Create(partPath)
	if err != nil {
		return false
	}
	defer os.Remove(partPath)

	written, err := io.Copy(out, res.Body)
	out.Close()
	if err != nil || written == 0 {
		return false
	}
	// Check length: when Content-Length is trustworthy, a short packet means incomplete
	if cl := res.Header.Get("Content-Length"); cl != "" {
		if want, _ := strconv.ParseInt(cl, 10, 64); want > 0 && written < want {
			return false
		}
	}

	// Decrypt + strip leading junk
	var decErr error
	if key != "" {
		decErr = decryptFileTo(partPath, currPath, []byte(key))
	} else {
		decErr = stripLeadingJunkFile(partPath, currPath)
	}
	return decErr == nil
}

// decryptFileTo decrypts an AES segment (CBC) and writes it to the target file
// after stripping leading SyncByte junk.
func decryptFileTo(srcPath, dstPath string, key []byte) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	plain, err := AesDecrypt(data, key)
	if err != nil {
		return fmt.Errorf("AES 解密失败: %w", err)
	}
	return os.WriteFile(dstPath, stripLeadingJunk(plain), 0o666)
}

// stripLeadingJunkFile streams the file into the target while stripping junk
// bytes before the leading SyncByte 0x47.
// [modify: 2020-08-13 fixed the unplayable issue when merging ts with SyncByte]
func stripLeadingJunkFile(srcPath, dstPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, 64*1024)
	started := false
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if !started {
				if idx := bytes.IndexByte(buf[:n], SYNC_BYTE); idx >= 0 {
					if _, werr := out.Write(buf[idx:n]); werr != nil {
						return werr
					}
					started = true
				}
			} else if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// stripLeadingJunk returns a slice with leading junk before the first SyncByte
// removed (in-memory version, used on the decryption path).
func stripLeadingJunk(data []byte) []byte {
	for i, b := range data {
		if b == SYNC_BYTE {
			return data[i:]
		}
	}
	return data
}

// fileSizeOf returns the byte size of a file (used for speed stats).
func fileSizeOf(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

// checkTsDownDir verifies each ts segment and returns the list of missing files.
// [merged nilarcs] Failed segments keep the directory for resume from breakpoint.
func checkTsDownDir(dir string, expected int) (missing []string) {
	for i := 0; i < expected; i++ {
		name := fmt.Sprintf(TS_NAME_TEMPLATE, i+1)
		if exists, _ := pathExists(filepath.Join(dir, name)); !exists {
			missing = append(missing, name)
		}
	}
	return
}

// dirHasTs loosely verifies that the directory contains at least one ts file.
// [merged vjsdhyygy] Used as the final check after ad dedup (indices may have gaps).
func dirHasTs(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ts") {
			return true
		}
	}
	return false
}

// purgeAllDuplicates removes duplicate ad segments: any MD5-duplicate files are
// all physically deleted (merged from vjsdhyygy/m3u8-downloader).
func purgeAllDuplicates(downloadDir string) {
	logger.Printf("[校验] 正在扫描重复/广告切片...")
	hashCount := make(map[string]int)
	hashToFileList := make(map[string][]string)

	entries, _ := os.ReadDir(downloadDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ts") {
			continue
		}
		path := filepath.Join(downloadDir, e.Name())
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		h := md5.New()
		_, _ = io.Copy(h, file)
		file.Close()
		sha := hex.EncodeToString(h.Sum(nil))
		hashCount[sha]++
		hashToFileList[sha] = append(hashToFileList[sha], path)
	}

	delCount := 0
	for sha, count := range hashCount {
		if count > 1 {
			logger.Printf("\n[发现重复/广告] 内容哈希 %s 出现 %d 次，正在执行全数剔除...", sha[:8], count)
			for _, p := range hashToFileList[sha] {
				os.Remove(p)
				delCount++
			}
		}
	}
	logger.Printf("[净化完成] 共剔除 %d 个异常切片。", delCount)
}

// mergeWithFFmpeg calls system ffmpeg to do a lossless concat merge.
// (merged from vjsdhyygy/m3u8-downloader)
func mergeWithFFmpeg(downloadDir, movieName, savePath string) string {
	listPath := filepath.Join(downloadDir, "filelist.txt")
	listFile, err := os.Create(listPath)
	if err != nil {
		return ""
	}

	hasTs := false
	entries, _ := os.ReadDir(downloadDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ts") {
			continue
		}
		listFile.WriteString(fmt.Sprintf("file '%s'\n", e.Name()))
		hasTs = true
	}
	listFile.Close()
	if !hasTs {
		return ""
	}

	outputMp4 := filepath.Join(savePath, movieName+".mp4")
	// -fflags +genpts regenerates timestamps, fixing A/V sync after removing ads
	cmd := exec.Command("ffmpeg", "-f", "concat", "-safe", "0",
		"-i", listPath, "-c", "copy", "-fflags", "+genpts", "-y", outputMp4)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		logger.Printf("[FFmpeg Error]: %s", errOut.String())
		return ""
	}
	return outputMp4
}

// mergeTs merges the ts files (built-in fallback: streaming merge via io.Copy).
// [merged nilarcs] Merges in known order 1..n to guarantee segment ordering.
func mergeTs(downloadDir string, expected int) string {
	mvName := downloadDir + ".mp4"
	outMv, err := os.Create(mvName)
	if err != nil {
		return ""
	}
	defer outMv.Close()
	for i := 0; i < expected; i++ {
		path := filepath.Join(downloadDir, fmt.Sprintf(TS_NAME_TEMPLATE, i+1))
		if exists, _ := pathExists(path); !exists {
			continue // skip missing segments (already flagged by checkTsDownDir)
		}
		in, err := os.Open(path)
		if err != nil {
			continue
		}
		_, _ = io.Copy(outMv, in)
		in.Close()
	}
	_ = outMv.Sync()
	return mvName
}

// DrawProgressBarWithSpeed renders the progress bar (with real-time speed during
// the download phase). [merged wangguanyuan] Additionally shows x/y count and MB/s.
func DrawProgressBarWithSpeed(prefix string, proportion float32, done, total int, totalBytes int64, startTs time.Time) {
	width := PROGRESS_WIDTH
	pos := int(proportion * float32(width))
	speed := float64(0)
	if elapsed := time.Since(startTs).Seconds(); elapsed > 0 {
		speed = float64(totalBytes) / 1024 / 1024 / elapsed
	}
	s := fmt.Sprintf("\r[%s] %s%*s %6.2f%%\t%d/%d\t%5.2f MB/s\t",
		prefix, strings.Repeat("■", pos), width-pos, "", proportion*100, done, total, speed)
	fmt.Print(s)
}

// DrawProgressBar renders a simple progress bar (merge phase version).
func DrawProgressBar(prefix string, proportion float32, width int, suffix ...string) {
	pos := int(proportion * float32(width))
	s := fmt.Sprintf("[%s] %s%*s %6.2f%% \t%s",
		prefix, strings.Repeat("■", pos), width-pos, "", proportion*100, strings.Join(suffix, ""))
	fmt.Print("\r" + s)
}

// ============================== File utilities ==============================
// pathExists reports whether a file or directory exists.
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// ============================== Encryption helpers ==============================
// PKCS7UnPadding removes PKCS7 padding (with guards against empty data and
// invalid padding).
func PKCS7UnPadding(origData []byte) []byte {
	length := len(origData)
	if length == 0 {
		return origData
	}
	unpadding := int(origData[length-1])
	if length < unpadding {
		return origData
	}
	return origData[:(length - unpadding)]
}

// AesDecrypt does AES-CBC decryption; when IV is absent it reuses the first 16
// bytes of the key (consistent with the m3u8 standard).
// [merged Orochi-Adde] Length validation: errors on ciphertext that is too short
// or not a multiple of the block size to avoid CryptBlocks panics.
func AesDecrypt(crypted, key []byte, ivs ...[]byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	if len(crypted) < blockSize {
		return nil, fmt.Errorf("密文过短")
	}
	if len(crypted)%blockSize != 0 {
		return nil, fmt.Errorf("密文长度不是区块大小的整数倍")
	}
	var iv []byte
	if len(ivs) == 0 {
		iv = key
	} else {
		iv = ivs[0]
	}
	blockMode := cipher.NewCBCDecrypter(block, iv[:blockSize])
	origData := make([]byte, len(crypted))
	blockMode.CryptBlocks(origData, crypted)
	if len(origData) > 0 {
		origData = PKCS7UnPadding(origData)
	}
	return origData, nil
}

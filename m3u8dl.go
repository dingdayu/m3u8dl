// Go 多线程 m3u8 视频下载器。
//
// 功能：解析 m3u8，多线程下载 ts 切片（支持 AES 加密、嵌套 m3u8、广告切片去重、
// 断点续传），优先调用系统 ffmpeg 无损合并为 mp4，缺失时回退内置合并。
//
// CLI 基于 cobra，长选项语义化命名并保留 -u 等短别名，支持 --help/--json/
// --version/自动补全，对人与 LLM/Agent 均友好。

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
	// PROGRESS_WIDTH 进度条长度
	PROGRESS_WIDTH = 20
	// TS_NAME_TEMPLATE ts视频片段命名规则
	TS_NAME_TEMPLATE = "%05d.ts"
	// SYNC_BYTE MPEG-TS 同步字节 0x47
	SYNC_BYTE = 0x47
	// TS_DOWNLOAD_RETRY 单个 ts 下载最大重试次数
	TS_DOWNLOAD_RETRY = 5
)

// version 可编译期注入：-ldflags "-X main.version=1.0.0"
var version = "dev"

// ============================== CLI 层 (cobra) ==============================

// runOptions 收集本次执行的全部参数，避免层层透传的同时保持核心逻辑签名稳定。
// 核心下载函数仍通过读取本全局 opts 完成（含 HTTP 请求配置），与旧版 flag 全局变量一致。
type runOptions struct {
	url         string
	urlListFile string // 批量列表文件，每行一个地址
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
	autoClean   bool // 合并成功后是否删除 ts 目录
	jsonOut     bool // 输出机器可解析的 JSON 结果
}

// opts 为运行期单例配置，runE 开头一次性填充。
var opts runOptions

// httpOpts 复用全局 HTTP 请求配置，get() 直接读取（保持旧逻辑）。
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

// logger 统一走 stderr：正常业务结果走 stdout，日志/进度/告警走 stderr，
// 便于 LLM/Agent 只解析 stdout 的结构化结果。
var logger = log.New(os.Stderr, "", log.LstdFlags)

// exitError 携带退出码，供 cobra 的 Execute 返回非零状态。
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func failCode(code int, format string, args ...any) error {
	return &exitError{code: code, msg: fmt.Sprintf(format, args...)}
}

// resultJSON 最终下载结果的 JSON 结构（--json 时输出到 stdout）。
type resultJSON struct {
	OK       bool    `json:"ok"`
	Path     string  `json:"path,omitempty"`
	Duration float64 `json:"duration_sec,omitempty"`
	URL      string  `json:"url,omitempty"`
	Name     string  `json:"name,omitempty"`
	Error    string  `json:"error,omitempty"`
	Mode     string  `json:"mode,omitempty"`
}

// newRootCmd 构建 cobra 根命令。
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

	// 短别名与旧版保持一致，同时提供语义化长参数。
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

	// 设置自定义 --version 输出格式。
	cmd.Version = version
	cmd.SetVersionTemplate("m3u8dl version {{.Version}}\n")

	return cmd
}

// runRoot 是 cobra 的入口回调：负责填充运行期配置并执行下载。
func runRoot(cmd *cobra.Command, args []string) error {
	runtime.GOMAXPROCS(runtime.NumCPU())

	// 位置参数兜底：第一个为 URL，第二个为输出名（旧版兼容）。
	if opts.url == "" && len(args) > 0 {
		opts.url = args[0]
	}
	if opts.movieName == "movie" && len(args) > 1 {
		opts.movieName = args[1]
	}
	if opts.url == "" && opts.urlListFile == "" {
		return failCode(1, "缺少 m3u8 地址：请用 -u <url> 指定，或 --list <文件> 批量，或直接用 -h 查看帮助")
	}

	// 请求配置一次性落地到全局（get() 直接读取）。
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

	// 统一错误向上传递（非 --json 时也打印友好消息）。
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

// printJSON 编码并输出机器可解析的 JSON 到 stdout。
func printJSON(r resultJSON) {
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
}

// runSingle 执行单个 m3u8 的完整下载。
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

// runBatch 执行列表批量下载（每行一个 m3u8 地址）。
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

// legacyCompat 兼容旧版多字符单横线参数（-ht/-sp/-pd/-ua 等）。
// pflag 的 shorthand 只能是单个字符，故这些历史参数在此映射为等价的语义化长参数，
// 使旧脚本无需修改即可继续使用。值为必填的参数若被显式赋值（含 '='），原样保留。
type legacyCompat struct {
	old  string // 旧短参数名（不含前导 -）
	long string // 对应的新长参数名（不含前导 --）
}

var legacyFlags = []legacyCompat{
	{"ht", "host-type"},
	{"sp", "save-path"},
	{"pd", "purge-dup"},
	{"ua", "user-agent"},
}

var legacyNoValueFlags = []string{
	"pd", // -pd 为布尔开关，无需值
}

// rewriteLegacyArgs 将历史多字符单横线参数改写为长参数形式。
// 单横线会被补成双横线：-ht v1  -> --host-type v1；-pd  -> --purge-dup。
func rewriteLegacyArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// 只处理单横线非 "--" 形式
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 1 {
			body := arg[1:]
			// 形如 -ht=v1（内联赋值）
			if eq := strings.Index(body, "="); eq > 0 {
				key, val := body[:eq], body[eq+1:]
				if m := matchLegacy(key); m != "" {
					out = append(out, "--"+m+"="+val)
					continue
				}
				out = append(out, arg)
				continue
			}
			// 普通参数：-ht v1
			if m := matchLegacy(body); m != "" && !isLegacyNoValue(body) {
				out = append(out, "--"+m)
				// 把下一个 token 也加入（值为必填）
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

// main 使用 cobra 执行，非零错误码供 Agent/脚本判断。
// 执行前先 rewriteLegacyArgs 兼容旧版多字符短参数。
func main() {
	cmd := newRootCmd()
	args := rewriteLegacyArgs(os.Args[1:])
	cmd.SetArgs(args)
	// 静默 cobra 默认的 Error+Usage 打印，由下方统一处理输出（JSON 或否则仅 stderr）
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
// 以下为核心下载逻辑，与旧版一致（仅 Run()/flag 相关部分被上方 cobra 层替换）。
// =============================================================================

// processOne 单个 m3u8 的完整下载流程：解析→下载→校验→去重→合并。
// 返回合并后的 mp4 绝对路径；失败返回空字符串。单任务与批量任务共用。
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
	// 逐个校验 ts 片段；缺失则保留目录供二次运行续传
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

// getHost 获取m3u8地址的host
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

// getM3u8Body 获取m3u8地址的内容体
func getM3u8Body(Url string) string {
	res := get(Url)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return ""
	}
	return string(body)
}

// get 发送 GET 请求，统一写入 UA/头/超时；非 2xx 视为失败返回空响应
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
		// 网络/DNS 等错误不 panic：返回一个空 body 的失败响应，
		// 使单个坏地址只影响自身，不影响整批任务或连传流程。
		logger.Printf("[warn] GET 失败 %s: %v", url, err)
		return &http.Response{
			StatusCode: 599, // 自定义错误码，调用方按非 2xx 处理
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
		}
	}
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		res.Body.Close()                               // 释放真实 body
		res.Body = io.NopCloser(strings.NewReader("")) // 非 2xx 视为无 body
	}
	return res
}

// parseHeader 解析 "-header \"Key: Value\"" 到请求头，支持任意 HTTP 头覆盖
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

// getFullUrl 统一解析 ts/key 的完整下载地址
// 【合并 ycq3/bkkkd】兼容绝对地址、"/根相对路径"、普通相对路径（含 ../ 多级跳转）
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

// getTsList 解析 ts 切片下载地址
// 【合并 ycq3/bkkkd】支持嵌套 m3u8(Master Playlist)：遇到 .m3u8 行递归展开子列表
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
		altHost = getHost(baseURL.String(), "v2") // v2 相对路径无法解析，仅对绝对/根路径有意义
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
		// 缓存当前加密 key（per-ts key）
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
		// 嵌套 m3u8：Master Playlist 指向二级列表
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

// fetchKey 拉取 AES 密钥内容（主地址失败时回退备用地址）
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

// TsInfo 用于保存 ts 文件的下载地址和文件名
// 【合并 ycq3/bkkkd】增加 Key 字段：每个 ts 可携带独立的 AES 密钥（嵌套 m3u8 不同片段可用不同 key）
type TsInfo struct {
	Name   string
	Url    string
	AltUrl string // 【合并 0penMax】-ht=auto 时的备用 host 重试地址
	Key    string
}

// downloader m3u8 下载器，使用信号量限制并发数
// 【合并 wangguanyuan】进度条带实时网速；计数使用原子变量保证并发安全
func downloader(tsList []TsInfo, maxGoroutines int, downloadDir string) {
	isTTY := isTerminal(os.Stdout) && !opts.jsonOut // JSON 模式或非 TTY 不画进度条
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

// isTerminal 判断 stdout 是否为 TTY（用于决定是否渲染进度条）。
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

// downloadTsFile 下载并落地单个 ts 片段，失败时按 主host → 备用host → 重试 的顺序尝试
func downloadTsFile(ts TsInfo, downloadDir string) bool {
	currPath := filepath.Join(downloadDir, ts.Name)
	if exists, _ := pathExists(currPath); exists {
		return true
	}

	// 主 host 一次尝试
	if tryDownload(ts.Url, ts.Key, currPath) {
		return true
	}
	// 备用 host（自动降级）
	if ts.AltUrl != "" && ts.AltUrl != ts.Url && tryDownload(ts.AltUrl, ts.Key, currPath) {
		return true
	}
	// 主 host 剩余重试（403 等永久失败不会进入重试）
	for i := 1; i < TS_DOWNLOAD_RETRY; i++ {
		if tryDownload(ts.Url, ts.Key, currPath) {
			return true
		}
	}
	return false
}

// tryDownload 执行一次下载尝试，返回是否成功
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

// writeTSFromResponse 将响应体清洗后写入 currPath。
// 通过 .part 临时文件落地：未加密时流式剥离 SyncByte，避免整体驻留内存；
// 重试失败时 .part 会被清理，不会残留半成品污染正式文件。
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
	// 校验长度：Content-Length 可信时，短包判定为下载不完全
	if cl := res.Header.Get("Content-Length"); cl != "" {
		if want, _ := strconv.ParseInt(cl, 10, 64); want > 0 && written < want {
			return false
		}
	}

	// 解密 + 剥离开头杂质
	var decErr error
	if key != "" {
		decErr = decryptFileTo(partPath, currPath, []byte(key))
	} else {
		decErr = stripLeadingJunkFile(partPath, currPath)
	}
	return decErr == nil
}

// decryptFileTo 解密 AES 片段（CBC），并剥离开头 SyncByte 杂质后写入目标文件
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

// stripLeadingJunkFile 流式剥离开头 SyncByte 0x47 前的杂质字节后写入目标文件
// 【modify: 2020-08-13 修复ts格式SyncByte合并不能播放问题】
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

// stripLeadingJunk 返回去除首个 SyncByte 之前杂质字节后的切片（内存版本，用于解密路径）
func stripLeadingJunk(data []byte) []byte {
	for i, b := range data {
		if b == SYNC_BYTE {
			return data[i:]
		}
	}
	return data
}

// fileSizeOf 获取文件字节数（用于速度统计）
func fileSizeOf(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

// checkTsDownDir 逐个校验 ts 片段，返回缺失文件列表
// 【合并 nilarcs】失败片段保留目录以便断点续传
func checkTsDownDir(dir string, expected int) (missing []string) {
	for i := 0; i < expected; i++ {
		name := fmt.Sprintf(TS_NAME_TEMPLATE, i+1)
		if exists, _ := pathExists(filepath.Join(dir, name)); !exists {
			missing = append(missing, name)
		}
	}
	return
}

// dirHasTs 宽松校验：目录中存在至少一个 ts 文件
// 【合并 vjsdhyygy】用于广告去重后（序号可能有空洞）的最终校验
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

// purgeAllDuplicates 广告切片暴力去重：只要 MD5 内容重复，全部物理删除
// （合并自 vjsdhyygy/m3u8-downloader）
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

// mergeWithFFmpeg 调用系统 ffmpeg 执行 concat 无损合并
// （合并自 vjsdhyygy/m3u8-downloader）
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
	// -fflags +genpts 重新生成时间戳，解决删除广告后的音画同步问题
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

// mergeTs 合并ts文件（内置回退方案：io.Copy 流式合并）
// 【合并 nilarcs】按已知序号 1..n 顺序合并，保证片段顺序正确
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
			continue // 缺失片段跳过（已由 checkTsDownDir 提示）
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

// DrawProgressBarWithSpeed 进度条（下载阶段带实时网速）
// 【合并 wangguanyuan】额外显示 x/y 计数与 MB/s 速度
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

// DrawProgressBar 进度条（合并阶段简单版）
func DrawProgressBar(prefix string, proportion float32, width int, suffix ...string) {
	pos := int(proportion * float32(width))
	s := fmt.Sprintf("[%s] %s%*s %6.2f%% \t%s",
		prefix, strings.Repeat("■", pos), width-pos, "", proportion*100, strings.Join(suffix, ""))
	fmt.Print("\r" + s)
}

// ============================== 文件工具 ==============================
// pathExists 判断文件或目录是否存在
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

// ============================== 加解密相关 ==============================
// PKCS7UnPadding 去除 PKCS7 填充（含空数据与非法填充防御）
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

// AesDecrypt AES-CBC 解密；IV 缺省时复用 key 前 16 字节（与 m3u8 标准一致）
// 【合并 Orochi-Adde】密度校验：密文过短或非区块整数倍则直接报错，避免 CryptBlocks panic
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

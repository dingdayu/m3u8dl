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
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

// Agent Skills (https://agentskills.io) bundled into the binary so users can
// install them with `m3u8dl skills install` instead of cloning the repo. The
// canonical sources live in .agents/skills/<name>/ (scripts/sync-skills.sh
// generates the .claude/skills mirror). The "all:" prefix is required so the
// dot-directory is embedded.
//
//go:embed all:.agents/skills
var embeddedSkills embed.FS

// skillsEmbedRoot is the embedded prefix that holds the skill folders.
const skillsEmbedRoot = ".agents/skills"

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
	autoClean   bool   // whether to remove the ts directory after a successful merge
	jsonOut     bool   // output machine-parsable JSON result
	rateLimit   string // aggregate speed limit, e.g. "2M" / "500KB" / "2" (bytes/s); empty = unlimited
}

// opts is the runtime singleton config, filled once at the start of runE.
var opts runOptions

// httpClient is the package-level shared HTTP client, rebuilt once by
// applyRequestConfig() whenever the timeout/insecure options change. Sharing
// one client (and therefore one transport) enables connection pooling and
// HTTP/2 multiplexing across all segment downloads; creating a fresh
// http.Transport per request would defeat both.
var httpClient = &http.Client{Transport: newTransport(false)}

// newTransport builds the shared transport. ForceAttemptHTTP2 is required to
// keep HTTP/2 negotiation alive when a custom TLSClientConfig is set (Go only
// auto-enables h2 on the default transport).
func newTransport(insecure bool) *http.Transport {
	tc := &tls.Config{InsecureSkipVerify: insecure}
	return &http.Transport{
		// Keep honouring HTTP(S)_PROXY/NO_PROXY: a nil Proxy would bypass
		// the environment entirely (http.DefaultTransport sets this).
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       tc,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// applyRequestConfig (re)builds the shared client to reflect the current
// insecure setting. Called once at startup from runRoot. The client carries
// no global wall-clock Timeout: a low --rate-limit may legitimately take
// longer than a fixed deadline to drain a segment, so stalls are instead
// bounded per-read by inactivity (see withInactivityTimeout), which excludes
// deliberate throttling sleep from the deadline.
func applyRequestConfig() {
	httpClient = &http.Client{
		Timeout:   0,
		Transport: newTransport(insecureSkipVerify),
	}
}

// withInactivityTimeout bounds each underlying Read by how long it may sit
// without returning data. It guards only NETWORK inactivity: a throttled
// download pauses between reads (token-bucket wait, outside this wrapper) so
// a low --rate-limit keeps working, while a genuinely stalled server is
// still aborted instead of hanging forever.
func withInactivityTimeout(r io.Reader, d time.Duration) io.Reader {
	return &inactivityTimeoutReader{r: r, d: d}
}

type inactivityTimeoutReader struct {
	r io.Reader
	d time.Duration
}

func (t *inactivityTimeoutReader) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := t.r.Read(p)
		ch <- result{n, err}
	}()
	timer := time.NewTimer(t.d)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-timer.C:
		// The underlying read may still be blocked; the caller's Close of the
		// response body unblocks it, and no further Read is issued on this
		// buffer after an error, so it is safe to return the timeout now.
		return 0, fmt.Errorf("download stalled: no data for %s", t.d)
	}
}

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
	f.StringVar(&opts.rateLimit, "rate-limit", "", "总下载速度上限，如 2M / 500KB / 200000（字节/秒，0 或空=不限速）")

	// Set a custom --version output format using the injected/fallback version.
	cmd.Version = versionString()
	cmd.SetVersionTemplate("m3u8dl version {{.Version}}\n")

	// Subcommand exposing the embedded Agent Skills to the user's coding agent.
	cmd.AddCommand(newSkillsCmd())

	return cmd
}

// skillAgentTargets maps an --agent preset to the directory (relative to the
// install root) where that agent product discovers project skills. ".agents"
// is the vendor-neutral location of the Agent Skills open standard
// (https://agentskills.io), scanned by VS Code/GitHub Copilot, Codex, Gemini
// CLI and others; ".claude"/".github" cover Claude Code and older Copilot
// layouts.
var skillAgentTargets = map[string]string{
	"agents":  filepath.Join(".agents", "skills"),
	"claude":  filepath.Join(".claude", "skills"),
	"copilot": filepath.Join(".github", "skills"),
}

// skillInstallJSON is the --json result shape of "m3u8dl skills install".
type skillInstallJSON struct {
	OK     bool     `json:"ok"`
	Target string   `json:"target,omitempty"`
	Skills []string `json:"skills,omitempty"`
	Files  int      `json:"files,omitempty"`
	Error  string   `json:"error,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
}

// newSkillsCmd builds the "skills" subcommand tree. It exposes the Agent
// Skills bundled into this binary (see embeddedSkills) so a user's coding
// agent can install them locally instead of cloning the repository.
func newSkillsCmd() *cobra.Command {
	var (
		agent    string
		dir      string
		scope    string
		force    bool
		dryRun   bool
		skillOut bool
		skillArg string
	)

	cmd := &cobra.Command{
		Use:   "skills",
		Short: "管理内置的 Agent Skills（供 AI 编码助手自动发现与使用）",
		Long: `m3u8dl 二进制内置了 Agent Skills（https://agentskills.io），
让用户的 AI 编码助手（VS Code / GitHub Copilot、Claude Code、Codex、Gemini CLI 等）
了解如何正确地安装与使用本工具。

用法示例:
  m3u8dl skills list                        # 查看内置 skills
  m3u8dl skills install                     # 安装到当前项目的 .agents/skills/
  m3u8dl skills install --agent claude      # 安装到 .claude/skills/
  m3u8dl skills install --scope user        # 安装到 ~/.agents/skills/（所有项目共用）
  m3u8dl skills install --dir ./out --json  # 指定目录并输出 JSON（供脚本/Agent 解析）`,
	}

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "把内置 skills 安装到指定目录，供 Agent 自动发现",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := resolveSkillTarget(agent, dir, scope)
			if err != nil {
				return err
			}
			res, err := installSkills(target, skillArg, force, dryRun)
			if skillOut {
				b, _ := json.Marshal(res)
				fmt.Println(string(b))
			} else if err == nil {
				if dryRun {
					logger.Printf("[dry-run] 将写入 %d 个文件到 %s (skills=%s)\n", res.Files, res.Target, strings.Join(res.Skills, ","))
				} else {
					fmt.Printf("[Success] 已安装 %d 个文件 -> %s (skills: %s)\n", res.Files, res.Target, strings.Join(res.Skills, ", "))
				}
			}
			return err
		},
	}
	installCmd.Flags().StringVar(&agent, "agent", "agents", "目标 Agent 预设：agents(.agents/skills) | claude(.claude/skills) | copilot(.github/skills)")
	installCmd.Flags().StringVar(&dir, "dir", "", "直接指定 skills 根目录（覆盖 --agent/--scope）")
	installCmd.Flags().StringVar(&scope, "scope", "project", "安装范围：project（当前目录）| user（$HOME）")
	installCmd.Flags().StringVar(&skillArg, "skill", "", "只安装指定名称的 skill（默认安装全部）")
	installCmd.Flags().BoolVar(&force, "force", false, "覆盖已存在的同名 skill")
	installCmd.Flags().BoolVar(&dryRun, "dry-run", false, "只报告将安装的内容，不写文件")
	installCmd.Flags().BoolVar(&skillOut, "json", false, "以 JSON 输出结构化结果（利于程序/LLM 解析）")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "列出二进制内置的 skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			names, err := listEmbeddedSkills()
			if err != nil {
				return failCode(1, "%v", err)
			}
			if skillOut {
				b, _ := json.Marshal(map[string]any{"ok": true, "skills": names})
				fmt.Println(string(b))
				return nil
			}
			for _, n := range names {
				fmt.Println(n)
			}
			return nil
		},
	}
	listCmd.Flags().BoolVar(&skillOut, "json", false, "以 JSON 输出结构化结果")

	cmd.AddCommand(installCmd, listCmd)
	return cmd
}

// resolveSkillTarget turns the --agent / --dir / --scope flags into an
// absolute skills root directory.
func resolveSkillTarget(agent, dir, scope string) (string, error) {
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", failCode(1, "解析 --dir 失败: %v", err)
		}
		return abs, nil
	}
	rel, ok := skillAgentTargets[agent]
	if !ok {
		return "", failCode(1, "未知 --agent %q（可选：agents|claude|copilot）", agent)
	}
	switch scope {
	case "project":
		cwd, err := os.Getwd()
		if err != nil {
			return "", failCode(1, "获取当前目录失败: %v", err)
		}
		return filepath.Join(cwd, rel), nil
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", failCode(1, "获取用户目录失败: %v", err)
		}
		return filepath.Join(home, rel), nil
	default:
		return "", failCode(1, "未知 --scope %q（可选：project|user）", scope)
	}
}

// listEmbeddedSkills returns the sorted skill names present in the embedded FS.
func listEmbeddedSkills() ([]string, error) {
	skillDirs, err := embeddedSkills.ReadDir(skillsEmbedRoot)
	if err != nil {
		return nil, fmt.Errorf("读取内置 skills 失败: %w", err)
	}
	var names []string
	for _, d := range skillDirs {
		if !d.IsDir() {
			continue
		}
		if _, err := embeddedSkills.ReadFile(skillsEmbedRoot + "/" + d.Name() + "/SKILL.md"); err == nil {
			names = append(names, d.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// installSkills copies the embedded skills (or one named skill) into the
// target skills root. An existing copy is never silently overwritten; pass
// force=true to upgrade it.
func installSkills(target, only string, force, dryRun bool) (skillInstallJSON, error) {
	res := skillInstallJSON{Target: target, DryRun: dryRun}
	names, err := listEmbeddedSkills()
	if err != nil {
		return skillInstallJSON{OK: false, Error: err.Error()}, failCode(1, "%v", err)
	}
	if only != "" {
		found := false
		for _, n := range names {
			if n == only {
				found = true
			}
		}
		if !found {
			msg := fmt.Sprintf("内置 skill %q 不存在（可用：%s）", only, strings.Join(names, ", "))
			return skillInstallJSON{OK: false, Error: msg}, failCode(1, "%s", msg)
		}
		names = []string{only}
	}
	res.Skills = names

	var count int
	for _, name := range names {
		root := skillsEmbedRoot + "/" + name
		dstRoot := filepath.Join(target, name)
		marker := filepath.Join(dstRoot, "SKILL.md")
		if _, err := os.Stat(marker); err == nil && !force && !dryRun {
			msg := fmt.Sprintf("%s 已存在，如需覆盖请加 --force", dstRoot)
			return skillInstallJSON{OK: false, Error: msg, Target: target}, failCode(1, "%s", msg)
		}
		err := fs.WalkDir(embeddedSkills, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			dst := filepath.Join(dstRoot, filepath.FromSlash(rel))
			count++
			if dryRun {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			data, err := embeddedSkills.ReadFile(p)
			if err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if strings.HasPrefix(filepath.ToSlash(rel), "scripts/") {
				mode = 0o755
			}
			return os.WriteFile(dst, data, mode)
		})
		if err != nil {
			msg := fmt.Sprintf("安装 skill %s 失败: %v", name, err)
			return skillInstallJSON{OK: false, Error: msg}, failCode(1, "%s", msg)
		}
	}
	res.Files = count
	res.OK = true
	return res, nil
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
	applyRequestConfig()
	if err := setupRateLimit(); err != nil {
		return err
	}
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
		if err := os.MkdirAll(downloadDir, os.ModePerm); err != nil {
			logger.Printf("[Error] 创建下载目录失败: %v", err)
			return ""
		}
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
	body, err := io.ReadAll(withInactivityTimeout(res.Body, reqTimeout))
	if err != nil {
		return ""
	}
	return string(body)
}

// get sends a GET request with unified UA/headers/timeout over the shared
// client (connection pooling + HTTP/2); non-2xx is treated as a failure and
// returns an empty response.
func get(url string) *http.Response {
	return doGet(url, "", "")
}

// getRange issues a GET with a "Range: bytes=<from>-" header (and, when
// ifRange is non-empty, an If-Range validator) so a partial .part can be
// resumed: a 206 appends the tail, while a changed resource answers 200
// (full body, safe restart) instead of a tail that could splice.
func getRange(url string, from int64, ifRange string) *http.Response {
	return doGet(url, fmt.Sprintf("bytes=%d-", from), ifRange)
}

func doGet(url, rangeHeader, ifRange string) *http.Response {
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
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	if ifRange != "" {
		req.Header.Set("If-Range", ifRange)
	}
	res, err := httpClient.Do(req)
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
			b, _ := io.ReadAll(withInactivityTimeout(res.Body, reqTimeout))
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

	// Retries on the primary host; after the first failure also try the
	// alternate host once (automatic fallback). Every attempt resumes from the
	// current .part size via HTTP Range when the server supports it.
	for i := 0; i < TS_DOWNLOAD_RETRY; i++ {
		if tryDownload(ts.Url, ts.Key, currPath) {
			return true
		}
		if i == 0 && ts.AltUrl != "" && ts.AltUrl != ts.Url {
			// The alternate URL is a different resolution candidate (v1/v2)
			// and may be a different resource: never let its bytes mix with
			// the primary's .part in either direction — clear before the
			// alternate attempt and again when it fails.
			dropPart(currPath + ".part")
			if tryDownload(ts.AltUrl, ts.Key, currPath) {
				return true
			}
			dropPart(currPath + ".part")
		}
	}
	return false
}

// partMeta binds a .part to the resource it came from: a later run must not
// resume partial bytes that belong to a different playlist (same positional
// segment name, different URL) or to a since-revised object.
type partMeta struct {
	url          string
	etag         string
	lastModified string
}

// validator returns the If-Range value: a strong ETag when present (weak
// validators are not allowed for range validation), else Last-Modified, else
// "" meaning the server gave us nothing to verify against.
func (m partMeta) validator() string {
	if m.etag != "" && !strings.HasPrefix(m.etag, "W/") {
		return m.etag
	}
	return m.lastModified
}

// writePartMeta records the origin of a fresh .part (its request URL plus any
// validators the response carried) so resume can verify it later.
func writePartMeta(metaPath, rawURL string, res *http.Response) {
	lines := strings.Join([]string{rawURL, res.Header.Get("ETag"), res.Header.Get("Last-Modified")}, "\n")
	_ = os.WriteFile(metaPath, []byte(lines), 0o666)
}

// readPartMeta loads the sidecar written by writePartMeta.
func readPartMeta(metaPath string) (partMeta, bool) {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return partMeta{}, false
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) != 3 || lines[0] == "" {
		return partMeta{}, false
	}
	return partMeta{url: lines[0], etag: lines[1], lastModified: lines[2]}, true
}

// dropPart removes a .part together with its meta sidecar.
func dropPart(partPath string) {
	os.Remove(partPath)
	os.Remove(partPath + ".meta")
}

// tryDownload performs one download attempt and returns whether it succeeded.
func tryDownload(rawURL, key, currPath string) bool {
	partPath := currPath + ".part"
	res, resumeFrom := acquireResponse(rawURL, partPath)
	if _, _, ok := rangeResume(res, resumeFrom); !ok {
		// A 206 whose Content-Range is missing/garbled, or starts at neither
		// our resume offset nor byte zero, is a tail — not the resource.
		// Re-fetch without Range instead of splicing it into the segment.
		res.Body.Close()
		resumeFrom = 0
		res = get(rawURL)
	}
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		res.Body.Close()
		return false
	}
	ok := writeTSFromResponse(res, key, currPath, resumeFrom, rawURL)
	res.Body.Close()
	return ok
}

// acquireResponse obtains the response for one download attempt, deciding
// whether the existing .part can be resumed (Range + If-Range) or must
// restart whole. It drops a .part that cannot be verified — missing/invalid
// or URL-mismatched meta, or one without a usable validator — as well as a
// stale one (416), returning a plain full GET in all of those cases. The
// returned resumeFrom is non-zero only when a valid resume was attempted.
func acquireResponse(rawURL, partPath string) (*http.Response, int64) {
	resumeFrom := fileSizeOf(partPath)
	if resumeFrom == 0 {
		return get(rawURL), 0
	}
	meta, ok := readPartMeta(partPath + ".meta")
	v := meta.validator()
	if !ok || meta.url != rawURL || v == "" {
		// Without a validator we cannot detect a same-URL revised object,
		// so an unguarded Range could splice stale prefix bytes. Treat a
		// partial we cannot bind and verify as garbage, restart whole.
		dropPart(partPath)
		return get(rawURL), 0
	}
	res := getRange(rawURL, resumeFrom, v)
	if res.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		// Our offset is beyond the resource (stale/corrupt .part): drop it
		// and fall through to a plain full download.
		res.Body.Close()
		dropPart(partPath)
		return get(rawURL), 0
	}
	return res, resumeFrom
}

// rangeResume maps a response onto the existing .part: seek is the write
// offset to append at (0 = restart from scratch), total is the advertised
// full resource size from Content-Range (-1 when unknown or not a 206),
// and ok is false when a 206 is unusable — missing/garbled Content-Range, or
// a start that matches neither resumeFrom (append the tail) nor zero (the
// body is the full resource) — in which case it is a tail, not the resource.
func rangeResume(res *http.Response, resumeFrom int64) (seek, total int64, ok bool) {
	if res.StatusCode != http.StatusPartialContent {
		return 0, -1, true // 200 full body: restart from byte zero
	}
	start, _, total, ok := parseContentRange(res.Header.Get("Content-Range"))
	if !ok || (start != resumeFrom && start != 0) {
		return 0, 0, false
	}
	if start == resumeFrom {
		return resumeFrom, total, true
	}
	return 0, total, true
}

// parseContentRange parses an HTTP Content-Range value like
// "bytes 200-1073/1234" into its start/end/total parts. total is -1 for "*".
func parseContentRange(v string) (start, end, total int64, ok bool) {
	if !strings.HasPrefix(v, "bytes ") {
		return 0, 0, 0, false
	}
	spec := v[len("bytes "):]
	slash := strings.IndexByte(spec, '/')
	if slash < 0 {
		return 0, 0, 0, false
	}
	pair := spec[:slash]
	dash := strings.IndexByte(pair, '-')
	if dash < 0 {
		return 0, 0, 0, false
	}
	s, err1 := strconv.ParseInt(pair[:dash], 10, 64)
	e, err2 := strconv.ParseInt(pair[dash+1:], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, 0, false
	}
	total = -1
	if t := spec[slash+1:]; t != "*" {
		tv, err3 := strconv.ParseInt(t, 10, 64)
		if err3 != nil {
			return 0, 0, 0, false
		}
		total = tv
	}
	return s, e, total, true
}

// writeTSFromResponse writes the cleaned response body to currPath.
// It lands via a .part temp file: when the response is a 206 that matches the
// known prefix, the body is appended to resume; otherwise the .part restarts
// from scratch. A short/interrupted transfer KEEPS the .part so the next
// attempt (or the next run of the same command) can resume mid-file; the
// .part is removed once the segment is finalized or proven unusable.
func writeTSFromResponse(res *http.Response, key, currPath string, resumeFrom int64, rawURL string) bool {
	partPath := currPath + ".part"

	// Only continue an existing .part when we can map this response onto it
	// (206 appended at our offset, or a full-body restart); anything else —
	// including a broken 206 surviving the earlier re-fetch — fails loudly.
	seek, rangeTotal, ok := rangeResume(res, resumeFrom)
	if !ok {
		return false
	}

	out, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return false
	}
	if seek > 0 {
		if _, err := out.Seek(seek, io.SeekStart); err != nil {
			out.Close()
			return false
		}
	} else if err := out.Truncate(0); err != nil {
		out.Close()
		return false
	}
	if seek == 0 {
		// The .part restarts from this response, so it now belongs to this
		// URL/validators; record them for the next resume verification.
		writePartMeta(partPath+".meta", rawURL, res)
	}

	written, err := io.Copy(out, throttleReads(withInactivityTimeout(res.Body, reqTimeout), res.Request.Context()))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil || (written == 0 && seek == 0) {
		if written == 0 {
			dropPart(partPath) // nothing was landed, no point keeping an empty .part
		}
		return false
	}
	// Check length: when Content-Length is trustworthy, a short packet means
	// an incomplete body (kept as .part for the next resume attempt).
	want := seek + written
	if cl := res.Header.Get("Content-Length"); cl != "" {
		if body, _ := strconv.ParseInt(cl, 10, 64); body > 0 {
			want = seek + body
		}
	}
	// A server may cap a 206 to a subrange (e.g. bytes 100-199/1000): only
	// finalize once the accumulated .part reaches the advertised total,
	// otherwise keep it so the next attempt resumes the missing tail.
	if rangeTotal > want {
		want = rangeTotal
	}
	if got := fileSizeOf(partPath); got < want {
		return false
	}

	// Decrypt + strip leading junk
	var decErr error
	if key != "" {
		decErr = decryptFileTo(partPath, currPath, []byte(key))
	} else {
		decErr = stripLeadingJunkFile(partPath, currPath)
	}
	if decErr != nil {
		dropPart(partPath) // bytes unusable (bad key/corrupt): restart clean
		return false
	}
	dropPart(partPath) // segment landed successfully
	return true
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
	var writeErr error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ts") {
			continue
		}
		if _, err := fmt.Fprintf(listFile, "file '%s'\n", e.Name()); err != nil {
			writeErr = err
			break
		}
		hasTs = true
	}
	listFile.Close()
	if writeErr != nil {
		logger.Printf("[Error] 写入 filelist 失败: %v", writeErr)
		return ""
	}
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

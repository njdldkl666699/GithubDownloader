package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	configName       = ".ghdownloader.json"
	defaultUserAgent = "GithubDownloader/1.0"
)

var mirrorBases = []string{
	"https://ghfast.top",
	"https://git.yylx.win",
	"https://gh-proxy.com",
	"https://ghfile.geekertao.top",
	"https://gh-proxy.net",
	"https://j.1win.ggff.net",
	"https://ghm.078465.xyz",
	"https://gitproxy.127731.xyz",
	"https://jiashu.1win.eu.org",
	"https://github.tbedu.top",
	"",
}

type AppConfig struct {
	BestMirror string           `json:"best_mirror"`
	Benchmarks map[string]int64 `json:"benchmarks_ms"`
	SavedAt    time.Time        `json:"saved_at"`
	Version    int              `json:"version"`
}

type RepoRef struct {
	Owner     string
	Repo      string
	Branch    string
	Path      string
	IsFile    bool
	SourceURL string
	Canonical string
}

type githubEntry struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	DownloadURL string `json:"download_url"`
}

type benchResult struct {
	Base     string
	Duration time.Duration
	Err      error
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	inputURL := strings.TrimSpace(os.Args[1])
	output := "."
	if len(os.Args) >= 3 {
		output = strings.TrimSpace(os.Args[2])
	}

	client := &http.Client{Timeout: 20 * time.Second}
	conf, err := loadOrBenchmarkConfig(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	ref, err := parseGitHubRef(inputURL, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "链接解析失败: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(output, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建输出目录失败: %v\n", err)
		os.Exit(1)
	}

	if ref.IsFile {
		err = downloadSingle(ref, output, conf.BestMirror, client)
	} else {
		err = downloadDirRecursive(ref, output, conf.BestMirror, client)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "下载失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("完成")

}
func printUsage() {
	fmt.Println("用法:")
	fmt.Println("  GithubDownloader <github链接或镜像链接> [输出目录]")
	fmt.Println("示例:")
	fmt.Println("  GithubDownloader https://github.com/golang/go/blob/master/README.md")
	fmt.Println("  GithubDownloader https://github.com/golang/go/tree/master/src ./out")
}

func loadOrBenchmarkConfig(client *http.Client) (*AppConfig, error) {
	if b, err := os.ReadFile(configName); err == nil {
		var cfg AppConfig
		if err := json.Unmarshal(b, &cfg); err == nil && cfg.BestMirror != "" {
			return &cfg, nil
		}
	}

	fmt.Println("首次运行，正在测速镜像...")
	best, mapDur, err := benchmarkMirrors(client)
	if err != nil {
		return nil, err
	}

	cfg := &AppConfig{
		BestMirror: best,
		Benchmarks: mapDur,
		SavedAt:    time.Now(),
		Version:    1,
	}

	if err := saveConfig(cfg); err != nil {
		return nil, err
	}

	fmt.Printf("测速完成，已保存配置: %s\n", displayMirror(best))
	return cfg, nil
}

func saveConfig(cfg *AppConfig) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configName, b, 0o644)
}

func benchmarkMirrors(client *http.Client) (string, map[string]int64, error) {
	probe := "https://raw.githubusercontent.com/github/gitignore/main/Go.gitignore"
	results := make([]benchResult, 0, len(mirrorBases))
	benchClient := &http.Client{Timeout: 6 * time.Second}
	type indexedResult struct {
		res benchResult
	}
	ch := make(chan indexedResult, len(mirrorBases))

	for _, base := range mirrorBases {
		go func(base string) {
			urlToTest := applyMirror(base, probe)
			start := time.Now()
			err := headOrGet(benchClient, urlToTest)
			ch <- indexedResult{res: benchResult{Base: base, Duration: time.Since(start), Err: err}}
		}(base)
	}

	for i := 0; i < len(mirrorBases); i++ {
		item := <-ch
		results = append(results, item.res)
	}

	sort.Slice(results, func(i, j int) bool {
		iOK := results[i].Err == nil
		jOK := results[j].Err == nil
		if iOK != jOK {
			return iOK
		}
		return results[i].Duration < results[j].Duration
	})

	bench := make(map[string]int64, len(results))
	for _, r := range results {
		key := displayMirror(r.Base)
		if r.Err != nil {
			bench[key] = -1
			continue
		}
		bench[key] = r.Duration.Milliseconds()
	}

	if len(results) == 0 || results[0].Err != nil {
		return "", nil, errors.New("所有镜像测速失败")
	}

	_ = client
	return results[0].Base, bench, nil
}

func headOrGet(client *http.Client, rawURL string) error {
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return nil
		}
	}

	reqGet, err2 := http.NewRequest(http.MethodGet, rawURL, nil)
	if err2 != nil {
		return err2
	}
	reqGet.Header.Set("User-Agent", defaultUserAgent)

	resp2, err2 := client.Do(reqGet)
	if err2 != nil {
		if err != nil {
			return err2
		}
		return fmt.Errorf("状态码: %d", resp.StatusCode)
	}
	defer resp2.Body.Close()
	io.CopyN(io.Discard, resp2.Body, 256)
	if resp2.StatusCode < 200 || resp2.StatusCode >= 400 {
		return fmt.Errorf("状态码: %d", resp2.StatusCode)
	}
	return nil
}

func parseGitHubRef(input string, client *http.Client) (*RepoRef, error) {
	canonical, err := extractCanonicalURL(input)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(canonical)
	if err != nil {
		return nil, err
	}

	segments := splitPathSegments(u.Path)
	if len(segments) < 2 {
		return nil, fmt.Errorf("无法识别仓库信息: %s", canonical)
	}

	ref := &RepoRef{
		Owner:     segments[0],
		Repo:      strings.TrimSuffix(segments[1], ".git"),
		SourceURL: input,
		Canonical: canonical,
	}

	host := strings.ToLower(u.Host)
	if host == "raw.githubusercontent.com" {
		if len(segments) < 4 {
			return nil, fmt.Errorf("raw 链接格式不正确: %s", canonical)
		}
		ref.Owner = segments[0]
		ref.Repo = segments[1]
		ref.Branch = segments[2]
		ref.Path = strings.Join(segments[3:], "/")
		ref.IsFile = true
		return ref, nil
	}

	if len(segments) == 2 {
		branch, err := fetchDefaultBranch(client, ref.Owner, ref.Repo)
		if err != nil {
			return nil, fmt.Errorf("无法获取默认分支: %w", err)
		}
		ref.Branch = branch
		ref.Path = ""
		ref.IsFile = false
		return ref, nil
	}

	mode := segments[2]
	rest := segments[3:]
	if len(rest) == 0 {
		return nil, fmt.Errorf("链接路径不完整: %s", canonical)
	}

	ref.Branch = rest[0]
	if len(rest) > 1 {
		ref.Path = strings.Join(rest[1:], "/")
	}

	switch mode {
	case "blob":
		ref.IsFile = true
	case "tree":
		ref.IsFile = false
	default:
		ref.IsFile = true
		if len(segments) >= 4 {
			ref.Branch = segments[2]
			ref.Path = strings.Join(segments[3:], "/")
		}
	}

	return ref, nil
}

func fetchDefaultBranch(client *http.Client, owner, repo string) (string, error) {
	api := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	req, err := http.NewRequest(http.MethodGet, api, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", fmt.Errorf("GitHub API错误: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.DefaultBranch == "" {
		return "", errors.New("default_branch 为空")
	}
	return payload.DefaultBranch, nil
}

func extractCanonicalURL(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return "", errors.New("链接必须以 http:// 或 https:// 开头")
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}

	host := strings.ToLower(u.Host)
	if host == "github.com" || host == "raw.githubusercontent.com" {
		return normalizeURL(trimmed), nil
	}

	for _, base := range mirrorBases {
		if base == "" {
			continue
		}
		bu, err := url.Parse(base)
		if err != nil {
			continue
		}
		if strings.EqualFold(bu.Host, u.Host) {
			inner := strings.TrimPrefix(strings.TrimSpace(u.EscapedPath()), "/")
			if inner == "" {
				inner = strings.TrimPrefix(strings.TrimSpace(u.Path), "/")
			}
			if inner == "" {
				break
			}

			decoded, err := url.PathUnescape(inner)
			if err == nil {
				inner = decoded
			}

			if strings.HasPrefix(inner, "http://") || strings.HasPrefix(inner, "https://") {
				return normalizeURL(inner), nil
			}
			if strings.Contains(inner, "github.com/") {
				if !strings.HasPrefix(inner, "http") {
					inner = "https://" + inner
				}
				return normalizeURL(inner), nil
			}
		}
	}

	return "", fmt.Errorf("无法识别为 GitHub 或已知镜像链接: %s", input)
}

func normalizeURL(s string) string {
	return strings.TrimSpace(strings.TrimSuffix(s, "/"))
}

func splitPathSegments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))
	for _, seg := range raw {
		if seg == "" {
			continue
		}
		out = append(out, seg)
	}
	return out
}

func displayMirror(base string) string {
	if base == "" {
		return "direct"
	}
	return base
}

func applyMirror(base, canonical string) string {
	if base == "" {
		return canonical
	}
	base = strings.TrimRight(base, "/")
	return base + "/" + canonical
}

func downloadSingle(ref *RepoRef, outputDir, base string, client *http.Client) error {
	rawURL, err := toRawURL(ref)
	if err != nil {
		return err
	}

	targetName := path.Base(ref.Path)
	if targetName == "." || targetName == "/" || targetName == "" {
		targetName = "downloaded_file"
	}

	targetPath := filepath.Join(outputDir, targetName)
	fmt.Printf("下载文件: %s\n", targetPath)
	return downloadWithFallback(rawURL, targetPath, base, client)
}

func downloadDirRecursive(ref *RepoRef, outputDir, base string, client *http.Client) error {
	rootName := ref.Repo
	if ref.Path != "" {
		rootName = path.Base(ref.Path)
	}
	rootLocal := filepath.Join(outputDir, rootName)

	fmt.Printf("下载目录: %s\n", rootLocal)
	if err := walkAndDownload(ref, ref.Path, rootLocal, base, client); err != nil {
		return err
	}

	return nil
}

func walkAndDownload(root *RepoRef, remotePath, localRoot, base string, client *http.Client) error {
	entries, err := listContents(client, root.Owner, root.Repo, root.Branch, remotePath)
	if err != nil {
		return err
	}

	for _, e := range entries {
		rel := strings.TrimPrefix(strings.TrimPrefix(e.Path, remotePath), "/")
		localPath := filepath.Join(localRoot, filepath.FromSlash(rel))

		switch e.Type {
		case "dir":
			if err := os.MkdirAll(localPath, 0o755); err != nil {
				return err
			}
			nextRef := *root
			nextRef.Path = e.Path
			if err := walkAndDownload(&nextRef, e.Path, localRoot, base, client); err != nil {
				return err
			}
		case "file":
			if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
				return err
			}
			rawURL := buildRawURL(root.Owner, root.Repo, root.Branch, e.Path)
			fmt.Printf("下载: %s\n", localPath)
			if err := downloadWithFallback(rawURL, localPath, base, client); err != nil {
				return err
			}
		}
	}

	return nil
}

func listContents(client *http.Client, owner, repo, branch, p string) ([]githubEntry, error) {
	p = strings.TrimPrefix(p, "/")
	api := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", owner, repo, p, url.QueryEscape(branch))

	req, err := http.NewRequest(http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("读取目录失败: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var arr []githubEntry
	if err := json.Unmarshal(body, &arr); err == nil {
		return arr, nil
	}

	var single githubEntry
	if err := json.Unmarshal(body, &single); err == nil && single.Type != "" {
		return []githubEntry{single}, nil
	}

	return nil, errors.New("GitHub API 返回无法解析")
}

func toRawURL(ref *RepoRef) (string, error) {
	u, err := url.Parse(ref.Canonical)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(u.Host, "raw.githubusercontent.com") {
		return ref.Canonical, nil
	}
	if ref.Owner == "" || ref.Repo == "" || ref.Branch == "" || ref.Path == "" {
		return "", errors.New("文件链接信息不完整")
	}
	return buildRawURL(ref.Owner, ref.Repo, ref.Branch, ref.Path), nil
}

func buildRawURL(owner, repo, branch, p string) string {
	p = strings.TrimPrefix(p, "/")
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, branch, p)
}

func downloadWithFallback(rawURL, localPath, base string, client *http.Client) error {
	primary := applyMirror(base, rawURL)
	err := downloadToFile(client, primary, localPath)
	if err == nil {
		return nil
	}
	if base != "" {
		fmt.Printf("镜像失败，回退直连: %v\n", err)
		return downloadToFile(client, rawURL, localPath)
	}
	return err
}

func downloadToFile(client *http.Client, srcURL, targetPath string) error {
	req, err := http.NewRequest(http.MethodGet, srcURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("请求失败: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	tmp := targetPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}

	if err := os.Rename(tmp, targetPath); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

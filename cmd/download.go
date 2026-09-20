package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/wujunwei928/parse-video/parser"
)

var invalidFileCharRe = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// sanitizeFilename 清理文件名中的非法字符
func sanitizeFilename(name string) string {
	name = invalidFileCharRe.ReplaceAllString(name, "_")
	name = strings.TrimSpace(name)
	if name == "" {
		name = "download"
	}
	// 截断过长的文件名
	runes := []rune(name)
	if len(runes) > 200 {
		name = string(runes[:200])
	}
	return name
}

// extFromURL 从 URL 路径提取文件扩展名
func extFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(path.Ext(u.Path))
	if ext != "" && len(ext) <= 10 {
		return ext
	}
	return ""
}

func userAgentForMediaURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return parser.DefaultUserAgent
	}
	host := strings.ToLower(u.Hostname())
	if host == "bilivideo.com" || strings.HasSuffix(host, ".bilivideo.com") ||
		(strings.HasSuffix(host, ".akamaized.net") && strings.Contains(u.Path, "/upgcxcode/")) {
		return parser.UserAgent
	}
	return parser.DefaultUserAgent
}
func refererForMediaURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "weibocdn.com" || strings.HasSuffix(host, ".weibocdn.com") ||
		host == "sinaimg.cn" || strings.HasSuffix(host, ".sinaimg.cn") {
		return "https://weibo.com/"
	}
	if host == "bilivideo.com" || strings.HasSuffix(host, ".bilivideo.com") {
		return "https://www.bilibili.com/"
	}
	return ""
}

func bilibiliCDNCandidates(rawURL string) []string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return []string{rawURL}
	}
	host := strings.ToLower(u.Hostname())
	if host != "bilivideo.com" && !strings.HasSuffix(host, ".bilivideo.com") {
		return []string{rawURL}
	}

	labels := strings.Split(host, ".")
	mirrorIndex := strings.LastIndex(labels[0], "-mirror")
	if mirrorIndex < 0 {
		return []string{rawURL}
	}
	prefix := labels[0][:mirrorIndex+len("-mirror")]
	providers := []string{"ali", "hw", "cos"}
	candidates := make([]string, 0, len(providers)+1)
	seen := make(map[string]struct{}, len(providers)+1)
	for _, provider := range providers {
		clone := *u
		candidateLabels := append([]string(nil), labels...)
		candidateLabels[0] = prefix + provider
		clone.Host = strings.Join(candidateLabels, ".")
		candidate := clone.String()
		if _, ok := seen[candidate]; !ok {
			seen[candidate] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	if _, ok := seen[rawURL]; !ok {
		candidates = append(candidates, rawURL)
	}
	return candidates
}

func probeMediaURL(rawURL, referer string) bool {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", userAgentForMediaURL(rawURL))
	req.Header.Set("Range", "bytes=0-65535")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return false
	}
	n, copyErr := io.CopyN(io.Discard, resp.Body, 64*1024)
	return n == 64*1024 || (copyErr == io.EOF && n > 0)
}

func selectMediaURL(rawURL string) string {
	candidates := bilibiliCDNCandidates(rawURL)
	if len(candidates) == 1 {
		return rawURL
	}
	referer := refererForMediaURL(rawURL)
	for _, candidate := range candidates {
		if probeMediaURL(candidate, referer) {
			return candidate
		}
	}
	return rawURL
}

// downloadFile 下载单个文件到指定路径
func downloadFile(fileURL, savePath string) error {
	fileURL = selectMediaURL(fileURL)
	request := resty.New().R().
		SetHeader("User-Agent", userAgentForMediaURL(fileURL)).
		SetOutput(savePath)
	if referer := refererForMediaURL(fileURL); referer != "" {
		request.SetHeader("Referer", referer)
	}
	resp, err := request.Get(fileURL)
	if err != nil {
		os.Remove(savePath)
		return err
	}
	if resp.StatusCode() != http.StatusOK {
		os.Remove(savePath)
		return fmt.Errorf("HTTP %d", resp.StatusCode())
	}
	return nil
}

// downloadMedia 下载解析结果中的所有媒体文件
func downloadMedia(info *parser.VideoParseInfo, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	baseName := sanitizeFilename(info.Title)
	count := 0

	// 下载视频
	if info.VideoUrl != "" {
		ext := extFromURL(info.VideoUrl)
		if ext == "" {
			ext = ".mp4"
		}
		filename := baseName + ext
		fmt.Fprintf(os.Stderr, "下载视频: %s\n", filename)
		if err := downloadFile(info.VideoUrl, filepath.Join(outputDir, filename)); err != nil {
			return fmt.Errorf("视频下载失败: %w", err)
		}
		count++
	}

	// 下载图集
	for i, img := range info.Images {
		ext := extFromURL(img.Url)
		if ext == "" {
			ext = ".jpg"
		}
		filename := fmt.Sprintf("%s_%03d%s", baseName, i+1, ext)
		fmt.Fprintf(os.Stderr, "下载图片: %s\n", filename)
		if err := downloadFile(img.Url, filepath.Join(outputDir, filename)); err != nil {
			return fmt.Errorf("图片 %d 下载失败: %w", i+1, err)
		}
		count++

		// 下载 LivePhoto 视频
		if img.LivePhotoUrl != "" {
			liveExt := extFromURL(img.LivePhotoUrl)
			if liveExt == "" {
				liveExt = ".mp4"
			}
			liveName := fmt.Sprintf("%s_%03d_live%s", baseName, i+1, liveExt)
			fmt.Fprintf(os.Stderr, "下载 LivePhoto: %s\n", liveName)
			if err := downloadFile(img.LivePhotoUrl, filepath.Join(outputDir, liveName)); err != nil {
				fmt.Fprintf(os.Stderr, "警告: LivePhoto 下载失败: %v\n", err)
			}
			count++
		}
	}

	// 下载封面
	if info.CoverUrl != "" {
		ext := extFromURL(info.CoverUrl)
		if ext == "" {
			ext = ".jpg"
		}
		filename := baseName + "_cover" + ext
		fmt.Fprintf(os.Stderr, "下载封面: %s\n", filename)
		if err := downloadFile(info.CoverUrl, filepath.Join(outputDir, filename)); err != nil {
			fmt.Fprintf(os.Stderr, "警告: 封面下载失败: %v\n", err)
		} else {
			count++
		}
	}

	// 下载音乐
	if info.MusicUrl != "" {
		ext := extFromURL(info.MusicUrl)
		if ext == "" {
			ext = ".mp3"
		}
		filename := baseName + "_music" + ext
		fmt.Fprintf(os.Stderr, "下载音乐: %s\n", filename)
		if err := downloadFile(info.MusicUrl, filepath.Join(outputDir, filename)); err != nil {
			fmt.Fprintf(os.Stderr, "警告: 音乐下载失败: %v\n", err)
		} else {
			count++
		}
	}

	if count > 0 {
		fmt.Fprintf(os.Stderr, "下载完成: %d 个文件保存到 %s\n", count, outputDir)
	} else {
		return fmt.Errorf("无可下载的媒体文件")
	}
	return nil
}

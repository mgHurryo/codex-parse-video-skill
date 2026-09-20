package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wujunwei928/parse-video/parser"
)

func TestDownloadMediaRejectsResultWithoutMedia(t *testing.T) {
	info := &parser.VideoParseInfo{Title: "只有元数据"}

	err := downloadMedia(info, t.TempDir())
	if err == nil {
		t.Fatal("没有任何媒体地址时，下载命令必须返回错误")
	}
	if !strings.Contains(err.Error(), "无可下载的媒体文件") {
		t.Fatalf("错误信息应说明没有媒体文件，实际: %v", err)
	}
}

func TestRefererForMediaURLUsesPublicWeiboOrigin(t *testing.T) {
	cases := []string{
		"https://f.video.weibocdn.com/path/video.mp4",
		"https://wx1.sinaimg.cn/large/cover.jpg",
	}
	for _, mediaURL := range cases {
		if got := refererForMediaURL(mediaURL); got != "https://weibo.com/" {
			t.Fatalf("微博 CDN 应使用公开微博来源页，url=%s referer=%q", mediaURL, got)
		}
	}
	if got := refererForMediaURL("https://example.com/video.mp4"); got != "" {
		t.Fatalf("其他平台不应被注入微博 Referer，实际: %q", got)
	}
}
func TestRefererForMediaURLUsesPublicBilibiliOrigin(t *testing.T) {
	mediaURL := "https://upos-sz-mirrorcosov.bilivideo.com/path/video.mp4"
	if got := refererForMediaURL(mediaURL); got != "https://www.bilibili.com/" {
		t.Fatalf("B站 CDN 应使用公开 B站来源页，实际: %q", got)
	}
}
func TestBilibiliCDNCandidatesPreferAlternateMirrors(t *testing.T) {
	original := "https://upos-sz-mirrorcosov.bilivideo.com/path/video.mp4?token=abc"
	got := bilibiliCDNCandidates(original)
	wantHosts := []string{
		"upos-sz-mirrorali.bilivideo.com",
		"upos-sz-mirrorhw.bilivideo.com",
		"upos-sz-mirrorcos.bilivideo.com",
		"upos-sz-mirrorcosov.bilivideo.com",
	}
	if len(got) != len(wantHosts) {
		t.Fatalf("候选 CDN 数量不匹配: got=%v", got)
	}
	for index, candidate := range got {
		u, err := url.Parse(candidate)
		if err != nil {
			t.Fatalf("候选 URL 无效: %v", err)
		}
		if u.Hostname() != wantHosts[index] {
			t.Fatalf("候选 CDN 顺序不匹配: got=%v", got)
		}
		if u.RawQuery != "token=abc" {
			t.Fatalf("候选 URL 必须保留签名参数: %s", candidate)
		}
	}
}
func TestUserAgentForMediaURLUsesBilibiliDesktopAgent(t *testing.T) {
	urls := []string{
		"https://upos-sz-mirrorcosov.bilivideo.com/path/video.mp4",
		"https://upos-hz-mirrorakam.akamaized.net/upgcxcode/1/2/video.mp4",
	}
	for _, mediaURL := range urls {
		if got := userAgentForMediaURL(mediaURL); got != parser.UserAgent {
			t.Fatalf("B站媒体应沿用解析时的桌面 User-Agent，url=%s got=%q", mediaURL, got)
		}
	}
	if got := userAgentForMediaURL("https://example.com/video.mp4"); got != parser.DefaultUserAgent {
		t.Fatalf("其他媒体应保留默认 User-Agent，实际: %q", got)
	}
}
func TestDownloadMediaDoesNotCountFailedCoverAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	info := &parser.VideoParseInfo{Title: "封面失败", CoverUrl: server.URL + "/cover.jpg"}
	err := downloadMedia(info, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "无可下载的媒体文件") {
		t.Fatalf("封面下载失败不能报告成功，实际: %v", err)
	}
}
func TestDownloadFileRemovesPartialOutputOnNetworkFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
	}))
	server.CloseClientConnections()
	server.Close()

	output := filepath.Join(t.TempDir(), "partial.mp4")
	if err := downloadFile(server.URL+"/video.mp4", output); err == nil {
		t.Fatal("网络失败时应返回错误")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("网络失败后不应保留部分文件，stat err=%v", err)
	}
}

package parser

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func Test_douYin_parseIdFromPath(t *testing.T) {
	type args struct {
		path string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{"抖音视频", args{"/share/video/7329354490828623130/"}, "7329354490828623130", false},
		{"西瓜视频", args{"/douyin/share/video/7144194760184594977"}, "7144194760184594977", false},
		{"异常视频", args{""}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := douYin{}
			got, err := d.parseVideoIdFromPath(tt.args.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseVideoIdFromPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseVideoIdFromPath() got = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestFindDouyinAwemeByID(t *testing.T) {
	body := []byte(`{"aweme_list":[{"aweme_id":"other"},{"aweme_id":"7668621161214889268","desc":"target"}]}`)
	got := findDouyinAwemeByID(body, "7668621161214889268")
	if !got.Exists() || got.Get("desc").String() != "target" {
		t.Fatalf("应找到目标作品，实际: %s", got.Raw)
	}
	if got := findDouyinAwemeByID(body, "missing"); got.Exists() {
		t.Fatalf("不应返回其他作品，实际: %s", got.Raw)
	}
}
func TestDouyinGetRedirectURLDoesNotDownloadResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "bytes=0-0" {
			t.Fatalf("应使用最小 Range 请求探测跳转，实际: %q", got)
		}
		w.Header().Set("Content-Length", "104857600")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(2 * time.Second)
	}))
	defer server.Close()

	info := &VideoParseInfo{VideoUrl: server.URL + "/video.mp4"}
	started := time.Now()
	douYin{}.getRedirectUrl(info)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("跳转探测不应等待或下载媒体正文，耗时: %v", elapsed)
	}
	if info.VideoUrl != server.URL+"/video.mp4" {
		t.Fatalf("没有重定向时不应改写 URL，实际: %q", info.VideoUrl)
	}
}
func TestFetchMobileFeedDetailUsesMatchingAweme(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("aweme_id"); got != "7668621161214889268" {
			t.Fatalf("aweme_id 参数不匹配: %q", got)
		}
		if got := r.URL.Query().Get("aid"); got != "1128" {
			t.Fatalf("aid 参数不匹配: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"aweme_list":[{"aweme_id":"other"},{"aweme_id":"7668621161214889268","desc":"target"}]}`))
	}))
	defer server.Close()

	original := douyinMobileFeedEndpoints
	douyinMobileFeedEndpoints = []string{server.URL}
	defer func() { douyinMobileFeedEndpoints = original }()

	got, err := douYin{}.fetchMobileFeedDetail(newClient(), "7668621161214889268")
	if err != nil {
		t.Fatalf("移动端 Feed 获取失败: %v", err)
	}
	if got.Get("desc").String() != "target" {
		t.Fatalf("应返回严格匹配的作品，实际: %s", got.Raw)
	}
}

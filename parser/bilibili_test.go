package parser

import "testing"

func TestParseBiliInitialState(t *testing.T) {
	body := []byte(`<html><script>window.__INITIAL_STATE__={"videoData":{"bvid":"BV1test","title":"示例标题","pic":"https://example.com/cover.jpg","owner":{"mid":123,"name":"作者","face":"https://example.com/avatar.jpg"},"pages":[{"cid":456}]}};(function(){})();</script></html>`)

	got, err := parseBiliInitialState(body)
	if err != nil {
		t.Fatalf("解析页面回退数据失败: %v", err)
	}
	if got.Data.Bvid != "BV1test" || got.Data.Title != "示例标题" {
		t.Fatalf("视频元数据不匹配: %+v", got.Data)
	}
	if len(got.Data.Pages) != 1 || got.Data.Pages[0].Cid != 456 {
		t.Fatalf("分页 CID 不匹配: %+v", got.Data.Pages)
	}
	if got.Data.Owner.Mid != 123 || got.Data.Owner.Name != "作者" {
		t.Fatalf("作者信息不匹配: %+v", got.Data.Owner)
	}
}

func TestParseBiliInitialStateRejectsMissingData(t *testing.T) {
	if _, err := parseBiliInitialState([]byte(`<html></html>`)); err == nil {
		t.Fatal("缺少 __INITIAL_STATE__ 时应返回错误")
	}
	if _, err := parseBiliInitialState([]byte(`window.__INITIAL_STATE__={"videoData":{}};`)); err == nil {
		t.Fatal("视频数据不完整时应返回错误")
	}
}

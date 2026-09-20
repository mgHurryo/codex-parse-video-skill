package parser

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/tidwall/gjson"
	"golang.org/x/net/html"
)

type douYin struct{}

func (d douYin) parseVideoID(videoId string) (*VideoParseInfo, error) {
	client := newClient()
	data, mobileFeedErr := d.fetchMobileFeedDetail(client, videoId)
	isNote := false
	var jsonBytes []byte

	if data.Exists() {
		jsonBytes = []byte(data.Raw)
		isNote = len(data.Get("images").Array()) > 0
	} else {
		reqUrl := fmt.Sprintf("https://www.iesdouyin.com/share/video/%s", videoId)
		res, err := client.R().
			SetHeader(HttpHeaderUserAgent, "Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1").
			Get(reqUrl)
		if err != nil {
			return nil, err
		}

		resBody := res.Body()
		canonical, err := d.getCanonicalFromHTML(string(resBody))
		if err == nil && canonical != "" {
			isNote = strings.Contains(canonical, "/note/")
		}

		if isNote {
			webId := "75" + d.generateFixedLengthNumericID(15)
			aBogus := d.randSeq(64)

			reqUrl = fmt.Sprintf("https://www.iesdouyin.com/web/api/v2/aweme/slidesinfo/?reflow_source=reflow_page&web_id=%s&device_id=%s&aweme_ids=%%5B%s%%5D&request_source=200&a_bogus=%s", webId, webId, videoId, aBogus)
			res, err = client.R().
				SetHeader(HttpHeaderUserAgent, "Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1").
				Get(reqUrl)
			if err != nil {
				return nil, err
			}

			jsonBytes = res.Body()
			data = gjson.GetBytes(jsonBytes, "aweme_details.0")
			if !data.Exists() {
				isNote = false
			}
		}

		if !isNote {
			re := regexp.MustCompile(`window._ROUTER_DATA\s*=\s*(.*?)</script>`)
			findRes := re.FindSubmatch(resBody)
			if len(findRes) < 2 {
				return nil, fmt.Errorf("移动端 Feed 获取失败（%v），且分享页缺少视频数据", mobileFeedErr)
			}

			jsonBytes = bytes.TrimSpace(findRes[1])
			data = gjson.GetBytes(jsonBytes, "loaderData.video_(id)/page.videoInfoRes.item_list.0")
		}
	}

	if !data.Exists() {
		filterObj := gjson.GetBytes(
			jsonBytes,
			fmt.Sprintf(`loaderData.video_(id)/page.videoInfoRes.filter_list.#(aweme_id=="%s")`, videoId),
		)

		return nil, fmt.Errorf(
			"get video info fail: %s - %s (mobile feed: %v)",
			filterObj.Get("filter_reason"),
			filterObj.Get("detail_msg"),
			mobileFeedErr,
		)
	}

	imagesObjArr := data.Get("images").Array()
	images := make([]ImgInfo, 0, len(imagesObjArr))
	for _, imageItem := range imagesObjArr {
		urlList := imageItem.Get("url_list").Array()
		imageUrl := d.getNoWebpUrl(urlList)
		if len(imageUrl) > 0 {
			images = append(images, ImgInfo{
				Url:          imageUrl,
				LivePhotoUrl: imageItem.Get("video.play_addr.url_list.0").String(),
			})
		}
	}

	var videoUrl string
	if !isNote {
		videoUrl = data.Get("video.play_addr_h264.url_list.0").String()
		if videoUrl == "" {
			videoUrl = data.Get("video.play_addr.url_list.0").String()
		}
		videoUrl = strings.ReplaceAll(videoUrl, "playwm", "play")
	}

	musicUrl := data.Get("video.play_addr.uri").String()
	if len(images) > 0 {
		videoUrl = ""
	} else {
		musicUrl = ""
	}

	urlList := data.Get("video.cover.url_list").Array()
	coverUrl := d.getNoWebpUrl(urlList)
	if coverUrl == "" {
		coverUrl = d.getNoWebpUrl(data.Get("video.origin_cover.url_list").Array())
	}

	videoInfo := &VideoParseInfo{
		Title:    data.Get("desc").String(),
		VideoUrl: videoUrl,
		MusicUrl: musicUrl,
		CoverUrl: coverUrl,
		Images:   images,
	}
	videoInfo.Author.Uid = data.Get("author.sec_uid").String()
	videoInfo.Author.Name = data.Get("author.nickname").String()
	videoInfo.Author.Avatar = data.Get("author.avatar_thumb.url_list.0").String()

	if len(videoInfo.VideoUrl) > 0 {
		d.getRedirectUrl(videoInfo)
	}

	if videoInfo.VideoUrl == "" && len(videoInfo.Images) == 0 {
		return nil, errors.New("没有作品")
	}

	return videoInfo, nil
}

const douyinMobileFeedUserAgent = "com.ss.android.ugc.aweme/290101 (Linux; U; Android 10; zh_CN; Pixel 4; Build/QQ3A.200805.001; Cronet/TTNetVersion:5f9037be 2023-01-13 QuicVersion:4668bb42 2022-11-21)"

var douyinMobileFeedEndpoints = []string{
	"https://api5-normal-c-hl.amemv.com/aweme/v1/feed/",
	"https://aweme.snssdk.com/aweme/v1/feed/",
}

func (d douYin) fetchMobileFeedDetail(client *resty.Client, videoID string) (gjson.Result, error) {
	if client == nil {
		client = newClient()
	}
	var lastErr error
	for _, endpoint := range douyinMobileFeedEndpoints {
		resp, err := client.R().
			SetHeader(HttpHeaderUserAgent, douyinMobileFeedUserAgent).
			SetHeader("Accept", "application/json, text/plain, */*").
			SetQueryParams(map[string]string{
				"aweme_id": videoID,
				"aid":      "1128",
			}).
			Get(endpoint)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode() != 200 {
			lastErr = fmt.Errorf("%s 返回 HTTP %d", endpoint, resp.StatusCode())
			continue
		}
		if detail := findDouyinAwemeByID(resp.Body(), videoID); detail.Exists() {
			return detail, nil
		}
		lastErr = fmt.Errorf("%s 未返回目标作品", endpoint)
	}
	if lastErr == nil {
		lastErr = errors.New("移动端 Feed 没有可用节点")
	}
	return gjson.Result{}, lastErr
}

func findDouyinAwemeByID(body []byte, videoID string) gjson.Result {
	for _, item := range gjson.GetBytes(body, "aweme_list").Array() {
		if item.Get("aweme_id").String() == videoID || item.Get("id").String() == videoID {
			return item
		}
	}
	return gjson.Result{}
}
func (d douYin) parseShareUrl(shareUrl string) (*VideoParseInfo, error) {
	urlRes, err := url.Parse(shareUrl)
	if err != nil {
		return nil, err
	}

	switch urlRes.Host {
	case "www.iesdouyin.com", "www.douyin.com":
		return d.parsePcShareUrl(shareUrl) // 解析电脑网页端链接
	case "v.douyin.com":
		return d.parseAppShareUrl(shareUrl) // 解析App分享链接
	}

	return nil, fmt.Errorf("douyin not support this host: %s", urlRes.Host)
}

func (d douYin) parseAppShareUrl(shareUrl string) (*VideoParseInfo, error) {
	// 适配App分享链接类型:
	// https://v.douyin.com/xxxxxx/

	client := newClient()
	// disable redirects in the HTTP client, get params before redirects
	client.SetRedirectPolicy(resty.NoRedirectPolicy())
	res, err := client.R().
		SetHeader(HttpHeaderUserAgent, DefaultUserAgent).
		Get(shareUrl)
	// 非 resty.ErrAutoRedirectDisabled 错误时，返回错误
	if !errors.Is(err, resty.ErrAutoRedirectDisabled) {
		return nil, err
	}

	locationRes, err := res.RawResponse.Location()
	if err != nil {
		return nil, err
	}

	videoId, err := d.parseVideoIdFromPath(locationRes.Path)
	if err != nil {
		return nil, err
	}
	if len(videoId) <= 0 {
		return nil, errors.New("parse video id from share url fail")
	}

	// 西瓜视频解析方式不一样
	if strings.Contains(locationRes.Host, "ixigua.com") {
		return xiGua{}.parseVideoID(videoId)
	}

	return d.parseVideoID(videoId)
}

func (d douYin) parsePcShareUrl(shareUrl string) (*VideoParseInfo, error) {
	// 适配电脑网页端链接类型
	// https://www.iesdouyin.com/share/video/xxxxxx/
	// https://www.douyin.com/video/xxxxxx
	videoId, err := d.parseVideoIdFromPath(shareUrl)
	if err != nil {
		return nil, err
	}
	return d.parseVideoID(videoId)
}

func (d douYin) parseVideoIdFromPath(urlPath string) (string, error) {
	if len(urlPath) <= 0 {
		return "", errors.New("url path is empty")
	}

	urlPathParse, err := url.Parse(urlPath)
	if err != nil {
		return "", err
	}

	//判断网页精选页面的视频
	//https://www.douyin.com/jingxuan?modal_id=7555093909760789812
	videoId := urlPathParse.Query().Get("modal_id")

	if len(videoId) > 0 {
		return videoId, nil
	}

	//判断其他页面的视频
	//https://www.iesdouyin.com/share/video/7424432820954598707/?region=CN&mid=7424432976273869622&u_code=0
	urlPath = strings.Trim(urlPathParse.Path, "/")
	urlSplit := strings.Split(urlPath, "/")

	// 获取最后一个元素
	if len(urlSplit) > 0 {
		return urlSplit[len(urlSplit)-1], nil
	}

	return "", errors.New("parse video id from path fail")
}

func (d douYin) getRedirectUrl(videoInfo *VideoParseInfo) {
	client := newClient()
	client.SetTimeout(8 * time.Second)
	client.SetRedirectPolicy(resty.NoRedirectPolicy())
	res, err := client.R().
		SetHeader(HttpHeaderUserAgent, DefaultUserAgent).
		SetHeader("Range", "bytes=0-0").
		SetDoNotParseResponse(true).
		Get(videoInfo.VideoUrl)
	if res != nil && res.RawBody() != nil {
		defer res.RawBody().Close()
	}
	if err != nil && !errors.Is(err, resty.ErrAutoRedirectDisabled) {
		return
	}
	if res == nil || res.RawResponse == nil {
		return
	}
	locationRes, locationErr := res.RawResponse.Location()
	if locationErr == nil && locationRes != nil {
		videoInfo.VideoUrl = locationRes.String()
	}
}

func (d douYin) randSeq(n int) string {
	letters := []rune("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// 生成固定位数的随机数字（前导零）
func (d douYin) generateFixedLengthNumericID(length int) string {
	// 创建一个新的随机数生成器源
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	max2 := int64(1)
	for i := 0; i < length; i++ {
		max2 *= 10
	}

	randomNum := r.Int63n(max2)
	return fmt.Sprintf("%0*d", length, randomNum)
}

// 优先获取非 .webp 格式的图片 url
func (d douYin) getNoWebpUrl(urlList []gjson.Result) string {
	var imageUrl string
	// 手动遍历查找包含 .jpeg 或 .png 的 URL
	found := false
	for _, urllink := range urlList {
		urlStr := urllink.String()
		//if strings.Contains(urlStr, ".jpeg") || strings.Contains(urlStr, ".png") {
		if !strings.Contains(urlStr, ".webp") {
			imageUrl = urlStr
			found = true
			break
		}
	}

	// 如果没找到，使用第一项
	if !found && len(urlList) > 0 {
		imageUrl = urlList[0].String()
	}

	return imageUrl
}

// 从 HTML 字符串获取 canonical URL
func (d douYin) getCanonicalFromHTML(htmlContent string) (string, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return "", err
	}

	return d.findCanonical(doc), nil
}

// 递归查找 canonical link
func (d douYin) findCanonical(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "link" {
		var rel, href string
		for _, attr := range n.Attr {
			switch attr.Key {
			case "rel":
				rel = attr.Val
			case "href":
				href = attr.Val
			}
		}
		if rel == "canonical" && href != "" {
			return href
		}
	}

	// 递归遍历子节点
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := d.findCanonical(c); result != "" {
			return result
		}
	}

	return ""
}

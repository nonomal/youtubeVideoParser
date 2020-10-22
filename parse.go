package youtubevideoparser

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	"github.com/suconghou/youtubevideoparser/request"
	"github.com/tidwall/gjson"
)

const (
	baseURL       = "https://www.youtube.com"
	videoPageHost = baseURL + "/watch?v=%s"
	videoInfoHost = baseURL + "/get_video_info?video_id=%s"
)

var ytplayerConfigRegexp = regexp.MustCompile(`;ytplayer\.config\s*=\s*({.+?});ytplayer`)

// Parser return instance
type Parser struct {
	ID     string
	JsPath string
	Player gjson.Result
	client http.Client
}

// VideoInfo contains video info
type VideoInfo struct {
	ID       string                 `json:"id"`
	Title    string                 `json:"title"`
	Duration string                 `json:"duration"`
	Author   string                 `json:"author"`
	Streams  map[string]*StreamItem `json:"streams"`
}

// NewParser create Parser instance
func NewParser(id string, client http.Client) (*Parser, error) {
	var (
		videoPageURL = fmt.Sprintf(videoPageHost, id)
		cachekey     = "jsPath"
	)
	videoPageData, err := request.GetURLData(videoPageURL, false, client)
	if err != nil {
		return nil, err
	}
	var (
		jsPath string
		player gjson.Result
	)
	if arr := ytplayerConfigRegexp.FindSubmatch(videoPageData); len(arr) >= 2 {
		res := gjson.ParseBytes(arr[1])
		jsPath = res.Get("assets.js").String()
		player = gjson.Parse(res.Get("args.player_response").String())
		if jsPath != "" {
			request.Set(cachekey, []byte(jsPath))
		}
	}
	if jsPath == "" {
		var (
			videoInfoURL = fmt.Sprintf(videoInfoHost, id)
		)
		videoInfoData, err := request.GetURLData(videoInfoURL, false, client)
		if err != nil {
			return nil, err
		}
		values, err := url.ParseQuery(string(videoInfoData))
		if err != nil {
			return nil, err
		}
		status := values.Get("status")
		if status != "ok" {
			return nil, fmt.Errorf("%s %s:%s", status, values.Get("errorcode"), values.Get("reason"))
		}
		player = gjson.Parse(values.Get("player_response"))
		jsPath = string(request.Get(cachekey))
		ps := player.Get("playabilityStatus")
		s := ps.Get("status").String()
		if s == "UNPLAYABLE" || s == "LOGIN_REQUIRED" || s == "ERROR" {
			reason := ps.Get("reason").String()
			subreason := ps.Get("errorScreen.playerErrorMessageRenderer.subreason.simpleText").String()
			if reason == "" {
				reason = s
			}
			if subreason != "" {
				reason += " " + subreason
			}
			return nil, fmt.Errorf(reason)
		}
	}
	return &Parser{
		id,
		jsPath,
		player,
		client,
	}, nil
}

// Parse parse video info
func (p *Parser) Parse() (*VideoInfo, error) {
	var (
		v    = p.Player.Get("videoDetails")
		info = &VideoInfo{
			ID:       p.ID,
			Title:    v.Get("title").String(),
			Duration: v.Get("lengthSeconds").String(),
			Author:   v.Get("author").String(),
			Streams:  make(map[string]*StreamItem),
		}
		s   = p.Player.Get("streamingData")
		err error
	)
	var loop = func(key gjson.Result, value gjson.Result) bool {
		var (
			url           string
			itag          = value.Get("itag").String()
			streamType    = value.Get("mimeType").String()
			quality       = value.Get("qualityLabel").String()
			contentLength = value.Get("contentLength").String()
		)
		if quality == "" {
			quality = value.Get("quality").String()
		}
		if value.Get("url").Exists() {
			url = value.Get("url").String()
		} else if value.Get("cipher").Exists() {
			url, err = p.buildURL(value.Get("cipher").String())
		} else if value.Get("signatureCipher").Exists() {
			url, err = p.buildURL(value.Get("signatureCipher").String())
		}
		info.Streams[itag] = &StreamItem{
			quality,
			streamType,
			url,
			itag,
			contentLength,
			&rangeItem{
				value.Get("initRange.start").String(),
				value.Get("initRange.end").String(),
			},
			&rangeItem{
				value.Get("indexRange.start").String(),
				value.Get("indexRange.end").String(),
			},
		}
		return true
	}
	s.Get("formats").ForEach(loop)
	s.Get("adaptiveFormats").ForEach(loop)
	return info, err
}

func (p *Parser) buildURL(cipher string) (string, error) {
	var (
		stream, err = url.ParseQuery(cipher)
	)
	if err != nil {
		return "", err
	}
	if p.JsPath == "" {
		return "", fmt.Errorf("jsPath not found")
	}
	bodystr, err := request.GetURLData(baseURL+p.JsPath, true, p.client)
	if err != nil {
		return "", err
	}
	return getDownloadURL(stream, string(bodystr))
}

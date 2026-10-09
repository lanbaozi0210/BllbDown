package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"bilidown/util"
)

// getYTDLPInfo exposes yt-dlp metadata for URLs not handled by the Bilibili API.
// yt-dlp emits one JSON object per video; playlists are intentionally limited to one item.
func getYTDLPInfo(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !util.IsValidURL(r.FormValue("url")) {
		util.Res{Success: false, Message: "URL 格式错误"}.Write(w)
		return
	}
	url := r.FormValue("url")
	bin := "yt-dlp"
	if _, err := exec.LookPath(bin); err != nil {
		bin = filepath.Join("bin", "yt-dlp")
	}
	out, err := exec.Command(bin, "--no-warnings", "--skip-download", "--playlist-end", "1", "-J", url).Output()
	if err != nil {
		util.Res{Success: false, Message: fmt.Sprintf("yt-dlp 解析失败: %v", err)}.Write(w)
		return
	}
	var raw struct {
		ID          string  `json:"id"`
		WebpageURL  string  `json:"webpage_url"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Uploader    string  `json:"uploader"`
		Thumbnail   string  `json:"thumbnail"`
		Duration    float64 `json:"duration"`
		Width       int     `json:"width"`
		Height      int     `json:"height"`
		UploadDate  string  `json:"upload_date"`
		Formats     []struct {
			URL      string `json:"url"`
			FormatID string `json:"format_id"`
			Vcodec   string `json:"vcodec"`
			Acodec   string `json:"acodec"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
		} `json:"formats"`
	}
	if err := json.Unmarshal(out, &raw); err != nil || raw.ID == "" {
		util.Res{Success: false, Message: "yt-dlp 返回数据无效"}.Write(w)
		return
	}
	var video, audio string
	for _, f := range raw.Formats {
		if video == "" && f.URL != "" && f.Vcodec != "none" && f.Height > 0 {
			video = f.URL
		}
		if audio == "" && f.URL != "" && f.Acodec != "none" && f.Vcodec == "none" {
			audio = f.URL
		}
	}
	if video == "" {
		for _, f := range raw.Formats {
			if f.URL != "" {
				video = f.URL
				break
			}
		}
	}
	util.Res{Success: true, Message: "获取成功", Data: map[string]any{
		"id": "YT:" + raw.ID, "url": strings.TrimSpace(raw.WebpageURL), "title": raw.Title, "description": raw.Description,
		"uploader": raw.Uploader, "thumbnail": raw.Thumbnail, "duration": int(raw.Duration), "width": raw.Width, "height": raw.Height,
		"uploadDate": raw.UploadDate, "video": video, "audio": audio,
	}}.Write(w)
}

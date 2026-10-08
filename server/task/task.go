package task

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"bilidown/bilibili"
	"bilidown/common"
	"bilidown/util"
)

// TaskInitOption 创建任务时需要从 POST 请求获取的参数
type TaskInitOption struct {
	Bvid         string             `json:"bvid"`
	Cid          int                `json:"cid"`
	Format       common.MediaFormat `json:"format"`
	Title        string             `json:"title"`
	Owner        string             `json:"owner"`
	Cover        string             `json:"cover"`
	Status       TaskStatus         `json:"status"`
	Folder       string             `json:"folder"`
	Audio        string             `json:"audio"`
	Video        string             `json:"video"`
	Duration     int                `json:"duration"`
	DownloadType string             `json:"downloadType"`
}

// TaskInDB 任务数据库中的数据
type TaskInDB struct {
	TaskInitOption
	ID       int64     `json:"id"`
	CreateAt time.Time `json:"createAt"`
}

func (task *TaskInDB) FilePath() string {
	ext := ".mp4"
	if task.DownloadType == "audio" {
		ext = ".m4a"
	} else if task.DownloadType == "audio_mp3" {
		ext = ".mp3"
	}
	return filepath.Join(task.Folder,
		fmt.Sprintf("%s %s%s", task.Title,
			strings.Replace(base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(task.ID, 10))), "=", "", -1),
			ext,
		),
	)
}

// done | waiting | running | error
type TaskStatus string

type Task struct {
	TaskInDB
	AudioProgress float64 `json:"audioProgress"`
	VideoProgress float64 `json:"videoProgress"`
	MergeProgress float64 `json:"mergeProgress"`
}

var GlobalTaskList = []*Task{}
var GlobalTaskMux = &sync.Mutex{}
var GlobalDownloadSem = util.NewSemaphore(3)
var GlobalMergeSem = util.NewSemaphore(3)

func (task *Task) Create(db *sql.DB) error {
	util.SqliteLock.Lock()
	result, err := db.Exec(`INSERT INTO "task" ("bvid", "cid", "format", "title", "owner", "cover", "status", "folder", "duration", "download_type")
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.Bvid,
		task.Cid,
		task.Format,
		task.Title,
		task.Owner,
		task.Cover,
		task.Status,
		task.Folder,
		task.Duration,
		task.DownloadType,
	)
	util.SqliteLock.Unlock()
	if err != nil {
		return err
	}

	task.ID, err = result.LastInsertId()
	task.CreateAt = time.Now()
	return err
}

func registerTask(task *Task) {
	GlobalTaskMux.Lock()
	replaced := false
	for i, existing := range GlobalTaskList {
		if existing.ID == task.ID {
			GlobalTaskList[i] = task
			replaced = true
			break
		}
	}
	if !replaced {
		GlobalTaskList = append(GlobalTaskList, task)
	}
	GlobalTaskMux.Unlock()
}

// Start 开始下载，并将任务加入全局任务列表。
func (task *Task) Start() {
	if task.DownloadType == "" {
		task.DownloadType = "merge"
	}
	registerTask(task)
	db := util.MustGetDB()
	defer db.Close()
	sessdata, err := bilibili.GetSessdata(db)
	if err != nil {
		task.UpdateStatus(db, "error", fmt.Errorf("bilibili.GetSessdata: %v", err))
		return
	}
	client := &bilibili.BiliClient{SESSDATA: sessdata}

	GlobalDownloadSem.Acquire()
	task.UpdateStatus(db, "running")

	if task.DownloadType == "audio" || task.DownloadType == "audio_mp3" {
		// 仅音频模式：只下载音频，重命名音频文件为输出文件
		err = DownloadMedia(client, task.Audio, task, "audio")
		if err != nil {
			GlobalDownloadSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("DownloadMedia: %v", err))
			return
		}
		GlobalDownloadSem.Release()
		outputPath := task.TaskInDB.FilePath()
		audioPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+".audio")
		if task.DownloadType == "audio_mp3" {
			if err := task.convertToMP3(audioPath, outputPath); err != nil {
				task.UpdateStatus(db, "error", fmt.Errorf("convertToMP3: %v", err))
				return
			}
			if err := os.Remove(audioPath); err != nil {
				log.Printf("清理已转换音频失败 (任务ID: %d): %v", task.ID, err)
			}
			task.UpdateStatus(db, "done")
			return
		}
		err = os.Rename(audioPath, outputPath)
		if err != nil {
			task.UpdateStatus(db, "error", fmt.Errorf("os.Rename: %v", err))
			return
		}
		// 添加元数据
		if err := task.addMetadata(outputPath); err != nil {
			log.Printf("添加元数据失败 (任务ID: %d): %v", task.ID, err)
		}
		task.UpdateStatus(db, "done")
		return
	} else if task.DownloadType == "video" {
		// 仅视频模式：只下载视频，重命名视频文件为输出文件
		err = DownloadMedia(client, task.Video, task, "video")
		if err != nil {
			GlobalDownloadSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("DownloadMedia: %v", err))
			return
		}
		GlobalDownloadSem.Release()
		outputPath := task.TaskInDB.FilePath()
		videoPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+".video")
		err = os.Rename(videoPath, outputPath)
		if err != nil {
			task.UpdateStatus(db, "error", fmt.Errorf("os.Rename: %v", err))
			return
		}
		// 添加元数据
		if err := task.addMetadata(outputPath); err != nil {
			log.Printf("添加元数据失败 (任务ID: %d): %v", task.ID, err)
		}
		task.UpdateStatus(db, "done")
		return
	} else {
		// 合并模式：下载音频和视频，然后合并
		err = DownloadMedia(client, task.Audio, task, "audio")
		if err != nil {
			GlobalDownloadSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("DownloadMedia: %v", err))
			return
		}
		err = DownloadMedia(client, task.Video, task, "video")
		if err != nil {
			GlobalDownloadSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("DownloadMedia: %v", err))
			return
		}
		GlobalDownloadSem.Release()

		outputPath := task.TaskInDB.FilePath()
		videoPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+".video")
		audioPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+".audio")
		GlobalMergeSem.Acquire()
		err = task.MergeMedia(outputPath, videoPath, audioPath)
		if err != nil {
			GlobalMergeSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("task.MergeMedia: %v", err))
			return
		}
		err = os.Remove(videoPath)
		if err != nil {
			GlobalMergeSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("os.Remove: %v", err))
			return
		}
		err = os.Remove(audioPath)
		if err != nil {
			GlobalMergeSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("os.Remove: %v", err))
			return
		}
		GlobalMergeSem.Release()
		// 添加元数据
		if err := task.addMetadata(outputPath); err != nil {
			log.Printf("添加元数据失败 (任务ID: %d): %v", task.ID, err)
		}
		task.UpdateStatus(db, "done")
	}
}

// convertToMP3 转码并写入常见 MP3 播放器可识别的 ID3 元数据。
func (task *Task) convertToMP3(inputPath, outputPath string) error {
	ffmpegPath, err := util.GetFFmpegPath()
	if err != nil {
		return err
	}
	tempPath := outputPath + ".tmp.mp3"
	defer os.Remove(tempPath)
	cmd := exec.Command(ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", inputPath, "-vn", "-codec:a", "libmp3lame", "-b:a", "192k",
		"-id3v2_version", "3",
		"-metadata", "title="+task.Title,
		"-metadata", "artist="+task.Owner,
		"-metadata", "description="+task.Bvid,
		"-y", tempPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg 转码失败: %w: %s", err, string(output))
	}
	return os.Rename(tempPath, outputPath)
}

// 合并音视频
func (task *Task) MergeMedia(outputPath string, inputPaths ...string) error {
	inputs := []string{}
	for _, path := range inputPaths {
		inputs = append(inputs, "-i", path)
	}

	ffmpegPath, err := util.GetFFmpegPath()
	if err != nil {
		return err
	}

	tempPath := outputPath + ".tmp.mp4"
	defer os.Remove(tempPath)
	cmd := exec.Command(ffmpegPath, append(inputs, "-c:v", "copy", "-c:a", "copy", "-progress", "pipe:1", "-strict", "-2", "-y", tempPath)...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)

	progress := newProgressBar(int64(task.Duration))
	outTimeRegex := regexp.MustCompile(`out_time_ms=(\d+)`) // 毫秒

	for scanner.Scan() {
		line := scanner.Text()
		match := outTimeRegex.FindStringSubmatch(line)
		if len(match) == 2 {
			outTime, err := strconv.ParseInt(match[1], 10, 64)
			if err != nil {
				return err
			}
			progress.current = outTime / 1000000
			GlobalTaskMux.Lock()
			task.MergeProgress = progress.percent()
			GlobalTaskMux.Unlock()
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if err := cmd.Wait(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return err
	}
	GlobalTaskMux.Lock()
	task.MergeProgress = 1
	GlobalTaskMux.Unlock()
	return nil
}

func GetVideoURL(medias []bilibili.Media, format common.MediaFormat) (string, error) {
	for _, code := range []int{12, 7, 13} {
		for _, item := range medias {
			if item.ID == format && item.Codecid == code {
				return item.BaseURL, nil
			}
		}
	}
	return "", errors.New("未找到对应视频分辨率格式")
}

func GetAudioURL(dash *bilibili.Dash) string {
	if dash.Flac != nil {
		return dash.Flac.Audio.BaseURL
	}
	var maxAudioID common.MediaFormat
	var audioURL string
	for _, item := range dash.Audio {
		if item.ID > maxAudioID {
			maxAudioID = item.ID
			audioURL = item.BaseURL
		}
	}
	return audioURL
}

// RetryTask 从数据库中的原任务重新获取有效媒体地址，不创建重复任务。
// 已下载的临时文件会在 DownloadMedia 中校验后尽量续传。
func RetryTask(db *sql.DB, taskID int) (TaskStatus, error) {
	stored, err := GetTask(db, taskID)
	if err != nil {
		return "", err
	}
	if stored.Status != "error" {
		return "", fmt.Errorf("只有失败的任务可以重试，当前状态：%s", stored.Status)
	}
	if output, err := os.Stat(stored.FilePath()); err == nil && output.Size() > 0 && stored.DownloadType != "merge" && stored.DownloadType != "" {
		item := &Task{TaskInDB: *stored}
		if err := item.UpdateStatus(db, "done"); err != nil {
			return "", err
		}
		registerTask(item)
		return "done", nil
	}
	sessdata, err := bilibili.GetSessdata(db)
	if err != nil || sessdata == "" {
		return "", errors.New("登录已失效，请先重新登录")
	}
	client := &bilibili.BiliClient{SESSDATA: sessdata}
	playInfo, err := client.GetPlayInfo(stored.Bvid, stored.Cid)
	if err != nil {
		return "", fmt.Errorf("重新获取下载地址失败: %w", err)
	}
	if playInfo.Dash == nil {
		return "", errors.New("视频没有可用的媒体流")
	}
	if stored.DownloadType != "video" {
		if stored.DownloadType == "audio" {
			stored.Audio = GetAACAudioURL(playInfo.Dash)
		} else {
			stored.Audio = GetAudioURL(playInfo.Dash)
		}
		if stored.Audio == "" {
			return "", errors.New("没有可用的音频流")
		}
	}
	if stored.DownloadType == "video" || stored.DownloadType == "merge" || stored.DownloadType == "" {
		stored.Video, err = GetVideoURL(playInfo.Dash.Video, stored.Format)
		if err != nil {
			return "", fmt.Errorf("原画质已不可用，请从解析页重新选择: %w", err)
		}
	}
	util.SqliteLock.Lock()
	result, err := db.Exec(`UPDATE "task" SET "status" = 'waiting' WHERE "id" = ? AND "status" = 'error'`, taskID)
	util.SqliteLock.Unlock()
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return "", errors.New("任务状态已变化，请刷新列表")
	}
	stored.Status = "waiting"
	item := &Task{TaskInDB: *stored}
	registerTask(item)
	go item.Start()
	return "waiting", nil
}

func GetAACAudioURL(dash *bilibili.Dash) string {
	var maxID common.MediaFormat
	var url string
	for _, item := range dash.Audio {
		if item.ID > maxID {
			maxID = item.ID
			url = item.BaseURL
		}
	}
	return url
}

func (task *Task) UpdateStatus(db *sql.DB, status TaskStatus, errs ...error) error {
	util.SqliteLock.Lock()
	_, err := db.Exec(`UPDATE "task" SET "status" = ? WHERE "id" = ?`, status, task.ID)
	util.SqliteLock.Unlock()
	if err != nil {
		return err
	}
	for _, err := range errs {
		if err != nil {
			err = util.CreateLog(db, fmt.Sprintf("Task-%d-Error: %v", task.ID, err))
			if err != nil {
				log.Fatalln("CreateLog:", err)
			}
		}
	}
	GlobalTaskMux.Lock()
	task.Status = status
	GlobalTaskMux.Unlock()
	return err
}

func DownloadMedia(_ *bilibili.BiliClient, _url string, task *Task, mediaType string) error {
	partialPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+"."+mediaType)
	var existingSize int64
	if fileInfo, err := os.Stat(partialPath); err == nil {
		existingSize = fileInfo.Size()
	} else if !os.IsNotExist(err) {
		return err
	}
	// 媒体 CDN 不接收登录 Cookie。续传前比较文件开头，避免把不同音轨拼接在一起。
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	guest := &bilibili.BiliClient{}
	if existingSize > 0 {
		match, err := partialPrefixMatches(client, guest, _url, partialPath, existingSize)
		if err != nil {
			return fmt.Errorf("校验已下载部分失败: %w", err)
		}
		if !match {
			backupPath := fmt.Sprintf("%s.unmatched-%d", partialPath, time.Now().UnixNano())
			if err := os.Rename(partialPath, backupPath); err != nil {
				return fmt.Errorf("保留不匹配的临时文件失败: %w", err)
			}
			log.Printf("任务 %d 原临时文件与新音源不匹配，已保留在 %s", task.ID, backupPath)
			existingSize = 0
		}
	}
	var resp *http.Response
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		resp, err = requestMedia(client, guest, _url, existingSize, -1)
		if err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && existingSize > 0 {
		if resp.Header.Get("Content-Range") == fmt.Sprintf("bytes */%d", existingSize) {
			setMediaProgress(task, mediaType, 1)
			return nil
		}
		return fmt.Errorf("服务器拒绝续传，现有临时文件大小为 %d 字节", existingSize)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("媒体下载接口返回 HTTP %d", resp.StatusCode)
	}
	appendData := existingSize > 0 && resp.StatusCode == http.StatusPartialContent
	var total int64
	if resp.StatusCode == http.StatusPartialContent {
		start, parsedTotal, err := parseContentRange(resp.Header.Get("Content-Range"))
		if err != nil || start != existingSize {
			return fmt.Errorf("服务器返回不匹配的续传范围：%q", resp.Header.Get("Content-Range"))
		}
		total = parsedTotal
	} else {
		// 服务器忽略 Range 时只能重新下载；确认响应成功后才覆盖旧临时文件。
		existingSize = 0
		total = resp.ContentLength
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendData {
		flags = os.O_WRONLY | os.O_APPEND
	}
	file, err := os.OpenFile(partialPath, flags, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	progress := &progressBar{total: total, current: existingSize}
	setMediaProgress(task, mediaType, progress.percent())
	buf := make([]byte, 64*1024)
	var copied int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written, writeErr := file.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
			copied += int64(n)
			progress.add(n)
			setMediaProgress(task, mediaType, progress.percent())
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if resp.ContentLength >= 0 && copied != resp.ContentLength {
		return io.ErrUnexpectedEOF
	}
	if total > 0 && progress.current != total {
		return fmt.Errorf("下载未完成：%d/%d 字节", progress.current, total)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	setMediaProgress(task, mediaType, 1)
	return nil
}

func requestMedia(client *http.Client, guest *bilibili.BiliClient, mediaURL string, start, end int64) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header = guest.MakeHeader()
	if start > 0 || end >= 0 {
		if end >= 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		} else {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
		}
	}
	return client.Do(req)
}

func partialPrefixMatches(client *http.Client, guest *bilibili.BiliClient, mediaURL, partialPath string, size int64) (bool, error) {
	const maxSample = 64 * 1024
	sampleSize := size
	if sampleSize > maxSample {
		sampleSize = maxSample
	}
	file, err := os.Open(partialPath)
	if err != nil {
		return false, err
	}
	defer file.Close()
	local := make([]byte, sampleSize)
	if _, err := io.ReadFull(file, local); err != nil {
		return false, err
	}
	resp, err := requestMedia(client, guest, mediaURL, 0, sampleSize-1)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return false, fmt.Errorf("校验接口返回 HTTP %d", resp.StatusCode)
	}
	remote := make([]byte, sampleSize)
	if _, err := io.ReadFull(resp.Body, remote); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(local, remote), nil
}

func parseContentRange(header string) (int64, int64, error) {
	match := regexp.MustCompile(`^bytes (\d+)-\d+/(\d+)$`).FindStringSubmatch(header)
	if len(match) != 3 {
		return 0, 0, errors.New("无效的 Content-Range")
	}
	start, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	total, err := strconv.ParseInt(match[2], 10, 64)
	return start, total, err
}

func setMediaProgress(task *Task, mediaType string, value float64) {
	GlobalTaskMux.Lock()
	if mediaType == "video" {
		task.VideoProgress = value
	} else {
		task.AudioProgress = value
	}
	GlobalTaskMux.Unlock()
}

type progressBar struct {
	total   int64
	current int64
}

func (p *progressBar) add(n int) {
	p.current += int64(n)
}

func (p *progressBar) percent() float64 {
	if p.total <= 0 {
		return 0
	}
	return float64(p.current) / float64(p.total)
}

func newProgressBar(total int64) *progressBar {
	return &progressBar{
		total: total,
	}
}

func GetTaskList(db *sql.DB, page int, pageSize int) ([]TaskInDB, error) {
	tasks := []TaskInDB{}
	util.SqliteLock.Lock()
	rows, err := db.Query(`SELECT
		"id", "bvid", "cid", "format", "title",
		"owner", "cover", "status", "folder", "duration", "download_type", "create_at"
	FROM "task" ORDER BY "id" DESC LIMIT ?, ?`,
		page*pageSize, pageSize,
	)
	util.SqliteLock.Unlock()
	if err != nil {
		return nil, err
	}

	createAt := ""

	for rows.Next() {
		task := TaskInDB{}
		err = rows.Scan(
			&task.ID,
			&task.Bvid,
			&task.Cid,
			&task.Format,
			&task.Title,
			&task.Owner,
			&task.Cover,
			&task.Status,
			&task.Folder,
			&task.Duration,
			&task.DownloadType,
			&createAt,
		)
		if err != nil {
			return nil, err
		}
		task.CreateAt, err = time.Parse("2006-01-02 15:04:05", createAt)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func DeleteTask(db *sql.DB, taskID int) error {
	util.SqliteLock.Lock()
	_, err := db.Exec(`DELETE FROM "task" WHERE "id" = ?`, taskID)
	util.SqliteLock.Unlock()
	return err
}

func GetTask(db *sql.DB, taskID int) (*TaskInDB, error) {
	task := TaskInDB{}
	createAt := ""
	util.SqliteLock.Lock()
	err := db.QueryRow(`SELECT
		"id", "bvid", "cid", "format", "title",
		"owner", "cover", "status", "folder", "duration", "download_type", "create_at"
	FROM "task" WHERE "id" = ?`,
		taskID,
	).Scan(
		&task.ID,
		&task.Bvid,
		&task.Cid,
		&task.Format,
		&task.Title,
		&task.Owner,
		&task.Cover,
		&task.Status,
		&task.Folder,
		&task.Duration,
		&task.DownloadType,
		&createAt,
	)
	util.SqliteLock.Unlock()
	if err != nil {
		return nil, err
	}

	task.CreateAt, err = time.Parse("2006-01-02 15:04:05", createAt)
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// addMetadata 使用 ffmpeg 给输出文件添加元数据（description 和 artist）
func (task *Task) addMetadata(filePath string) error {
	ffmpegPath, err := util.GetFFmpegPath()
	if err != nil {
		return err
	}

	desc := task.Bvid
	if desc == "" {
		desc = ""
	}

	author := task.Owner

	// 临时文件加上 .mp4 扩展名
	tempPath := filePath + ".tmp.mp4"

	// 使用双引号包裹文件路径，避免特殊字符
	cmd := exec.Command(ffmpegPath,
		"-i", filePath,
		"-metadata", "description="+desc,
		"-metadata", "artist="+author,
		"-codec", "copy",
		"-y",
		tempPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg添加元数据失败: %v, 输出: %s", err, string(output))
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("删除原文件失败: %v", err)
	}
	if err := os.Rename(tempPath, filePath); err != nil {
		return fmt.Errorf("重命名临时文件失败: %v", err)
	}

	return nil
}

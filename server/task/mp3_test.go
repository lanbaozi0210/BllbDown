package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertToMP3(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "tone.m4a")
	output := filepath.Join(dir, "tone.mp3")
	synth := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "aac", "-y", input)
	if data, err := synth.CombinedOutput(); err != nil {
		t.Fatalf("create source audio: %v: %s", err, data)
	}
	item := &Task{TaskInDB: TaskInDB{TaskInitOption: TaskInitOption{Title: "Test tone", Owner: "BllbDown Test", Bvid: "BV1test"}}}
	if err := item.convertToMP3(input, output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
	inspect := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name:format_tags=title,artist", "-of", "default=noprint_wrappers=1", output)
	data, err := inspect.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect mp3: %v: %s", err, data)
	}
	if !strings.Contains(string(data), "codec_name=mp3") || !strings.Contains(string(data), "artist=BllbDown Test") || !strings.Contains(string(data), "title=Test tone") {
		t.Fatalf("unexpected MP3 output: %s", data)
	}
}

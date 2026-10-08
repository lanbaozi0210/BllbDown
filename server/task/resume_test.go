package task

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadMediaResume(t *testing.T) {
	source := bytes.Repeat([]byte("BllbDown-audio-frame-"), 9000)
	cases := []struct {
		name          string
		initial       []byte
		supportsRange bool
		expectBackup  bool
	}{
		{"range_resume", source[:50000], true, false},
		{"already_complete", source, true, false},
		{"range_ignored", source[:50000], false, false},
		{"different_source", bytes.Repeat([]byte("x"), 50000), true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rangeSeen := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rangeValue := r.Header.Get("Range")
				if rangeValue != "" {
					rangeSeen = true
				}
				if !tc.supportsRange || rangeValue == "" {
					w.Header().Set("Content-Length", strconv.Itoa(len(source)))
					w.Write(source)
					return
				}
				if !strings.HasPrefix(rangeValue, "bytes=") {
					t.Errorf("unexpected Range: %s", rangeValue)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				parts := strings.Split(strings.TrimPrefix(rangeValue, "bytes="), "-")
				start, err := strconv.Atoi(parts[0])
				if err != nil {
					t.Errorf("invalid Range: %s", rangeValue)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if start >= len(source) {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(source)))
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return
				}
				end := len(source) - 1
				if parts[1] != "" {
					requestedEnd, err := strconv.Atoi(parts[1])
					if err == nil && requestedEnd < end {
						end = requestedEnd
					}
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(source)))
				w.WriteHeader(http.StatusPartialContent)
				w.Write(source[start : end+1])
			}))
			defer server.Close()
			folder := t.TempDir()
			partialPath := filepath.Join(folder, "11.audio")
			if err := os.WriteFile(partialPath, tc.initial, 0600); err != nil {
				t.Fatal(err)
			}
			item := &Task{TaskInDB: TaskInDB{ID: 11, TaskInitOption: TaskInitOption{Folder: folder}}}
			if err := DownloadMedia(nil, server.URL, item, "audio"); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(partialPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, source) || item.AudioProgress != 1 {
				t.Fatalf("unexpected result: bytes=%d progress=%v", len(got), item.AudioProgress)
			}
			if !rangeSeen {
				t.Fatal("expected a Range request")
			}
			backups, err := filepath.Glob(partialPath + ".unmatched-*")
			if err != nil {
				t.Fatal(err)
			}
			if (len(backups) == 1) != tc.expectBackup {
				t.Fatalf("unexpected backups: %v", backups)
			}
		})
	}
}

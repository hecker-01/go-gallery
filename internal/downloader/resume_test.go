package downloader

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResumeResponseValidation(t *testing.T) {
	for _, kind := range []string{"valid", "ignored", "bad-range", "416", "short"} {
		t.Run(kind, func(t *testing.T) {
			payload := makeJPEGPayload(2048)
			dest := filepath.Join(t.TempDir(), "file.jpg")
			part := dest + ".part"
			os.WriteFile(part, payload[:1024], 0600)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "bytes=1024-" {
					t.Error("missing resume range")
				}
				switch kind {
				case "valid":
					w.Header().Set("Content-Range", "bytes 1024-2047/2048")
					w.WriteHeader(206)
					w.Write(payload[1024:])
				case "bad-range":
					w.Header().Set("Content-Range", "bytes 0-1023/2048")
					w.WriteHeader(206)
					w.Write(payload[:1024])
				case "ignored":
					w.Write(payload)
				case "416":
					w.Header().Set("Content-Range", "bytes */2048")
					w.WriteHeader(416)
				case "short":
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
					w.Write(payload[:1200])
				}
			}))
			defer srv.Close()
			err := New(srv.Client()).attemptDownload(context.Background(), srv.URL, dest, part, Config{Resume: true})
			if kind == "valid" || kind == "ignored" {
				if err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(dest)
				if !bytes.Equal(got, payload) {
					t.Fatal("corrupt output")
				}
			} else {
				if err == nil {
					t.Fatal("accepted invalid transfer")
				}
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatal("promoted partial")
				}
			}
		})
	}
}

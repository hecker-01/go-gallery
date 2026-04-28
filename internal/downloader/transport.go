// Package downloader provides HTTP and yt-dlp based media download implementations.
package downloader

import (
	"net/http"
	"time"
)

// NewTransport returns a shared http.Transport tuned for media download
// throughput: HTTP/2 enabled, connection pooling, longer idle timeouts.
func NewTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// NewHTTPClient returns an *http.Client backed by NewTransport.
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &http.Client{
		Transport: NewTransport(),
		Timeout:   timeout,
	}
}

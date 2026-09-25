package gallery_test

import (
	"context"
	"fmt"

	gallery "github.com/hecker-01/go-gallery"
)

func Example_downloadUserMedia() {
	client := gallery.NewClient(
		gallery.WithConcurrency(4),
	)
	defer client.Close()

	result, err := client.Download(
		context.Background(),
		"https://twitter.com/exampleuser/media",
		gallery.WithOutputDir("./downloads"),
		gallery.WithSimulate(true),
		gallery.WithFilter(gallery.AllOf()),
	)
	if err != nil {
		// A failed extraction can still return successfully downloaded files.
		_ = result
		_ = err
	}
	fmt.Println("download example executed")
}

// Smart refresh shares the account budget across clients and drains accepted
// transfers after reaching the first existing item. This example is compile-only.
func Example_smartRefresh() {
	rates := gallery.NewRateLimitRegistry()
	client := gallery.NewClient(gallery.WithRateLimitRegistry(rates))
	defer client.Close()
	result, err := client.Download(context.Background(), "https://x.com/example/media",
		gallery.WithDirectOutputDir("./downloads/example"),
		gallery.WithStopAfterExisting(1),
		gallery.WithDownloadObserver(func(e gallery.DownloadEvent) { fmt.Println(e.Kind, e.Path) }),
	)
	fmt.Println(result.StoppedEarly, err)
}

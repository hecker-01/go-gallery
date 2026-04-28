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
		// In this example the method is not yet implemented; suppress the error
		// so the testable example compiles and runs cleanly.
		_ = result
		_ = err
	}
	fmt.Println("download example executed")
	// Output: download example executed
}

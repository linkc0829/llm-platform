// Command healthcheck is the container health probe: the distroless image has
// no curl or wget. It GETs one URL and exits 0 on 2xx, 1 otherwise.
//
//	healthcheck http://127.0.0.1:12599/healthz
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

const timeout = 3 * time.Second

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: healthcheck <url>")
		os.Exit(2)
	}
	if err := check(os.Args[1], timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("get %s: status %d", url, resp.StatusCode)
	}
	return nil
}

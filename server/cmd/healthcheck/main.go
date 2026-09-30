// Command healthcheck is a dependency-free probe for distroless service
// images. It deliberately bypasses environment proxy variables so a local
// readiness check cannot leave the container or depend on the proxy stack.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	target := "http://127.0.0.1:8080/readyz"
	if len(os.Args) == 2 {
		target = os.Args[1]
	} else if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: healthcheck [url]")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	transport := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: -1,
		}).DialContext,
		TLSHandshakeTimeout: 2 * time.Second,
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid healthcheck URL:", err)
		os.Exit(2)
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck request failed:", err)
		os.Exit(1)
	}
	defer response.Body.Close()
	_, _ = io.CopyN(io.Discard, response.Body, 4<<10)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		fmt.Fprintln(os.Stderr, "healthcheck returned", response.Status)
		os.Exit(1)
	}
}

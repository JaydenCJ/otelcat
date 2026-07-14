// Helper for scripts/smoke.sh: POSTs a body to a URL and prints the
// HTTP status code, so the smoke test can assert on rejection paths
// without depending on curl being installed.
//
// Usage: go run ./scripts/httppost.go <url> <content-type> <body>
package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: httppost <url> <content-type> <body>")
		os.Exit(2)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(os.Args[1], os.Args[2], strings.NewReader(os.Args[3]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "httppost: %v\n", err)
		os.Exit(1)
	}
	resp.Body.Close()
	fmt.Println(resp.StatusCode)
}

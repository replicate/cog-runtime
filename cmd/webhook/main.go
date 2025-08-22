package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("===== Request =====")
		fmt.Println(r.Method, r.URL.Path)
		fmt.Println("----- Headers -----")
		for k, v := range r.Header {
			fmt.Printf("%s: %v\n", k, v)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			fmt.Printf("failed to read body: %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		bodyStr := string(body)
		if bodyStr != "" {
			fmt.Println("----- Body -----")
			fmt.Println(bodyStr)
		}
		w.WriteHeader(http.StatusOK)
	})
	fmt.Println("Listening on :5150")
	if err := http.ListenAndServe(":5150", nil); err != nil { //nolint:gosec // test utility only
		fmt.Printf("failed to start server: %v\n", err)
		os.Exit(1)
	}
}

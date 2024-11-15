package main

import (
	"fmt"
	"io"
	"net/http"

	"github.com/replicate/go/must"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println(r.Method, r.URL.Path)
		body := string(must.Get(io.ReadAll(r.Body)))
		if body != "" {
			fmt.Println(body)
		}
		w.WriteHeader(http.StatusOK)
	})
	fmt.Println("Listening on :5150")
	http.ListenAndServe(":5150", nil)
}

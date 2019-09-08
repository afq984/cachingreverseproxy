package main // import "github.com/afq984/cachingreverseproxy"

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/afq984/cachingreverseproxy/single"
)

func main() {
	var upstream string
	var cachedir string
	var port int
	flag.StringVar(&upstream, "upstream", "http://mirror.archlinux.example.org", "upstream mirror URL")
	flag.StringVar(&cachedir, "cachedir", "cache.d", "directory to store the cache")
	flag.IntVar(&port, "port", 8000, "http port to serve")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	http.Handle("/", single.NewCachingReverseProxy(upstream, cachedir))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

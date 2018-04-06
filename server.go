package main

import (
	"flag"
	"runtime"

	"./server"
)

var (
	addr = flag.String("addr", ":8080", "http service address")
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()

	server.Start(*addr)
}

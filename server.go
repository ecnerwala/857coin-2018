package main

import (
	"flag"
	"runtime"

	"github.com/cfromknecht/857coin/server"
)

var (
	addr = flag.String("addr", ":8080", "http service address")
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()

	server.NewExplorer(*addr)
}

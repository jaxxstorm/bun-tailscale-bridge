package main

import (
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/process"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/tsnode"
)

func main() { process.Main(tsnode.New) }

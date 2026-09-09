// This command is TEST ONLY. Never package it as a production helper.
package main

import (
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/process"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/testhelper/simnode"
)

func main() { process.Main(simnode.New) }

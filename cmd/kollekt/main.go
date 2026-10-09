package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/yniverz/kollekt/internal/app"
)

func main() {
	cfg := app.ConfigFromEnv()
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		addr := cfg.Addr
		if len(addr) > 0 && addr[0] == ':' {
			addr = "127.0.0.1" + addr
		}
		cl := http.Client{Timeout: 3 * time.Second}
		res, err := cl.Get("http://" + addr + "/healthz")
		if err != nil || res.StatusCode != 200 {
			fmt.Fprintln(os.Stderr, "unhealthy")
			os.Exit(1)
		}
		return
	}
	if err := app.Run(cfg); err != nil {
		log.Fatal(err)
	}
}

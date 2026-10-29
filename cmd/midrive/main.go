package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/cham/xiaomi-netdisk-pro/internal/app"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dir := flag.String("data-dir", app.DefaultDir(), "设置与待恢复文件目录")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := app.Run(ctx, *dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

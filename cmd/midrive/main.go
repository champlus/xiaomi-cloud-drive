package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/champlus/xiaomi-cloud-drive/internal/app"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dir := flag.String("data-dir", app.DefaultDir(), "设置与待恢复文件目录")
	nativeSession := flag.Bool("native-session", false, "由原生 App 管理钥匙串会话")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := app.Run(ctx, *dir, *nativeSession); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

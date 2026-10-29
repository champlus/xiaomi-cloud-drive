package main

import (
	"encoding/json"
	"fmt"
	"github.com/champlus/xiaomi-cloud-drive/internal/app"
	"github.com/champlus/xiaomi-cloud-drive/internal/cli"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		r := cli.Runner{}
		os.Exit(r.Run(args, os.Stdout, os.Stderr))
	}
	dir := os.Getenv("MIDRIVE_DATA_DIR")
	if dir == "" {
		dir = app.DefaultDir()
	}
	b, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	var runtime app.Runtime
	if err != nil || json.Unmarshal(b, &runtime) != nil {
		message := "米盘尚未运行，请先打开米盘 App"
		if strings.Contains(strings.Join(args, " "), "--json") {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"schemaVersion": 1, "ok": false, "error": map[string]string{"code": "not_running", "message": message}})
		} else {
			fmt.Fprintln(os.Stderr, message)
		}
		os.Exit(1)
	}
	r := cli.Runner{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", runtime.Port), Token: runtime.Token, Password: runtime.Password}
	os.Exit(r.Run(args, os.Stdout, os.Stderr))
}

# 米盘

把小米云盘挂载到 macOS Finder 的轻量菜单栏 App。SwiftUI 原生设置，Go 负责云盘接口和本地 WebDAV；登录后自动启动、自动挂载，无需 macFUSE。

已实现目录浏览、按需读取、分段下载、上传、文件夹创建、重命名、移动、复制、删除，以及供脚本和 agent 使用的 CLI。设置布局沿用本机 Lyli 项目的侧栏和卡片规范。

## 使用

需要 macOS 14 或更新版本。开发构建使用当前 Mac 的架构。

```sh
scripts/build.sh
scripts/install.sh
```

打开 `~/Applications/米盘.app`，在小米官方页面登录，然后在 Finder 中访问 `~/小米云盘`。关闭设置窗口后，服务继续在菜单栏运行。登录时启动与自动挂载可在「通用」中调整。

会话保存在 macOS 钥匙串；设置和待恢复文件位于 `~/Library/Application Support/米盘/`。服务仅监听本机回环地址，挂载使用仅在本机解析的 `MiCloud.local`，不修改 hosts 或路由。

CLI 安装在 `~/.local/bin/mipan`，与 App 共用登录状态。

```sh
mipan --json status
mipan --json ls /售后凭据
mipan put ./receipt.pdf /售后凭据/receipt.pdf
mipan get /售后凭据/receipt.pdf ./receipt.pdf
```

完整用法见 [CLI.md](docs/CLI.md)。如果 shell 找不到 `mipan`，可直接运行 `~/.local/bin/mipan`，或把 `~/.local/bin` 加入 PATH。

## 开发与验证

```sh
go test ./...
go test -race ./...
go vet ./...
swiftc macos/LocalServiceName.swift tests/NativeTests.swift -o .build/native-tests
.build/native-tests
# 安装后检查图标和应用签名
python3 tests/package_test.py
```

真实账号的验收测试默认不运行。已登录并挂载后，可显式启用；测试只操作随机命名的独立测试目录，结束后移到云端最近删除。

```sh
MIDRIVE_LIVE=1 python3 tests/live_integration.py
MIDRIVE_LIVE=1 python3 tests/cli_live.py
```

按 TDD 增补功能：先添加并运行失败的行为测试，再修复实现。Finder 协议、失败恢复、同名覆盖、锁、Unicode、缓存、并发和 CLI 均有测试。开发中更新运行的 App：先构建，再执行 `python3 scripts/reload-dev.py`；存在活动传输时会停止更新。

## 当前边界

- 小米拒绝零字节云端文件，因此空文件是按账号隔离、持久保存在本机的占位，写入内容后才上传；它们不会出现在其他设备。
- 单文件需小于 4 GiB。端到端加密账号暂不支持。
- 这是在线挂载，不是离线同步盘。目录在内存缓存 30 秒，过期后可立即显示缓存并后台刷新；最长保留 5 分钟。可用 CLI `refresh` 主动刷新。
- 上传先落入本机暂存，再上传和确认；替换同名文件前保留旧版本。失败副本可在「传输」中找到。应用意外退出时，接收未完成的副本会明确标为不完整。
- Finder 的 `.DS_Store` 和 AppleDouble 元数据不会上传；资源叉和扩展属性不作为云端文件存储。
- 使用小米网页版私有接口。接口变动或登录会话失效时需要重新连接。首次登录、验证码和二次验证在官方页面完成。

接口调研和实现取舍见 [RESEARCH.md](docs/RESEARCH.md)。

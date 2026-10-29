# 开发与测试

SwiftUI 负责原生设置和菜单栏，Go 负责小米云盘接口、WebDAV 与 CLI。macOS 通过 NetFS 挂载，本地服务名称由 DNS-SD 注册。

## 构建

需要 Go 1.25.2+、Xcode Command Line Tools 和 macOS 14+。

```sh
scripts/build.sh
scripts/install.sh
```

本地开发构建必须使用固定代码签名身份，避免每版临时签名改变钥匙串信任身份。可设置 `MIDRIVE_SIGN_IDENTITY`，或在 `~/Library/Application Support/米盘构建/signing.json` 保存公开的证书指纹：

```json
{"identity":"证书的40位SHA-1指纹"}
```

用 `security find-identity -v -p codesigning` 查看可用身份。配置的身份不可用时构建会失败，不会自动退回临时签名；私钥留在钥匙串中。CI 可以临时签名，本机临时测试需显式设置 `MIDRIVE_SIGN_IDENTITY=-`，这类构建不保证跨版本继承授权。稳定签名构建后运行 `python3 tests/signing_test.py` 检查签名连续性。但自签名程序的默认钥匙串条目还受 CDHash 分区限制，相同证书并不足以证明实际更新后仍可读取。

会话由 `credential-helper` 保存。它只接受具有相同证书和 App 标识的真实父进程，App 也会在传递数据前检查正在运行的小组件；数据仅走管道。小组件的签名二进制缓存在 `~/Library/Application Support/米盘构建/credential-helper/`，界面更新和编译器更新都复用同一份字节。不要在发布时重新签名它，否则会改变已有用户的钥匙串访问身份。`native/credential-helper.json` 固定正式发布签名身份、源码和二进制校验值；同一发布身份出现不匹配会中止构建，避免默默让用户再次授权。更换发布机器时应保留这份签名构建缓存；变更小组件本身需要专门设计迁移，不能仅修改源码后假定授权自然继承。CI 的临时签名构建用于编译、打包检查，不支持此凭证协议。

旧版钥匙串只在尚无新存储时读取。需要系统确认时显示「恢复登录」；新用户首次登录直接由小组件保存，不需要执行旧版迁移。已经存在的新存储（包括退出后的空记录）不会回读旧登录。

运行实际本机钥匙串回归：

```sh
MIDRIVE_KEYCHAIN_TEST=1 scripts/test.sh
```

该测试仅创建、读写并删除唯一命名的模拟条目，不读取小米会话、不访问网络。它使用两个代码哈希不同的 App 验证升级继承，并检查未授权调用者和伪造的小组件被拒绝。

构建产物为 `dist/米盘.app`，安装到 `~/Applications/米盘.app`。CLI 链接位于 `~/.local/bin/mipan`。

更新正在运行的开发版本：

```sh
scripts/build.sh
python3 scripts/reload-dev.py
```

## TDD

修改云盘接口、WebDAV、持久化或 CLI 时，先添加行为测试并运行出失败，再实现功能。

```sh
scripts/test.sh
```

该命令运行 Go 单测、竞态检查、静态检查、原生服务名称测试和登录保持策略测试。登录测试模拟会话轮换、60 天有效期的重启恢复、失败后的退避与请求恢复，不等待真实月份，也不接触真实账号凭据。CI 同时检查 App 构建与发布包。

图标以 `macos/AppIcon.png` 为唯一输入，每次构建生成各分辨率 ICNS，图案占画布 80%，四周保留透明边距。运行中的图标与 Finder 共用该 ICNS，不能再用无边距 PNG 覆盖。图标文件名包含图案及生成脚本的内容摘要，安装时刷新 Launch Services 登记，以免更新后启动台仍用旧缓存。

安装后可检查应用签名与图标：

```sh
python3 tests/package_test.py
swift tests/icon_test.swift "$PWD"
python3 tests/launchpad_icon_test.py
```

真实账号验收默认不运行。登录并挂载后，可以显式启用：

```sh
MIDRIVE_LIVE=1 python3 tests/live_integration.py
MIDRIVE_LIVE=1 python3 tests/cli_live.py
```

测试只操作独立命名的测试目录，结束后将目录移到云端最近删除。

## 发布包

```sh
scripts/build.sh
scripts/package.sh
python3 tests/release_test.py
python3 tests/credential_release_test.py
```

发布包只包含 App、独立 CLI 和校验文件，不包含登录状态、本机配置或日志。

## 传输状态

`internal/transfer` 采集实际读写字节、计算最近三秒的速度并持久化按账号隔离的文件记录。进度更新仅写内存；任务开始和结束时原子保存私有文件。`/api/status` 返回结构化的 active、completed、failed 列表。

Finder 的流式 PUT 可能提供 `X-Expected-Entity-Length` 后保持请求流打开；`internal/dav/finder_test.go` 使用本地 TCP 复现并检查完整接收、错误长度和原文件保护。该测试与传输进度测试不访问真实小米账号。原生速度格式、时间、进度与退出保护由 `tests/TransferTests.swift` 覆盖，统一纳入 `scripts/test.sh`。

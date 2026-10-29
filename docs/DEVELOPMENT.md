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

用 `security find-identity -v -p codesigning` 查看可用身份。配置的身份不可用时构建会失败，不会自动退回临时签名；私钥留在钥匙串中。CI 可以临时签名，本机临时测试需显式设置 `MIDRIVE_SIGN_IDENTITY=-`，这类构建不保证跨版本继承授权。稳定签名构建后运行 `python3 tests/signing_test.py`，会比较两份不同程序的签名身份，验证更新不会改变钥匙串识别的身份。

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

安装后可检查应用签名与图标：

```sh
python3 tests/package_test.py
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
```

发布包只包含 App、独立 CLI 和校验文件，不包含登录状态、本机配置或日志。

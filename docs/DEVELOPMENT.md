# 开发与测试

SwiftUI 负责原生设置和菜单栏，Go 负责小米云盘接口、WebDAV 与 CLI。macOS 通过 NetFS 挂载，本地服务名称由 DNS-SD 注册。

## 构建

需要 Go 1.25.2+、Xcode Command Line Tools 和 macOS 14+。

```sh
scripts/build.sh
scripts/install.sh
```

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

该命令运行 Go 单测、竞态检查、静态检查和原生服务名称测试。CI 同时检查 App 构建与发布包。

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

# 调研记录 · 2026-10-29

## 已验证的接口

根据 [小米云盘官方网页版](https://i.mi.com/drive/h5) 的实际请求，以独立测试目录验证。现行网页版源码用于识别请求字段；项目没有复制第三方登录代码，也不接触账号密码。

| 功能 | 当前网页版接口 |
| --- | --- |
| 分页目录 | `GET /drive/v2/user/folders/children` |
| 创建目录 | `POST /drive/v2/user/folders/create` |
| 文件下载元数据 | `GET /drive/v2/user/files/download` |
| 创建上传 | `POST /drive/v2/user/files/create` |
| 确认上传 | `POST /drive/v2/user/files/commit` |
| 重命名 | `POST /drive/v2/user/records/rename` |
| 移动、删除 | `POST /drive/v2/user/records/filemanager` |
| 异步操作确认 | `GET /drive/v2/user/records/taskquery` |

文件 ID 保留为字符串，避免超过 JavaScript 安全整数范围时损坏。上传按 4 MiB 分片计算 SHA-1/MD5。下载经过 JSONP 元数据获取存储地址，存储节点使用独立 HTTP client，不携带小米登录 Cookie。

[wangbjun/go-micloud](https://github.com/wangbjun/go-micloud) 提供了旧版接口的参考，但没有直接作为依赖；当前分页和上传接口已与其实现不同。[XiaomiAlbumSyncer](https://github.com/Coooolfan/XiaomiAlbumSyncer) 主要面向相册，未作为通用云盘文件系统使用。

## Finder 兼容性实测

macOS 自带 WebDAV 客户端即可挂载，不需要 FUSE。Swift 通过 NetFS 挂载并直接传递凭据，密码不放进进程参数或 URL。

服务器项和卷名不同。卷名通过挂载 URL 的末级路径设为「小米云盘」；服务器项使用本机 DNS-SD 注册的 `MiCloud.local`。记录仅在 local-only 接口上生效，A 记录指向 `127.0.0.1`，App 退出即注销。

实际验收发现并修复：

- 目录 PROPFIND 和未知类型 HEAD 会触发内容探测，造成无谓下载。通过 ContentTyper 和响应 MIME 元数据消除。
- 多个并发目录请求重复访问云端。加入请求合并、30 秒缓存和后台刷新；浏览不等待上传互斥锁。
- 小米确认重命名后，目录索引存在短暂延迟。写操作等待索引可读后才返回成功，避免连续 COPY/MOVE 出现 404。
- 小米以 `invalid size` 拒绝空文件，而 Finder 创建文件先提交空占位。用本机持久占位兼容，再在非空写入时正常上传。
- 系统 WebDAV 的默认 MOVE 覆盖流程会先删除目标。自定义写入和移动处理，先暂存新版本，再切换文件名，最后把旧版本移入最近删除。

## 设计

设置使用 SwiftUI NavigationSplitView，布局参考 Lyli 的 SettingsPage、SettingsCard 和 SettingsRow：20 点图标列、12 点间距、14 点水平内边距、11 点垂直内边距。分为云盘、通用、传输，常用操作直接可见。

应用图标使用小米云服务图标。官方登录页面是唯一的 WebKit 界面，设置本身不包含网页。

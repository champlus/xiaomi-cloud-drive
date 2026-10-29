# CLI 与 agent 接入

`mipan` 调用正在运行的米盘服务，不需要 agent 处理小米密码或 Cookie。未运行时先打开米盘 App。

## 安装

从源码安装会自动设置命令。使用下载的 App 时，可运行：

```sh
mkdir -p ~/.local/bin
ln -sfn "/Applications/米盘.app/Contents/MacOS/mipan" ~/.local/bin/mipan
```

如果 App 放在个人「应用程序」目录，将 `/Applications` 改为 `~/Applications` 对应的实际路径。可直接运行 `~/.local/bin/mipan`；也可把 `~/.local/bin` 加入 PATH。

## 用法

```sh
~/.local/bin/mipan --json status
~/.local/bin/mipan --json ls /售后凭据
~/.local/bin/mipan stat '/售后凭据/发票.pdf'
~/.local/bin/mipan cat /notes.txt
~/.local/bin/mipan put ./notes.txt /notes.txt
~/.local/bin/mipan get /notes.txt ./download.txt
~/.local/bin/mipan mkdir /资料
~/.local/bin/mipan mv /notes.txt /资料/notes.txt
~/.local/bin/mipan cp /资料/notes.txt /资料/copy.txt
~/.local/bin/mipan rm /资料/copy.txt
~/.local/bin/mipan put --recursive ./folder /folder
~/.local/bin/mipan rm --recursive /folder
~/.local/bin/mipan refresh
~/.local/bin/mipan recoveries --json
```

远端路径始终以云盘根目录为起点，不是 Finder 挂载目录。路径含空格时加引号。上传会替换同名文件；下载默认拒绝覆盖本地文件，显式使用 `--overwrite` 可以覆盖。删除目录需要 `--recursive`，根目录不可删除。服务器端复制可以复制整个目录。

`--json` 可放在任意位置。成功退出码为 0，操作失败为 1，用法错误为 2。进度和凭据不会混入 stdout。

成功：

```json
{"schemaVersion":1,"ok":true,"data":[{"name":"notes.txt","path":"/notes.txt","type":"file","size":12}]}
```

失败：

```json
{"schemaVersion":1,"ok":false,"error":{"code":"http_404","message":"file does not exist"}}
```

`cat --json` 返回 `contentBase64`，限制 16 MiB；较大内容使用 `get`。普通 `cat` 直接输出字节流。下载先写入临时文件，传输完整后才发布到目标路径。

给 agent 的简短说明可以写成：

> 使用 `~/.local/bin/mipan --json` 访问我的小米云盘。先查询 status，再按任务执行 ls、stat、get、put、mkdir、mv、cp 或 rm。远端路径以 `/` 为根；目录上传和删除需要 `--recursive`。依照用户授权范围执行写操作。

使用非默认开发服务时，可通过 `MIDRIVE_DATA_DIR` 指向该服务的数据目录。

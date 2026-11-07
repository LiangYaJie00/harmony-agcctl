# harmony-agcctl

牵手 HarmonyOS 正式发布使用的本地命令行工具，命令名是 `agcctl`。它一次只执行一个华为 AppGallery Connect 操作，然后退出。任务状态、审批、轮询和页面不在这个工具里。

每条命令都要同时提供本机配置和一份请求 JSON。标准输出只有一份响应 JSON，标准错误是脱敏日志。

## 下载和安装

需要 Python 3.11。仓库地址是 `git@github.com:LiangYaJie00/harmony-agcctl.git`。

```bash
git clone git@github.com:LiangYaJie00/harmony-agcctl.git
cd harmony-agcctl
python3.11 -m venv .venv
.venv/bin/pip install -r requirements.lock
.venv/bin/pip install -e .
.venv/bin/agcctl --version
```

安装完成后用 `.venv/bin/agcctl` 调用。下文的 `agcctl` 都是指这个命令。

`private.json` 和本机的 `config.yaml` 不在仓库里。复制示例后再填写：

```bash
cp config.example.yaml config.yaml
cp private.example.json private.json
chmod 0600 private.json
```

`config.yaml` 至少要改这几项：

- `appId`：AppGallery Connect 里的应用 ID。
- `bundleName`：应用包名。牵手正式包是 `com.qianshou.billionclub`。
- `privateKeyFile`：Service Account 私钥文件的绝对路径。本机调试可以指向上面复制出的 `private.json`。
- `artifactRoot`：APP Pack 所在目录。上传文件必须放在 `artifactRoot/<taskId>/` 下面。
- `logDir`、`runDir`：当前用户可写。
- `releaseUser`：运行命令的系统用户名。

`private.json` 填 Service Account 的 `key_id`、`sub_account` 和 `private_key`。示例里的值都是占位，不能直接使用。文件权限保持 `0600`。

## 调用方式

```bash
agcctl <分组> <动作> --config config.yaml --request-file <请求.json>
```

`doctor` 没有分组和动作：

```bash
agcctl doctor --config config.yaml --request-file doctor.json
```

请求文件外层固定为：

```json
{
  "schemaVersion": "1.0",
  "taskId": "<taskId>",
  "operationKey": "<taskId>:<动作>",
  "toolVersion": "0.1.0",
  "appId": "<appId>",
  "request": {}
}
```

`appId` 必须和 `config.yaml` 一致。`operationKey` 必须以 `taskId:` 开头。`toolVersion` 必须等于 `agcctl --version` 的版本。每次只跑一条命令。变更命令成功后，先保存响应里的外部 ID，再跑下一条。

退出码：

| 退出码 | 含义 |
| --- | --- |
| `0` | 本次调用成功。包是否解析通过、版本是否已上架，还要看响应里的状态 |
| `10` | 可以重试 |
| `20` | 不能重试 |
| `30` | 变更请求已经发出，但结果未知。先查询，不要用同一份请求再发一次 |
| `40` | 本地配置、私钥、文件或请求字段有问题 |
| `50` | 工具内部错误 |

这五个 ID 不能混用：

| 名称 | 从哪条命令得到 | 后面用来做什么 |
| --- | --- | --- |
| `objectId` | `upload package` | 测试选包和正式关联都用它，再加上同一个 `fileName` |
| 测试 `versionId` | `test create-version` | AppTest 版本 |
| 测试 `pkgId` | `test add-package` | 查询测试包解析 |
| 正式 `packageId` | `publish update-package` | 查询正式包解析 |
| 正式 `publishVersionId` | `publish get-app-info` 或 `publish list-versions` | 审核、上架、灰度和转全网 |

每个命令的完整请求字段见 [docs/publish-agcctl-commands.md](docs/publish-agcctl-commands.md)。

## 命令

按正式发布顺序使用。查询命令可以重复执行。创建、提交、撤销审核、停止、删除和转全网在退出码为 `30` 时不要原样重放。

### 准备

`agcctl doctor` 检查配置、私钥、时钟和华为接口，并用包名核对 App ID。`request` 为 `{}`。退出码 `0` 后再继续。

`agcctl publish get-app-info` 读取当前正式应用。不传 `publishVersionId` 时返回当前正式版本。

`agcctl publish list-versions` 按 `request.versionCode` 核对正式版本。只把 `releaseType` 为 `1` 的记录计入候选，测试版本即使版本码相同也不算。

### 上传

`agcctl upload package` 上传 APP Pack。`request` 需要：

- `artifactPath`：`artifactRoot/<taskId>/` 里的绝对路径。
- `fileName`：文件名，必须和路径中的文件名一致。
- `contentLength`：文件字节数。
- `sha256`：64 位小写 SHA-256。

成功后保存 `externalIds.objectId` 和 `externalIds.fileName`。

### 工程检测

`agcctl test create-version` 新建测试草稿。`request` 可带 `testDesc`。不传 `onshelfSelfDetect` 时发送 `0`，这次测试版本不做上架自检；需要自检时传 `1`。保存 `externalIds.versionId`，这是测试 `versionId`。

`agcctl test add-package` 把已上传的包选进这个草稿。`request` 只带 `fileName` 和 `objectId`，不包含安装包本身。保存 `externalIds.pkgId`，这是测试 `pkgId`。

`agcctl package compile-status` 查询解析。`request.pkgId` 传刚才的测试 `pkgId`。`successStatus` 为 `0` 通过，`1` 处理中，`2` 不可用。

### 提交测试版本

`agcctl test list-groups` 查询测试群组。不传 `groupType` 时查询内部群组。外部群组传 `{ "groupType": 0 }`。保存 `groupId`。

`agcctl test update-version` 绑定测试版本、软件包、群组和测试时间。`request` 需要 `versionId`、`pkgId`、`groupId`（或 `groupIds`）、`startTime`、`endTime`。时间是毫秒时间戳。

`agcctl test submit-version` 提交测试版本。`request` 只带测试 `versionId`。成功只表示提交被接受。

`agcctl test get-version` 查询测试状态。`request.versionId` 是测试 `versionId`。`releaseState` 为 `0` 且 `data.shareLink` 非空时，测试入口可用。

`agcctl test list-testers` 分页取群组成员。`request` 需要 `groupId`、`current`、`pageSize`。成员 ID 在 `data.testerInfo[].id`。

`agcctl test notify` 向这些成员发官方邀请。`request` 需要 `groupId` 和 `testerIds`。这会真正发出邀请。

### 提交审核

`agcctl publish update-package` 把同一次上传的包关联到正式版本。`request` 需要 `fileName`、`objectId` 和 `releasePhase`。`releasePhase` 为 `3` 表示 7 天分阶段，`0` 表示全网。保存正式 `packageId`。

再用 `agcctl package compile-status` 查询正式包，`request.pkgId` 改为正式 `packageId`。

`agcctl publish update-language` 写入 `zh-CN` 的更新说明。`request` 需要 `newFeatures` 和同一个 `releasePhase`。

`agcctl publish submit` 正式提审。先加 `--dry-run`，它只检查请求，不访问华为：

```bash
agcctl publish submit --dry-run --config config.yaml --request-file submit.json
```

确认 `data.request` 之后，去掉 `--dry-run` 才会发送。`request` 需要 `releaseMode` 和 `schedule`。分阶段发布时还要 `phasedReleaseDescription`。未取得发布确认前不要去掉 `--dry-run`。

提审被接受后，用 `agcctl test stop-version` 停止 AppTest。`request.versionId` 是测试 `versionId`。命令会先查询；版本已经停止或不存在时不再发送停止请求。

### 审核、上架和全量

`agcctl publish get-app-info` 查看审核和发布状态。看本次正式版本时，`request.publishVersionId` 传正式 `publishVersionId`。

`agcctl publish cancel-review` 撤销本次正式版本的审核。全网发布处在审核中、预审中、升级审核中、待上架/预约上架或申请下架时才可以撤销。`request` 只带正式 `publishVersionId`。`remoteState.cancelled` 为 `true` 只表示撤销被接受，之后继续用 `publish get-app-info` 查询。

`agcctl publish list-versions` 用 `versionCode` 核对这次正式版本。

`agcctl publish get-package-info` 按正式 `packageId` 对账软件包。`request` 只带 `packageId`。

`agcctl publish promote-full` 把仍在灰度的正式版本申请转全网。`request` 必须包含 `"confirmed": true` 和正式 `publishVersionId`。受理成功不等于已经全量，之后继续用 `publish get-app-info` 查询。未确认前不要执行。

`agcctl test delete-version` 在测试版本停止并保留期满后删除。`request.versionId` 是测试 `versionId`。删除失败不会撤销已经完成的发布。

## 开发

```bash
.venv/bin/pip install -e '.[dev]'
.venv/bin/pytest
```

## 离线包

```bash
.venv/bin/python scripts/build_offline_package.py
```

产物在 `dist/`，包含源码包、`MANIFEST.json` 和整包 SHA-256。目标机器解压后仍按上面的虚拟环境步骤安装。测试应用验收清单见 [docs/agc-test-app-poc.md](docs/agc-test-app-poc.md)。

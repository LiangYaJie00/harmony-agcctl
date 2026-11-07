# PUBLISH 流程 agcctl 命令

按 PUBLISH 节点顺序调用 `agcctl`。每次只执行一条命令。变更命令成功后，把响应里的外部 ID 记下来，再执行下一条。查询命令可以重复执行。创建、提交、撤销审核、停止、删除和转全网在结果未知时不要原样重放。

打 Release 包、上传包管理平台、QA 页面确认、钉钉、develop/master PR 和 tag 不调用 `agcctl`。

未取得正式发布审批前，不要去掉 `publish submit` 的 `--dry-run`，也不要执行 `publish promote-full`。牵手这次本地验证已经真实提审过一次，不要用同一份请求再提审。

本次验证使用的请求文件、外部 ID 和调用限制见 `docs/publish-agcctl-local-guide.md`。

## 公共约定

每条命令都要带同一份配置和一份请求 JSON：

```bash
agcctl <分组> <动作> --config <config.yaml> --request-file <请求.json>
```

请求外层固定为：

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

`appId` 必须与 `config.yaml` 的 `appId` 一致。`operationKey` 必须以 `taskId:` 开头。`toolVersion` 必须等于当前 CLI 版本。下文每个请求都使用这层外层，只写出该命令的 `request` 和要替换的 `operationKey`。

标准输出是一份响应 JSON。退出码 `0` 表示成功。`10` 可重试，`20` 不可重试，`30` 表示变更请求已发出但结果未知，要先查询再决定是否继续，`40` 是本地配置、私钥或请求字段问题，`50` 是工具内部错误。

华为返回 `ret.code` 不为 `0` 时，退出码是 `20`，`error.code` 仍是 `AGC_RET_ERROR`。`error.message` 是官方错误描述；文档要求按 `msg` 排查，或错误码不在表中时，使用华为返回的 `msg`。`error.detail` 是原始 `{code}: {msg}`。

这五个 ID 不能混用：

| 名称 | 来源 | 用途 |
| --- | --- | --- |
| `objectId` | `upload package` | 已上传的 APP Pack。测试选包和正式关联都用它 |
| 测试 `versionId` | `test create-version` | 测试版本 |
| 测试 `pkgId` | `test add-package` 的 `pkgVersion[0]` | 查询测试包解析 |
| 正式 `packageId` | `publish update-package` | 查询正式包解析和对账 |
| 正式 `publishVersionId` | `publish get-app-info` 或 `publish list-versions` | 审核、上架、灰度和转全网 |

软件包使用场景在 `test add-package` 决定。`distributeMode=2` 对应包管理里的「测试和正式上架」。正式提审复用同一次上传的 `fileName` 和 `objectId`。

上架自检 `GET /api/publish/v2/test/app/version/detect-report` 还没有对应命令。

## 1. 准备

检查配置、私钥、时钟、JWT，并用包名核对 App ID。

`operationKey`：`<taskId>:doctor`

```json
"request": {}
```

```bash
agcctl doctor --config <config.yaml> --request-file <doctor.json>
```

退出码 `0` 后继续。

## 2. 创建任务前检查

### 2.1 读取当前正式应用

`operationKey`：`<taskId>:get-app-info`

不传 `publishVersionId` 时返回当前正式版本。

```json
"request": {}
```

```bash
agcctl publish get-app-info --config <config.yaml> --request-file <get-app-info.json>
```

记下 `externalIds.publishVersionId`、`remoteState.releaseState` 和 `remoteState.releasePhase`。这是调用前的在架或草稿版本，不要把它当成这次新包的正式版本。

### 2.2 按版本码对账

`operationKey`：`<taskId>:list-versions`

`versionCode` 使用这次 APP Pack 的版本码。

```json
"request": {
  "versionCode": 103
}
```

```bash
agcctl publish list-versions --config <config.yaml> --request-file <list-versions.json>
```

命令用顶层 `versionCode` 或 `packageList[].versionCode` 匹配。候选只包含正式版本：`releaseType` 为 `1`，或响应里没有 `releaseType`。测试版本 `releaseType=6` 即使版本码相同也不计入。唯一命中时，`externalIds.publishVersionId` 是这次正式版本。多于一条正式记录时，`remoteState.multipleCandidates` 为 `true`，并且不写 `publishVersionId`。`data.versions` 仍包含测试版本和旧的在架版本。

## 3. 上传 AGC

APP Pack 必须放在 `config.yaml` 的 `artifactRoot/<taskId>/` 下。`artifactPath` 是该目录内文件的绝对路径，文件名必须等于 `fileName`。`contentLength` 和 `sha256` 必须与文件本身一致。

`operationKey`：`<taskId>:upload-package`

```json
"request": {
  "artifactPath": "<artifactRoot>/<taskId>/<fileName>",
  "fileName": "<fileName>.app",
  "contentLength": 91881402,
  "sha256": "<64位小写sha256>"
}
```

```bash
agcctl upload package --config <config.yaml> --request-file <upload-package.json>
```

保存 `externalIds.objectId` 和 `externalIds.fileName`。上传地址大约 5 分钟有效；超时后重新执行本命令，并使用新的 `objectId`。

## 4. 工程检测

### 4.1 新建测试版本

`releaseType=6` 和 `testType=3` 来自配置，请求里即使写入也会被覆盖。查询参数会带上 `appId`。`testDesc` 为 1 到 50 个字符。不传 `onshelfSelfDetect` 时默认为 `0`，只提交邀请测试、不做这次上架自检；需要按正式上架标准自检时传 `1`。这个开关只作用于本次新建的测试版本，不会补检已经上传的软件包。同一应用同时只能有一个进行中的自检任务。

`operationKey`：`<taskId>:create-version`

```json
"request": {
  "testDesc": "本地验证邀请测试"
}
```

```bash
agcctl test create-version --config <config.yaml> --request-file <create-version.json>
```

保存 `externalIds.versionId`，记作 `<测试versionId>`。

### 4.2 登记软件包

只传 `fileName` 和 `objectId`。命令会提交 `distributeMode=2`（应用市场），包管理使用场景为「测试和正式上架」。

`operationKey`：`<taskId>:add-package`

```json
"request": {
  "fileName": "<fileName>.app",
  "objectId": "<objectId>"
}
```

```bash
agcctl test add-package --config <config.yaml> --request-file <add-package.json>
```

保存 `externalIds.pkgId`，记作 `<测试pkgId>`。

### 4.3 查询测试包解析

`pkgId` 填 `<测试pkgId>`。

`operationKey`：`<taskId>:compile-status`

```json
"request": {
  "pkgId": "<测试pkgId>"
}
```

```bash
agcctl package compile-status --config <config.yaml> --request-file <compile-status.json>
```

`remoteState.successStatus` 为 `1` 时稍后重复本命令。`0` 才继续。`2` 表示包不可用，停止后续步骤。

## 5. 提交测试版本

### 5.1 查询测试群组

不传 `groupType` 时查询内部群组（`1`）。外部群组传 `0`。

`operationKey`：`<taskId>:list-groups`

```json
"request": {}
```

```bash
agcctl test list-groups --config <config.yaml> --request-file <list-groups.json>
```

从 `data.groups` 保存目标群组的 `groupId`。响应 `remoteState.groupType` 是实际查询的类别。群组名称不能用来判断类别：名为「牵手内测」的群组可以是外部群组（`groupType=0`）。内部群组使用缺省查询。

### 5.2 绑定包、群组和测试时间

`versionId` 是测试版本 ID，`pkgId` 是测试包 ID。`startTime` 和 `endTime` 是毫秒时间戳，结束时间必须晚于开始时间。可以传一个 `groupId`，也可以传 `groupIds` 数组。命令会为每个群组提交 `bind=1`，并固定带上 `displayArea=1`、`needShareLink=1`、`needNotify=0`。

`operationKey`：`<taskId>:update-version`

```json
"request": {
  "versionId": "<测试versionId>",
  "pkgId": "<测试pkgId>",
  "groupId": "<groupId>",
  "startTime": 1762000000000,
  "endTime": 1762600000000
}
```

```bash
agcctl test update-version --config <config.yaml> --request-file <update-version.json>
```

### 5.3 提交测试版本

`operationKey`：`<taskId>:submit-version`

```json
"request": {
  "versionId": "<测试versionId>"
}
```

```bash
agcctl test submit-version --config <config.yaml> --request-file <submit-version.json>
```

成功只表示提交被接受。

### 5.4 查询测试状态和入口

重复执行，直到 `remoteState.releaseState` 为 `0` 且 `data.shareLink` 非空。

`operationKey`：`<taskId>:get-version`

```json
"request": {
  "versionId": "<测试versionId>"
}
```

```bash
agcctl test get-version --config <config.yaml> --request-file <get-version.json>
```

### 5.5 查询群组成员

`groupId` 是群组 ID。成员 ID 在 `data.testerInfo[].id`。`pageInfo.totalPage` 大于 1 时增大 `current` 继续查。

`operationKey`：`<taskId>:list-testers`

```json
"request": {
  "groupId": "<groupId>",
  "current": 1,
  "pageSize": 25
}
```

```bash
agcctl test list-testers --config <config.yaml> --request-file <list-testers.json>
```

### 5.6 发送官方邀请

会向列出的成员真实发送邀请。`releaseState=0` 且 `shareLink` 非空后再执行。`testerIds` 为 1 到 10000 个 `testerInfo[].id`。同一批成员不要重复发送。

`operationKey`：`<taskId>:notify`

```json
"request": {
  "groupId": "<groupId>",
  "testerIds": ["<testerId>"]
}
```

```bash
agcctl test notify --config <config.yaml> --request-file <notify.json>
```

`remoteState.notified` 为 `true` 只表示华为接受了发送。当前 `notifyPath` 是 `/api/publish/v2/test/user/action/notify`。成功后不要改路径再发一次。

## 6. 确认测试通过

QA 在页面确认。这一步不调用 `agcctl`。测试不通过或取消任务时，执行第 11 节的停止；保留期满后再删除。QA 只是暂停测试时不调用停止。

## 7. 提交审核

`releasePhase` 在本阶段的几条命令里保持一致：`0` 是全网，`3` 是 7 天分阶段。下面按 7 天分阶段给出。

### 7.1 提审前读取正式应用

与第 2.1 节相同，再执行一次 `publish get-app-info`。

### 7.2 关联正式软件包

`fileName` 和 `objectId` 使用第 3 节同一次上传的值。

`operationKey`：`<taskId>:update-package`

```json
"request": {
  "fileName": "<fileName>.app",
  "objectId": "<objectId>",
  "releasePhase": 3
}
```

```bash
agcctl publish update-package --config <config.yaml> --request-file <update-package.json>
```

保存 `externalIds.packageId`，记作 `<正式packageId>`。

### 7.3 查询正式包解析

把第 4.3 节请求里的 `pkgId` 换成 `<正式packageId>`，重复 `package compile-status`，直到 `successStatus=0`。官方要求传包后至少等待约 2 分钟再提审。

### 7.4 写入更新说明

`newFeatures` 为 1 到 500 个字符，语言使用配置里的 `defaultLanguage`（默认 `zh-CN`）。

`operationKey`：`<taskId>:update-language`

```json
"request": {
  "newFeatures": "本次版本更新说明",
  "releasePhase": 3
}
```

```bash
agcctl publish update-language --config <config.yaml> --request-file <update-language.json>
```

### 7.5 正式提审

先加 `--dry-run`。它只在本地组装请求，不发给华为。`releaseMode` 为 `PHASED` 时必须带 1 到 500 字的 `phasedReleaseDescription`。`schedule` 为 `IMMEDIATE` 时不能带 `releaseTime`。

`operationKey`：`<taskId>:submit`

```json
"request": {
  "releaseMode": "PHASED",
  "schedule": "IMMEDIATE",
  "phasedReleaseDescription": "7天分阶段发布说明"
}
```

```bash
agcctl publish submit --dry-run --config <config.yaml> --request-file <submit.json>
```

`remoteState.dryRun` 为 `true` 时，检查 `data.request`：`releaseType` 为 `1`，`releasePhase` 为 `3`，并且没有 `releaseTime`。

全网发布改成：

```json
"request": {
  "releaseMode": "FULL",
  "schedule": "IMMEDIATE"
}
```

全网请求不能带 `phasedReleaseDescription`。预约发布把 `schedule` 改为 `SCHEDULED`，并增加带时区且晚于当前时间的 `releaseTime`，例如 `2026-11-10T10:00:00+08:00`。`remark` 可选；填写时为 10 到 300 个字符。

审批通过后，去掉 `--dry-run` 执行一次：

```bash
agcctl publish submit --config <config.yaml> --request-file <submit.json>
```

`remoteState.submitted` 为 `true` 只表示提审被接受。

## 8. 提交审核后停止测试版本

提审被接受后停止 AppTest。命令会先按测试 `versionId` 查询。`releaseState` 为 `2` 或 `10` 时视为已停止，不再发送停止请求。查询返回 HTTP 404 时也不再发送。版本已经删除时，华为可能返回 HTTP 200 和业务错误，命令会失败，停止请求不会再次发出。

`operationKey`：`<taskId>:stop-version`

```json
"request": {
  "versionId": "<测试versionId>"
}
```

```bash
agcctl test stop-version --config <config.yaml> --request-file <stop-version.json>
```

正在测试（`releaseState=0`）的版本要先停止，才能删除。停止后测试版本完全下架，不能再编辑。

## 9. 审核、上架或灰度

### 9.1 按正式版本查询

`publishVersionId` 使用本次正式版本，不用测试 `versionId`。

`operationKey`：`<taskId>:get-app-info`

```json
"request": {
  "publishVersionId": "<publishVersionId>"
}
```

```bash
agcctl publish get-app-info --config <config.yaml> --request-file <get-app-info.json>
```

不传 `publishVersionId` 时返回当前正式版本，通常是已上架版本，不是刚提审的版本。要看本次提审，把 `publishVersionId` 传成这次正式版本。

看 `remoteState.releaseState` 和 `remoteState.releasePhase`。正式版本里，`0` 是已上架，`4` 是审核中，`5` 是升级审核中，`7` 是准备提交，`11` 是撤销上架，`12` 是预审中。灰度进行中时，`data.phasePercent` 按 `currentPhaseDay` 第 1 到 7 天对应 1%、2%、5%、10%、20%、50%、100%。

### 9.2 撤销审核

全网发布处于审核中（`4`）、预审中（`12`）、升级审核中（`5`）、待上架/预约上架或申请下架时可以撤销。待上架和申请下架的数字编码没有写在这里。先用 `publish get-app-info` 核对本次正式版本，再执行本命令。命令本身不先查询。

`operationKey`：`<taskId>:cancel-review`

请求只带正式 `publishVersionId`，不要传测试 `versionId` 或 `packageId`。

```json
"request": {
  "publishVersionId": "<publishVersionId>"
}
```

```bash
agcctl publish cancel-review --config <config.yaml> --request-file <cancel-review.json>
```

`remoteState.cancelled` 为 `true` 只表示撤销被接受。随后重复第 9.1 节确认状态。退出码 `30` 时不要用同一份请求再发一次。

### 9.3 用版本码核对本次版本

与第 2.2 节相同。`versionCode` 使用这次包的版本码。唯一命中时使用 `externalIds.publishVersionId`，并在 `data.versions` 里确认该记录的 `releaseType` 为 `1`、`releaseState` 属于这次提交。

### 9.4 对账正式包

结果未知或需要核对正式包时执行。请求走 `GET /api/publish/v2/app-package-info`，`appId` 放请求头，查询参数只有 `packageId`。响应里的 `packageInfo` 含 `fileName`、`versionName`、`versionCode` 和 `buildVersion`。

`operationKey`：`<taskId>:get-package-info`

```json
"request": {
  "packageId": "<正式packageId>"
}
```

```bash
agcctl publish get-package-info --config <config.yaml> --request-file <get-package-info.json>
```

## 10. 确认全量

仍在灰度、并且发布负责人确认转全网时才执行。`confirmed` 必须是布尔值 `true`。只传正式 `publishVersionId`，不要传 `versionId` 或 `packageId`。

`operationKey`：`<taskId>:promote-full`

```json
"request": {
  "publishVersionId": "<publishVersionId>",
  "confirmed": true
}
```

```bash
agcctl publish promote-full --config <config.yaml> --request-file <promote-full.json>
```

`remoteState.accepted` 为 `true` 只表示受理。随后重复第 9.1 节，确认目标版本已经全量。

## 11. 停止后清理

测试版本停止并保留期满后删除。命令会先查询。查询返回 HTTP 404 时不再发送删除请求。版本已经删除时，华为可能返回 HTTP 200 和业务错误，此时删除请求不会发出，命令失败。已经删除的版本不要再执行 `test stop-version` 或 `test delete-version`。

`operationKey`：`<taskId>:delete-version`

```json
"request": {
  "versionId": "<测试versionId>"
}
```

```bash
agcctl test delete-version --config <config.yaml> --request-file <delete-version.json>
```

删除请求里的 `versionId` 必须是本次已停止的测试版本。旧请求文件里可能还留着更早的版本 ID，不要直接拿来删除。

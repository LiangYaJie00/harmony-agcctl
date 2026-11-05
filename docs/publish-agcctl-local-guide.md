# 牵手本地验证：agcctl 调用引导

本文记录一次在正式应用上实际调用过的命令。通用字段说明仍以 `docs/publish-agcctl-commands.md` 为准，接口对照以 `docs/publish-agcctl-verification.md` 为准。

文中的 App ID、上传对象、版本 ID、软件包 ID 和测试群组 ID 不写具体值。这些值留在本机的 `config.yaml` 和请求文件里，不放进仓库。

在仓库根目录执行。配置使用本机的 `config.yaml`，`appId` 与其中的应用一致，包名为 `com.qianshou.billionclub`。请求文件放在本机配置目录，下文用 `<请求目录>` 表示。每次只执行一条。

```bash
.venv/bin/agcctl <分组> <动作> --config config.yaml --request-file <请求目录>/<请求文件>
```

`doctor` 的 `taskId` 是 `local-doctor`，其余命令的 `taskId` 是 `local-verify`。`operationKey` 以对应的 `taskId:` 开头，`toolVersion` 为 `0.1.0`。

查询命令可以重复执行。`publish submit` 这次已经真实发送，不要再用 `submit.json` 提交一次。`publish promote-full` 这次没有执行。

## 外部 ID

这五个 ID 分开使用。下表只说明各自的角色，具体取值以当时命令返回并记在本机请求文件中的结果为准。

| 名称 | 记法 | 用途 |
| --- | --- | --- |
| `objectId` | `<objectId>` | 使用场景为「测试和正式上架」的上传对象。测试选包和正式关联都用它 |
| 测试 `versionId` | `<testVersionId>` | 本次测试版本。提审接受后已停止，`releaseState=10` |
| 软件包 `packageId` | `<packageId>` | 这次测试 `pkgId` 和正式 `packageId` 是同一个。`compile-status` 和 `get-package-info` 都用它 |
| 正式 `publishVersionId` | `<publishVersionId>` | 本次提审的正式版本，`releaseType=1`，`releasePhase=3`，提审后为升级审核中（`releaseState=5`） |
| 在架正式版本 | `<liveVersionId>` | 提审时仍在架的上一正式版本，`releaseState=0`。它不是这次提审 |

更早一次上传的对象使用场景是「仅测试」，不能再拿来做正式关联。已经删除的测试版本不要再停止或删除。

内部测试群组的 `groupType=1`。名称带「内测」的另一个群组属于外部群组，`groupId` 以 `test list-groups` 的返回为准。

## 命令

### doctor

请求文件 `doctor-request.json`，`operationKey` 为 `local-doctor:check`，`request` 为 `{}`。

```bash
.venv/bin/agcctl doctor --config config.yaml --request-file <请求目录>/doctor-request.json
```

退出码 `0` 后再继续。它会读取应用信息并按包名核对 App ID。

### publish get-app-info

请求文件 `get-app-info.json`，`operationKey` 为 `local-verify:get-app-info`。当前 `request` 是 `{}`，返回的是当前正式版本，这次是仍在架的上一版本。

要看本次提审，把 `request` 改成：

```json
"request": {
  "publishVersionId": "<publishVersionId>"
}
```

```bash
.venv/bin/agcctl publish get-app-info --config config.yaml --request-file <请求目录>/get-app-info.json
```

接口是 `GET /api/publish/v3/app-info`。看 `remoteState.releaseState` 和 `remoteState.releasePhase`。正式版本 `0` 为已上架，`5` 为升级审核中，`7` 为准备提交。

### publish list-versions

请求文件 `list-versions.json`，`operationKey` 为 `local-verify:list-versions`。`request.versionCode` 使用本次提审的版本码。

```bash
.venv/bin/agcctl publish list-versions --config config.yaml --request-file <请求目录>/list-versions.json
```

接口是 `POST /api/publish/v3/version/brief-info/list`，请求体里的 `appId` 由命令填入。命令用顶层 `versionCode` 或 `packageList[].versionCode` 匹配，并且只把 `releaseType=1` 计入候选。按这次列表，本次版本码应只命中正式版本，`externalIds.publishVersionId` 为 `<publishVersionId>`，`candidateCount` 为 `1`。相同版本码的测试版本不计入。`data.versions` 仍包含全部记录：

- `<publishVersionId>`：正式版本，`releaseType=1`，`releaseState=5`，`releasePhase=3`
- `<testVersionId>`：测试版本，`releaseType=6`，停止后 `releaseState=10`，同一个 `packageId`
- `<liveVersionId>`：在架的上一正式版本

### upload package

请求文件 `upload-package.json`，`operationKey` 为 `local-verify:upload-package`。包文件是 `artifactRoot/local-verify/qianshou-harmony-release-signed.app`。`contentLength` 和 `sha256` 必须与该文件一致。

```bash
.venv/bin/agcctl upload package --config config.yaml --request-file <请求目录>/upload-package.json
```

先取 `GET /api/publish/v2/upload-url/for-obs`，再把文件 `PUT` 到返回的临时地址。保存新的 `externalIds.objectId`。这次已经有可复用的 `<objectId>`，不要为了核对再上传一次。

### test create-version

请求文件 `create-version.json`，`operationKey` 为 `local-verify:create-version`。`request` 只有 `testDesc`：`本地验证邀请测试`。

```bash
.venv/bin/agcctl test create-version --config config.yaml --request-file <请求目录>/create-version.json
```

实际发送 `POST /api/publish/v2/test/app/version?appId=`。`releaseType=6`、`testType=3` 来自配置。未写 `onshelfSelfDetect` 时命令发送 `1`。这个开关只作用于新建的测试版本。保存 `externalIds.versionId`，记为 `<testVersionId>`。

### test add-package

请求文件 `add-package.json`，`operationKey` 为 `local-verify:add-package`。`request` 只有 `fileName` 和上面的 `<objectId>`。

```bash
.venv/bin/agcctl test add-package --config config.yaml --request-file <请求目录>/add-package.json
```

实际发送 `POST /api/publish/v2/test/version/pkg?appId=`，请求体为 `distributeMode=2` 和 `file`。`2` 对应包管理「测试和正式上架」。保存 `externalIds.pkgId`。这次它和后来的正式 `<packageId>` 相同。

### package compile-status

请求文件 `compile-status.json`，`operationKey` 为 `local-verify:compile-status`。`request.pkgId` 为 `<packageId>`。

```bash
.venv/bin/agcctl package compile-status --config config.yaml --request-file <请求目录>/compile-status.json
```

实际查询参数是 `appId` 和 `pkgIds`。`remoteState.successStatus` 为 `0` 才继续，`1` 时稍后重试，`2` 表示包不可用。测试包和正式包都用这条命令，只更换 `pkgId`。

### test list-groups

请求文件 `list-groups.json`，`operationKey` 为 `local-verify:list-groups`，`request` 为 `{}`。

```bash
.venv/bin/agcctl test list-groups --config config.yaml --request-file <请求目录>/list-groups.json
```

实际发送 `GET /api/app-test/v1/test-group/list?appId=&groupType=1`。`groupType=1` 返回内部群组。需要外部群组时，`request` 改为 `{ "groupType": 0 }`。

### test update-version

请求文件 `update-version.json`，`operationKey` 为 `local-verify:update-version`。`versionId` 为 `<testVersionId>`，`pkgId` 为 `<packageId>`，`groupId` 为内部群组。测试起止时间写在请求文件里。

```bash
.venv/bin/agcctl test update-version --config config.yaml --request-file <请求目录>/update-version.json
```

实际发送 `PUT /api/publish/v2/test/app/version?appId=`。群组放在 `openTestInfo.testTaskInfo.groupInfos`，每项 `bind=1`，并带 `needShareLink=1`、`displayArea=1`、`needNotify=0`。

### test submit-version

请求文件 `submit-version.json`，`operationKey` 为 `local-verify:submit-version`。`request.versionId` 为 `<testVersionId>`。

```bash
.venv/bin/agcctl test submit-version --config config.yaml --request-file <请求目录>/submit-version.json
```

实际发送 `POST /api/publish/v2/test/app/version/submit?appId=`，请求体只有 `versionId`。成功只表示测试版本提交被接受。

### test get-version

请求文件 `get-version.json`，`operationKey` 为 `local-verify:get-version`。`request.versionId` 为 `<testVersionId>`。

```bash
.venv/bin/agcctl test get-version --config config.yaml --request-file <请求目录>/get-version.json
```

实际发送 `GET /api/publish/v3/app-info?appId=&releaseType=6&versionId=`。可测条件是 `remoteState.releaseState=0` 且 `data.shareLink` 非空。

### test list-testers

请求文件 `list-testers.json`，`operationKey` 为 `local-verify:test-user`。`groupId` 为内部群组，`current` 为 `1`，`pageSize` 为 `25`。

```bash
.venv/bin/agcctl test list-testers --config config.yaml --request-file <请求目录>/list-testers.json
```

`groupId` 放在请求头。查询参数是 `appId`、`current` 和 `pageSize`。成员 ID 在 `data.testerInfo[].id`。

### test notify

请求文件 `notify.json`，`operationKey` 为 `local-verify:notify`。`groupId` 为内部群组，`testerIds` 使用 `list-testers` 返回的成员 ID。

```bash
.venv/bin/agcctl test notify --config config.yaml --request-file <请求目录>/notify.json
```

实际发送 `POST /api/publish/v2/test/user/action/notify`。`remoteState.notified=true` 只表示华为接受了发送。不要为了换路径再发一次。

### publish update-package

请求文件 `update-package.json`，`operationKey` 为 `local-verify:update-package`。`fileName` 和 `<objectId>` 与上传、测试选包相同，`releasePhase` 为 `3`。

```bash
.venv/bin/agcctl publish update-package --config config.yaml --request-file <请求目录>/update-package.json
```

实际发送 `PUT /api/publish/v3/app-package-info?appId=&releaseType=1&releasePhase=3`。请求体只有 `fileName` 和 `objectId`。保存 `externalIds.packageId`，记为 `<packageId>`。`releasePhase=3` 表示 7 天分阶段，`0` 表示全网。

### publish update-language

请求文件 `update-language.json`，`operationKey` 为 `local-verify:update-language`。`newFeatures` 为 `本地验证正式发布更新说明`，`releasePhase` 为 `3`。

```bash
.venv/bin/agcctl publish update-language --config config.yaml --request-file <请求目录>/update-language.json
```

实际发送 `PUT /api/publish/v3/app-language-info?appId=&releaseType=1&releasePhase=3`。语言取配置里的 `zh-CN`。这是版本更新说明，不是分阶段发布说明。

### publish submit

请求文件 `submit.json`，`operationKey` 为 `local-verify:submit`。`releaseMode` 为 `PHASED`，`schedule` 为 `IMMEDIATE`，`phasedReleaseDescription` 为 `本地验证7天分阶段发布`。

先看本地组装结果：

```bash
.venv/bin/agcctl publish submit --dry-run --config config.yaml --request-file <请求目录>/submit.json
```

`remoteState.dryRun=true` 时，`data.request` 应为 `releaseType=1`、`releasePhase=3`、上述分阶段说明，并且没有 `releaseTime`。`--dry-run` 不访问华为。

真实提审已经执行过，命令是去掉 `--dry-run` 的同一条。它发送 `POST /api/publish/v3/app-submit?appId=`。当时 `remoteState.submitted=true`，只表示提审被接受。控制台里的「分阶段发布说明」就是 `phasedReleaseDescription`。

### test stop-version

请求文件 `stop-version.json`，`operationKey` 为 `local-verify:stop-version`。`versionId` 为 `<testVersionId>`。

```bash
.venv/bin/agcctl test stop-version --config config.yaml --request-file <请求目录>/stop-version.json
```

命令先查询测试版本。这次查询后发送了 `POST /api/publish/v2/test/version/stop?appId=`，`remoteState.stopped=true`。该版本现在是 `releaseState=10`，再执行会视为已停止，不再发送停止请求。

### publish get-package-info

请求文件 `get-package-info.json`，`operationKey` 为 `local-verify:get-package-info`。`packageId` 为 `<packageId>`。

```bash
.venv/bin/agcctl publish get-package-info --config config.yaml --request-file <请求目录>/get-package-info.json
```

实际发送 `GET /api/publish/v2/app-package-info?packageId=`。`appId` 在请求头，不在查询参数。这是只读查询。成功时看 `data.packageInfo` 的 `fileName`、`versionName`、`versionCode` 和 `buildVersion`。这次应对上 `qianshou-harmony-release-signed.app` 和本次提审的版本号、版本码。

## 这次不要执行

- `publish submit` 不带 `--dry-run`。同一次发布已经提审。
- `publish promote-full`。正式版本仍在升级审核，命令这次没有验证。
- `test delete-version`。`delete-version.json` 里的 `versionId` 不是本次已停止的 `<testVersionId>`。保留期满前不要删除。已经删除的版本再查询时，华为可能返回 HTTP 200 业务错误，停止和删除都不会把这种情况当成“已经不存在”。

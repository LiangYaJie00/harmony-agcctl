# PUBLISH 场景 agcctl 验证对照

`doctor` 通过之后，按下面的主线逐条验证命令。每次只跑一个 `agcctl` 操作，把响应里的外部 ID 记下来再跑下一条。查询命令可以重复执行。创建、提交、停止、删除和转全网在结果未知时不要原样重放。

当前本机 `config.yaml` 指向正式应用「牵手」。这次本地验证已经完成上传、测试版本、正式关联、语言更新、真实提审和提审后停止。`publish promote-full` 没有执行。不要用同一份请求再次 `publish submit`，也不要转全网。请求文件和外部 ID 见 `docs/publish-agcctl-local-guide.md`。测试应用验收项见 `docs/agc-test-app-poc.md`。

打 Release 包、上传包管理平台、QA 页面确认、钉钉、develop/master PR 和 tag 都不调用 `agcctl`。

## 节点和命令

| 节点 | 步骤 | agcctl | 官方 API |
|---|---|---|---|
| 准备 | 配置、私钥、时钟、权限；用包名核对 App ID | `doctor` | `GET /api/publish/v3/app-info`；`GET /api/publish/v2/appid-list` |
| 创建任务前检查 | 读取当前应用和版本 | `publish get-app-info` | `GET /api/publish/v3/app-info` |
| 创建任务前检查 | 按 `versionCode` 对账。匹配顶层或 `packageList[].versionCode`，只计 `releaseType=1` 的正式版本 | `publish list-versions` | `POST /api/publish/v3/version/brief-info/list` |
| 上传 AGC | 取上传地址并上传 APP Pack，保存 `objectId` | `upload package` | `GET /api/publish/v2/upload-url/for-obs`，再 `PUT` 返回的临时地址 |
| 工程检测 | 新建 AppTest 草稿，保存 `versionId`。缺省 `onshelfSelfDetect=1` | `test create-version` | `POST /api/publish/v2/test/app/version` |
| 工程检测 | 用 `fileName + objectId` 选包，`distributeMode=2`（测试和正式上架），保存 `pkgVersion[0]` 为测试 `pkgId` | `test add-package` | `POST /api/publish/v2/test/version/pkg` |
| 工程检测 | 查测试包解析。`successStatus`：`0` 通过，`1` 处理中，`2` 不可用 | `package compile-status` | `GET /api/publish/v3/package/compile/status` |
| 提交测试版本 | 查 QA 群组。`groupType` 缺省为 `1`（内部群组），`0` 为外部群组 | `test list-groups` | `GET /api/app-test/v1/test-group/list?groupType=` |
| 提交测试版本 | 绑定 `versionId`、`pkgId` 和群组。`groupInfos.bind=1`，并带测试起止时间、`displayArea=1`、`needShareLink=1`、`needNotify=0` | `test update-version` | `PUT /api/publish/v2/test/app/version` |
| 提交测试版本 | 提交测试版本 | `test submit-version` | `POST /api/publish/v2/test/app/version/submit` |
| 提交测试版本 | 查 `releaseState` 和 `shareLink`。`releaseState=0` 且链接非空才算可测 | `test get-version` | `GET /api/publish/v3/app-info?releaseType=6&versionId=<测试versionId>` |
| 提交测试版本 | 分页取成员 `testerInfo[].id`。`groupId` 放请求头，`current` 和 `pageSize` 放查询参数 | `test list-testers` | `GET /api/app-test/v1/test-user` |
| 提交测试版本 | 发官方邀请。路径使用配置里的 `notifyPath`，当前为 v2 | `test notify` | `POST /api/publish/v2/test/user/action/notify` |
| 确认测试通过 | QA 在页面确认。不调用 `agcctl` | 无 | 无 |
| 提交审核 | 提审前读正式应用资料 | `publish get-app-info` | `GET /api/publish/v3/app-info` |
| 提交审核 | 用同一 `fileName + objectId` 关联正式包，保存正式 `packageId`。查询参数带 `appId`、`releaseType=1` 和请求里的 `releasePhase` | `publish update-package` | `PUT /api/publish/v3/app-package-info` |
| 提交审核 | 查正式包解析，`pkgIds` 传正式 `packageId` | `package compile-status` | `GET /api/publish/v3/package/compile/status` |
| 提交审核 | 写 `zh-CN` 的 `newFeatures`。查询参数带 `appId`、`releaseType=1` 和同一个 `releasePhase` | `publish update-language` | `PUT /api/publish/v3/app-language-info` |
| 提交审核 | 正式提审。可先 `--dry-run`，只校验不发送 | `publish submit` | `POST /api/publish/v3/app-submit` |
| 提交审核后 | 提审被接受后停止 AppTest。先查询；`releaseState` 为 `2` 或 `10`，或 HTTP 404 时不再停止。已删除版本返回 HTTP 200 业务错误时命令失败 | `test stop-version` | 先 `GET /api/publish/v3/app-info`，再 `POST /api/publish/v2/test/version/stop` |
| 审核中 / 上架或灰度 | 按正式 `publishVersionId` 查审核、上架和灰度 | `publish get-app-info` | `GET /api/publish/v3/app-info` |
| 审核中 / 上架或灰度 | 用 `versionCode` 核对本次正式版本。相同版本码的测试版本不计入候选 | `publish list-versions` | `POST /api/publish/v3/version/brief-info/list` |
| 对账正式包 | 结果未知时查正式包信息。`appId` 放请求头，查询参数只有 `packageId` | `publish get-package-info` | `GET /api/publish/v2/app-package-info` |
| 创建 PR、合入、tag | 平台 GitHub 适配器。不调用 `agcctl` | 无 | 无 |
| 确认全量 | 仍在灰度时转全网。请求里 `confirmed` 必须为 `true`，只传正式 `publishVersionId` | `publish promote-full` | `PUT /api/publish/v2/version/phased-release`，Body 为 `versionId + releasePhase=4` |
| 确认全量之后 | 继续查，直到目标版本已全量。受理成功不等于已全量 | `publish get-app-info` | `GET /api/publish/v3/app-info` |
| 停止后清理 | 保留期满后删除。先查询；HTTP 404 时不再删除。已删除版本返回 HTTP 200 业务错误时命令失败 | `test delete-version` | 先 `GET /api/publish/v3/app-info`，再 `DELETE /api/publish/v2/test/app/version` |

测试不通过或取消任务时，也是 `test stop-version`，必要时再 `test delete-version`。QA 只是暂停测试时不调用停止。

## 建议验证顺序

1. `publish get-app-info`、`publish list-versions`：只读，确认正式应用查询。
2. 准备一份放在 `artifactRoot/<taskId>/` 里的 `.app`，再跑 `upload package`，记下 `objectId`。
3. `test list-groups`。不传 `groupType` 时查询内部群组；需要外部群组时在 `request` 里传 `"groupType": 0`。
4. `test create-version`，记下 `versionId`。
5. `test add-package`，记下 `pkgId`。
6. 重复 `package compile-status`，直到 `successStatus=0`。
7. `test update-version`、`test submit-version`。
8. 重复 `test get-version`，看 `releaseState` 和 `shareLink`。
9. `test list-testers`，确认成员 ID 后再决定是否跑 `test notify`。通知会真正发给测试成员。
10. 正式包和提审从 `publish update-package` 开始。`publish submit` 先用 `--dry-run`，确认 `data.request` 后再去掉 `--dry-run` 发送一次。牵手这次 7 天分阶段、立即发布的提审已经发送。不要重复提审，也不要跑 `publish promote-full`。

这五类 ID 不要混用：`objectId`、测试 `versionId`、测试 `pkgId`、正式 `packageId`、正式 `publishVersionId`。

## 和方案的差异

验证时以代码实际发出的请求为准：

- `publish get-package-info` 是 `GET /api/publish/v2/app-package-info`，查询参数只有 `packageId`，`appId` 在请求头。`PUT /api/publish/v3/app-package-info` 只用于 `publish update-package`。
- `publish list-versions` 用顶层 `versionCode` 或 `packageList[].versionCode` 匹配，只把 `releaseType` 为 `1` 或未返回该字段的记录计入候选。测试版本 `releaseType=6` 即使版本码相同也不写入 `publishVersionId`。`data.versions` 仍是完整列表。
- `test add-package` 固定提交 `distributeMode=2`。使用场景为「仅测试」的旧 `objectId` 不能拿来做正式关联。
- `test create-version` 缺省 `onshelfSelfDetect=1`，只影响这次新建的测试版本。
- `test list-groups` 缺省 `groupType=1`。外部群组里也可以出现名称带「内测」的群组。
- `test list-testers` 把 `groupId` 放在请求头，`current` 和 `pageSize` 放在查询参数。
- `test notify` 使用配置中的 `/api/publish/v2/test/user/action/notify`。
- `test stop-version` 和 `test delete-version` 只把 HTTP 404 当成版本已不存在。华为对已删除版本常返回 HTTP 200 业务错误。
- 上架自检 `GET /api/publish/v2/test/app/version/detect-report` 还没有对应命令。
- `publish promote-full` 已实现，这次验证没有调用。

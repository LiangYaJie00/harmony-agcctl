# AGC 测试应用 PoC 清单

未获得正式发布审批前，只对独立测试应用执行。生产应用不调用 `publish submit` 和 `publish promote-full`。

记录每项的请求 ID、外部 ID 和结论。

1. Service Account PS256 鉴权，`doctor` 能完成只读应用查询。
2. `upload package` 取得 `objectId`，响应和日志中没有临时上传 URL。
3. `test add-package` 向华为提交 `distributeMode=2`（应用市场，对应包管理「测试和正式上架」）和 `file.fileName`、`file.objectId`。
4. `package compile-status` 能区分 `successStatus` 0、1、2。查询成功不等于解析通过。
5. 测试群组通过 `groupInfos.bind=1` 绑定，并带 `displayArea=1`、`needShareLink=1`、`needNotify=0`。
6. 测试版本提交后，`test get-version` 能读到 `releaseState` 和 `shareLink`。
7. 确认 `test notify` 的实际路径。默认值只是当前文档路径。
8. 停止和删除会先查询。已停止或不存在时不再发变更请求。
9. 记录 Testing API 与 Publishing API 能否复用同一个 `objectId`。
10. 正式包解析、语言资料、全网和 7 天分阶段参数。
11. `publish submit` 只在测试应用或已审批场景发送。`--dry-run` 不发请求。
12. `pkgVersion[0]`、测试包 ID、正式 `packageId`、正式 `publishVersionId` 不混用。
13. 分享链接可以由测试账号打开，官方通知不会重复发送。
14. 灰度 `currentPhaseDay` 与 1%、2%、5%、10%、20%、50%、100% 的对应关系。
15. `publish promote-full` 只发送正式 `publishVersionId` 和 `releasePhase=4`。受理成功后继续查询，才算全量。
16. 包管理归档、GitHub PR 和 tag 不由 `agcctl` 执行。

变更请求超时的故障注入点：取得 `objectId` 后、PUT 成功后、创建 `versionId` 后、添加 `pkgId` 后、提交测试后、更新正式包后、正式提交发出后。恢复时先查询，不能产生第二个测试版本或第二次正式提审。

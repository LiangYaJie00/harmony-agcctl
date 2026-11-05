# harmony-agcctl

牵手 HarmonyOS 正式发布使用的本地命令行工具，命令名是 `agcctl`。它一次只执行一个华为 API 操作，然后退出。平台服务负责任务状态、审批、轮询、幂等和对账。

`private.json` 不放进本仓库。构建机上的路径是 `/etc/qianshou-harmony/agc/private.json`，`config.yaml` 只记录这个路径。

`private.json` 不放进本仓库。构建机上的路径是 `/etc/qianshou-harmony/agc/private.json`，`config.yaml` 只记录这个路径。

## 开发

```bash
python3.11 -m venv .venv
.venv/bin/pip install -e '.[dev]'
.venv/bin/pytest
```

## doctor

复制 `config.example.yaml` 为机器上的配置，填入真实 `appId`。本机调试时把 `privateKeyFile`、目录和 `releaseUser` 指到当前用户可读写的位置。然后：

```bash
agcctl doctor --config config.yaml --request-file request.json
```

标准输出只有一份响应 JSON。标准错误是一行脱敏日志。退出码 `0` 表示检查通过，`10` 可重试，`20` 不可重试，`40` 是本地配置或私钥问题，`50` 是工具内部错误。

请求至少包含 `schemaVersion`、`taskId`、`operationKey`、`toolVersion`、`appId` 和 `request`。`operationKey` 以 `taskId:` 开头，`toolVersion` 必须等于当前 CLI 版本。

## 命令

除 `doctor` 外，命令都是 `agcctl <分组> <动作> --config <配置> --request-file <请求>`。

- `upload package`：校验任务目录、大小和 SHA-256 后上传，返回 `objectId`。
- `package compile-status`：返回 `successStatus`。0 是通过，1 是处理中，2 是不可用。
- `test create-version|add-package|update-version|submit-version|list-groups|list-testers|notify|get-version|stop-version|delete-version`
- `publish get-app-info|update-package|update-language|submit|list-versions|get-package-info|promote-full`

`publish submit --dry-run` 只校验发布参数，不发送变更请求。`publish promote-full` 只在请求里 `confirmed` 为 true 时发送正式 `publishVersionId` 和 `releasePhase=4`。

停止和删除会先查询。目标已经停止或不存在时不再发变更请求。变更请求发出后超时返回退出码 `30`，命令本身不会重放。

平台调用时使用参数数组和版本目录里的绝对路径，不要经过 shell。先写 `operationKey`，再保存响应里的外部 ID，然后删除权限为 `0600` 的请求文件。

## 离线包

```bash
.venv/bin/python scripts/build_offline_package.py
```

产物在 `dist/`，包含 `MANIFEST.json` 和整包 SHA-256。测试应用验收清单见 `docs/agc-test-app-poc.md`。

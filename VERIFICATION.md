# 验证记录（2026-11-15，锁/生命周期复验后）

只修改 E:\pro\speedapp。工作树保留未提交改动，未提交、推送、部署公网，未改 golang_game 或 cocos_game。初始工作树干净，未发现 AGENTS.md/.agents 项目约定或本项目并行写入冲突。

## 本轮修正

1. **构建锁和恢复**：以独立工作进程持有内核独占锁，永久保留 0 字节 build.lock，不按文件存在或年龄判断忙，也不删除锁载体。原子 build-owner.json 记录 PID、进程出生身份、随机所有权标识与完成状态。服务退出/请求断开通过控制管道 EOF 取消工作进程；Windows 在任何子进程启动前将工作进程纳入独立 Job Object，按 job 成员及进程句柄回收；Linux 使用 flock、/proc 所有权标识和 pidfd 定向回收。对仍活着或无法核实归属的所有者保守返回 429。Kotlin 编译改为 in-process，避免外部共享编译 daemon 逸出本次构建范围。
2. **游戏生命周期**：只在新游戏会话启动检查版本，普通返回前台不触发启动检查或重载。会话固定完整资源 generation 和 JS 配置 version，配置重建复用会话；后台验证成功只准备下一次新会话资源。首次校验完成前显示原生启动进度，之后只加载一次游戏页面。
3. **客户端 DNS 边界**：原 HttpURLConnection 的预检查与连接重解析存在间隙；改为 OkHttp DNS 回调把全部已检查 InetAddress 直接交给连接层。IP 字面量在连接前单独验证，禁用代理，逐跳校验 origin，保留原域名 Host/SNI 和默认 TLS 校验。此策略只覆盖版本/ZIP 更新请求，不代理游戏/WebView 的任意业务请求。

## 当前验收结果

| 检查 | 结果 | 实际范围/证据 |
| --- | --- | --- |
| Go 全包单元/集成 | 通过 | 25 个顶层测试及子用例（含子进程载荷入口），真实回环 HTTP 下载与 ZIP/SSRF/请求限制检查；[go-test.log](.cache/verification/go-test.log) |
| 真实 Windows 进程锁测试 | 通过 | 普通退出、取消、超时、八路并发唯一所有者、工作进程崩溃、服务崩溃后控制管道 EOF/重启、PID 出生身份变化、保护另一有效构建、旧格式/坏记录保守拒绝；[buildprocess-check.log](.cache/verification/buildprocess-check.log) |
| Go vet / Node 语法 | 通过 | go vet ./...、node --check index.js |
| Windows / Linux 编译 | 通过 | Windows 服务原生编译；Linux amd64 服务及工作进程测试二进制交叉编译。Linux 运行时没有实际执行 |
| 真实 HTTP 取消及后续恢复 | 通过 | 确认真实 Gradle 工作目录已出现、所有者仍 active 后断开请求；完成清理且取消作业未产出 APK，随后两个真实请求构建成功。清理响应尾部允许按 Retry-After=10 有界重试一次，持续 429 判失败；[service-result.json](.cache/verification/service-result.json) |
| URL / 空 URL APK 构建 | 均通过 | 最终服务真实 POST /packaging 均返回 200；Gradle release、R8、apksigner v2 签名校验执行；内置模式 HTML/嵌套 JS/JSON/WASM 与 ZIP 逐文件字节核对一致；URL 模式无残留 html |
| APK 鉴权下载 | 通过 | 未认证 401、认证 200，HTTP 下载 SHA-256 等于磁盘 APK |
| 更新核心 HTTP/ZIP/DNS/会话 | 通过（JVM，25 项） | 生产 BundleUpdater + ContentSession：同版跳过、两次正常升级、各类坏包/受限地址拒绝、旧版与内置回退、会话固定、下次会话选新版；假的域名通过受控 resolver 解析到真实回环服务，证明实际连接只使用已检查的结果且保留 Host，混合受限 DNS 答案在连接前拒绝；[updater-check.log](.cache/verification/updater-check.log) |
| Android Activity 生命周期 | 通过（Robolectric，2 项） | 运行生产 X5WebActivity 和真实回环版本/ZIP 更新：启动请求延迟期间回前台，之后收到并验证新版仍不重载对局；配置重建保留旧版本，新会话选择新版。WebView 为 Shadow 计数，明确不是设备/渲染通过；[lifecycle-result.xml](.cache/verification/lifecycle-result.xml)、[lifecycle-check.log](.cache/verification/lifecycle-check.log) |
| Android 设备 / 模拟器 | 未运行 | 最新 adb devices -l 仍为空，无法验收真实 X5/系统 WebView、WebGL、完整 Cocos 游戏、设备热更新 UI；[设备列表](.cache/verification/adb-devices.log) |
| Linux 工作进程运行时 | 未运行 | 无可用 Docker 引擎，WSL 子系统也未安装。仅交叉编译，不把 Windows 结果称作 Linux 崩溃恢复通过 |
| Docker 镜像 / 容器健康 | 未运行（阻塞） | Docker CLI、标准 Desktop 路径和 docker_engine 管道均未发现；未安装 Desktop 或使用远端引擎。Dockerfile/Compose/HEALTHCHECK 已提供，不能视为镜像构建或健康验收成功 |
| MinIO 上传 | 未运行（可选） | 默认关闭，未连接历史外部服务器；启用需实际凭据、已存在 bucket 与正确 PublicBaseURL |

## 真实产物

以下是本轮最终服务及客户端的真实 APK，版本 1.0.1，使用本地测试证书及临时回环地址。fixture 是嵌套静态资源，不是完整 Cocos 游戏；服务脚本结束后 URL 模式的临时网页/版本服务不再运行。内置模式资源实际存在 APK 中，设备显示未测。

| 模式 | APK | 字节数 | SHA-256 |
| --- | --- | --- | --- |
| url | [04a131c202a38805aaf76450c4748b6b.apk](E:/pro/speedapp/.cache/verification/service-data/artifacts/04a131c202a38805aaf76450c4748b6b.apk) | 2533298 | `95a2ae50a00f1c6f4c19f52b0fa4838571a8cebe3f1342ed274bcbbf0e9ee210` |
| local | [4259f4ffd48d40d7ef48f291962d39c7.apk](E:/pro/speedapp/.cache/verification/service-data/artifacts/4259f4ffd48d40d7ef48f291962d39c7.apk) | 2535084 | `70d9d2b30594cc95d1ce319d976c4001e0758d141c7650bda1cf88df0243079b` |

- [Windows Go 服务](.cache/verification/packager.exe)
- [Linux amd64 Go 服务](.cache/verification/packager-linux)
- [Linux 工作进程测试二进制，仅编译](.cache/verification/buildprocess-linux.test)
- [完整服务/构建日志](.cache/verification/service-check.log)

本轮实际取消的作业：`136fbbc65f54e4b0b3f839ac41c05a7d`。其后构建日志与所有真实 APK 的日志均在 `.cache/verification/service-data/jobs/<id>/build.log`。

## 恢复与部署边界

普通取消、超时、服务/工作进程崩溃在本机 Windows 已用实际进程验证，不需要删除锁。旧版本非空 O_EXCL 锁、无法解析或无法确认归属的元记录保持拒绝覆盖；迁移时必须先停旧服务并确认旧构建结束，再归档旧记录。不要删除正在使用的 build.lock，也不要以修改过期时间抢占有效构建。

Linux 需要 5.3+、允许 pidfd 系统调用、对服务自身同 UID 进程的 /proc 可见性；不能确认或无权限时保守拒绝回收。该实现不是跨主机分布式队列，不支持不同 PID namespace 的并发实例共用未完成构建目录。单实例 Docker 重启需确认旧容器已停止；真实 Linux/Docker 运行与恢复仍待在可用环境验证，未通过放宽 seccomp、文件权限或锁控制来假装通过。

每个新游戏会话会检查版本，更新下载完成后在下一次新会话生效。继续正在进行的对局不会为了应用新版而重载页面；配置重建保持同一资源版本。Go 下载与 Android 版本/ZIP 下载均校验地址，但游戏/WebView 自身网络不属于该下载白名单的保护范围。

没有待审批请求或未解决的自动审批拒绝。已停止本次新启动且无设备的 ADB 测试服务；HTTP fixture 与 Go 测试服务均在脚本结束时关闭。

## 改动文件

- Go：cmd/packager/main.go；internal/buildprocess/{worker,platform_windows,platform_linux,platform_other}.go 与真实进程测试；internal/packager/runner.go、main_test.go、runner_test.go；internal/config、handler、content、keystore、storage 的实现/测试；go.mod。
- Android：X5WebActivity.kt、ContentManager.kt、ContentSession.java、BundleUpdater.java、app/build.gradle.kts、gradle.properties、AndroidManifest.xml、strings.xml；新增 StartupLifecycleTest.java。
- 构建/部署：index.js、package.json/lock、Dockerfile、compose.yaml、etc/packager.yaml、.dockerignore、.gitignore。
- 说明/工具：README.md、本记录、tools/BundleUpdaterCheck.java、check-updater.ps1、check-lifecycle.ps1、verify_service.py。

## 环境与复现

Go 1.27.3、Node 24.22.1；项目缓存中的 Microsoft JDK 17.0.21.0.2、Android SDK android-33/build-tools 33.0.2/platform-tools；Gradle 8.4、AGP 8.1.0、Kotlin 1.8.0 沿用。新增 OkHttp 4.11；Robolectric 与 MockWebServer 只在测试使用。SDK/JDK 来源与散列见 [toolchain-manifest.json](.cache/verification/toolchain-manifest.json)，未改变系统安装或系统环境配置。

```powershell
$env:JAVA_HOME = "$PWD\.cache\toolchains\jdk17\jdk-17.0.21.0.2+1"
$env:PATH = "$env:JAVA_HOME\bin;$env:PATH"
$env:ANDROID_SDK_ROOT = "$PWD\.cache\toolchains\sdk"
$env:ANDROID_USER_HOME = "$PWD\.cache\toolchains\android-home"
$env:GRADLE_USER_HOME = "$PWD\.cache\gradle"
go test -count=1 -v ./...
go vet ./...
./tools/check-updater.ps1 -JavaHome $env:JAVA_HOME
./tools/check-lifecycle.ps1 -JavaHome $env:JAVA_HOME
python tools/verify_service.py --build
```

版本接口 JSON、四字段调用、生产密钥与 Docker 持久化配置见 [README.md](README.md)。上线前还需真实 CDN/业务版本服务、生产证书、完整 Cocos ZIP，以及 Docker 与设备实测。

**后续健康检查端口修复复验（2026-11-15）**：按用户授权修复 `-healthcheck` 固定请求 10010 的问题。现在先用相同 `-f` 与环境变量加载配置，再检查实际 Host/Port；通配地址转换为对应回环地址，保留 3 秒超时，不使用代理或跟随重定向，不初始化签名、SDK 或存储。

本轮重新运行 Go 全包测试，28 个顶层测试通过（包括 3 个新增健康检查测试及子用例）；Go vet、Windows 服务编译、Linux amd64 交叉编译和 git diff --check 通过。用本轮新编译的 Windows 服务真实验证默认配置/默认端口 10010、自定义 `-f` 端口 64855、`PACK_HOST` 覆盖与停止后退出 1；自定义实例停止时 10010 的另一实例仍正常，未误判成功。签名缺失时 `/ready` 为 503，但存活检查仍成功。本轮源码改动限于 main.go、两个新增健康检查 Go 文件及 README/本记录的补充，保留原有实现与其他改动。详见 [修复报告](.cache/healthcheck-fix-20261115/REPORT.md)、[真实服务结果](.cache/healthcheck-fix-20261115/real-service-result.json)、[Go 测试](.cache/healthcheck-fix-20261115/go-test.log)。

本轮额外 IPv6 真服务检查受到 Windows `WSAEACCES 10013` 回环连接权限拒绝，原始 TCP、Python HTTP 与 Go 检查均未成功；失败及诊断日志保留，未修改系统网络权限，IPv6 运行仍未验收。IPv6/通配地址的 URL 单元检查已通过。Docker 引擎仍缺失，Linux 仅交叉编译；本轮未重跑 Android/JVM/APK 构建，也未进行完整 Cocos 真机、真实版本 API/CDN、生产签名或 MinIO 验收。前文 APK 与 JVM 结果属于先前轮次的证据。

# packapp / speedapp Android 打包服务

这是独立的 Go HTTP 服务及 Android 模板工程。只在本项目构建，不依赖 golang_game 或 cocos_game。Node 仅以参数数组调用原 Gradle Wrapper 和 apksigner，不再反编译/重打包 APK，也不需要 npm 依赖或 apktool。

Go 服务位于 `app/packager`。入口为 `packager.go`，配置在 `etc/packager-etc.yaml`，路由由 `internal/router/v1` 注册。依赖在 `internal/svc` 组装，打包模块在 `internal/module/v1/packaging` 中维护 `handler.go`、`logic.go` 和 `vo.go`。

## 请求与产物

dodo 调用 `POST /api/packager/v1/packaging/build`，使用 `application/json` 和 `X-Pack-Token`（或 `Authorization: Bearer ...`）。JSON 必须且只能有以下四个字段，保持 `dominList` 的拼写。

```json
{"url":"","dominList":["https://api.example.com"],"version":"1.0.1","last_version_url":"https://cdn.example.com/cocos-1.0.1.zip"}
```

- `url` 非空：加载这个 HTTP(S) 页面，保持 URL 模式；不会混入上一次构建的 HTML。
- `url` 为空：必须下载 `last_version_url`，全量校验并解压到本次 `assets/html/`，随 release APK 一起打包。没有 ZIP 或入口时构建失败，不退化成 URL 套壳。
- `version` 是内置静态资源版本，1–64 位字母、数字、点、下划线、连字符；启动后与服务器版本按字符串是否相同比较。Android 原生的 versionCode/versionName 仍在 Gradle 配置中管理。
- `dominList` 是后端接口 origin 列表，最多 16 个，允许裸域名或带协议和端口的 origin。每次打包原样保留第一条作为 Cocos 的引导域名，供 `Android.getdomin()` 同步读取。版本查询仍使用规范化后的 origin，未配置固定 `VersionURL` 时至少提供一个。
- URL 模式的 `last_version_url` 可为空，四个键仍须保留。生产默认 HTTPS；仅明确的测试/内部配置可启用 HTTP。

成功时 `code` 为 `0`，从 `data.fileUrl` 取下载地址。文件名由随机作业 ID 生成。

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "id": "0123456789abcdef0123456789abcdef",
    "fileName": "0123456789abcdef0123456789abcdef.apk",
    "fileUrl": "/api/packager/v1/packaging/artifacts/0123456789abcdef0123456789abcdef.apk",
    "mode": "local",
    "version": "1.0.1",
    "bundleSha256": "下载 ZIP 的 SHA-256"
  },
  "request_id": "本次请求 ID",
  "timestamp": 1794787200000
}
```

参数错误、鉴权失败和限流分别使用业务码 `1001`、`1002`、`1005`，HTTP 状态为 200。限流或构建忙时可按 `Retry-After` 等待后重试。打包环境未就绪时返回 503 和业务码 `1007`。OpenAPI 文档通过 `GET /api/packager/v1/doc/openapi.json` 获取。

默认产物是 `<DataDir>/artifacts/<id>.apk`。下载本地 APK 时对 `data.fileUrl` 发起 GET 并带上同一个 token。启用 MinIO 时返回配置的 MinIO 对外地址。MinIO 上传失败时返回 502 和业务码 `1006`，此时可用 `data.fileUrl` 下载保留的本地 APK。每次构建的输入、Gradle 输出、脱敏日志和结果在 `<DataDir>/jobs/<id>/`。服务会逐字节检查 APK 内本次配置及所有静态资源，并用 SDK apksigner 验证签名。

已有调用方可以继续使用 `POST /packaging` 和 `GET /artifacts/<id>.apk`。旧打包入口直接返回上述 `data` 的字段，使用原有 HTTP 状态码和本地下载路径。

发布正式 Android 包通过 `golang_game` 的 `POST /api/admin/v1/clien_game/build`。Admin 调用本服务构建 APK，再按自身的 `StorageCfg` 将文件发布到 `android/a_<Unix 毫秒时间戳>.apk`，把桶内路径写入 `client_game_version.android_file_url`。新文件上传并登记成功后清理 `android/` 下的旧文件。

每次构建的 Android 包名采用 `yq.bxgame.com.b<Unix 毫秒时间戳>_<作业 ID>`。构建用独立的 RSA 密钥对签名，证书名称带有相同的时间戳和作业 ID。构建完成后清理临时密钥库。新 APK 会作为独立应用安装，分别使用各自的应用数据和资源缓存。

桌面图标使用 `app/src/main/res/mipmap-xxxhdpi/ic_launcher.webp`。应用、圆形图标和桌面启动入口都引用这份资源。返回产物前，服务核对实际包名、启动图标字节和本次生成的签名证书。容器部署时，替换图标后需用包含该资源的新镜像构建。

## 启动版本查询与热更新

项目原先没有可用的版本接口实现（旧代码为硬编码页面请求），现在采用以下可配置约定。**业务方需要提供该接口，本项目不会假设示例域名上已有服务器。**

- `app/packager/etc/packager-etc.yaml` 的 `VersionURL` 非空时使用这个固定地址。
- 否则，按 `dominList` 顺序尝试 `origin + VersionPath`，默认路径 `/app/version`。
- 每次新游戏会话启动发起 GET，追加 `current_version=当前资源版本`。普通回前台不发起新启动检查，配置重建复用原会话。检查和资源校验在后台，已有会话读取不等待下载锁。
- 接口直接返回 JSON（无 data 外层）：

```json
{
  "version": "1.0.2",
  "last_version_url": "https://cdn.example.com/cocos-1.0.2.zip",
  "sha256": "此ZIP文件的64位十六进制SHA256"
}
```

版本相同不下载。不同则校验下载 origin、目标地址、大小、完整 ZIP SHA-256、各文件 CRC、解压大小/数量/压缩率及入口，然后在应用私有目录中写入不可变 release，最后原子发布一个新的活动指针。指针与资源都不就地覆盖；重启时验证文件散列并保留最近两份有效版本。断网、超时、无效 JSON、坏 ZIP、校验失败、提交失败均继续使用旧版；旧版损坏则回退上一版或 APK 内置资源。ZIP64、多卷、加密、符号链接及特殊文件不用于客户端更新。

版本相同时可以只返回 version；版本不同时 last_version_url 和 sha256 必须有效。

本地内容使用 `https://appassets.androidplatform.net/html/<generation>/index.html`，由 X5/系统 WebView 共用拦截器直接读取 APK assets 或私有目录，支持 JS fetch/XHR、相对路径及 WASM MIME，不依赖外网提供内置资源。启动先在后台恢复并校验已有资源，再只加载一次游戏页面。每个会话固定 generation 和 JS 配置中的 version；后台下载验证成功只准备下一次新会话的版本，当前对局不重载。普通返回前台不切换页面，配置重建仍用原会话。旧 generation 在本次进程期间保留，避免混用两版文件。

URL 模式也在新会话启动时做版本查询和 ZIP 验证/缓存，但显示地址继续使用请求的 url；本地模式在下一次新会话选择已准备好的新资源页。原生 APK/SDK 插件不能通过 HTML 热更新改变。

### Cocos 静态目录与后端配置

将 Cocos Creator 的 Web 构建目录**内容**打成 ZIP，根目录必须直接有非空 `index.html`，保留完整子目录：

```text
index.html
assets/...
src/...
engine/...
...
```

不要额外包一层 `html/` 或项目名目录。所有实际依赖的静态文件均须放入 ZIP；本服务保留目录及字节内容，不分析 JavaScript 去推断遗漏的远程依赖。Cocos 的动态 bundle/远程资源下载逻辑仍由游戏代码管理，下载地址、CORS 和资源版本兼容由业务方配置。

Cocos 启动脚本可同步读取打包配置和引导域名。

```js
const packConfig = JSON.parse(window.Android.getPackConfig());
// packConfig: { url, dominList, version, last_version_url }
const backendOrigins = packConfig.dominList;
const bootstrapDomain = window.Android.getdomin();
// 由游戏代码按需选择 backendOrigins 发起业务请求。
```

内置及热更新资源的 `index.html` 会在首个页面脚本前设置 `window.getdomin`。Cocos 用它访问 `/api/domin/v1/domain/enabled`，提交 `{"type_list":["1","2"]}`，并使用返回的 HTTP 与 WebSocket 地址。

页面结束加载后另有 `window.PackAppConfig`，启动阶段应优先用同步桥接方法。设备 ID 的原有 `Android.getSpeedAppUid()` 保留。业务 API 需允许本地域名 origin 的 CORS；有登录凭据时按实际 Cookie/CORS 规则配置。WebView 禁用 file/content 访问，默认禁用 HTTPS 页面中的 HTTP 混合内容。

桥接配置中的 version 是当前会话固定的已验证资源版本；last_version_url 保留原始打包输入，后续更新下载地址始终以版本接口响应为准。

## 配置与本机启动

需要 Go 1.22+、Node 20+、JDK 17（含 `keytool`）、Android SDK `platforms;android-33` / `build-tools;33.0.2`。

1. 将实际 ZIP CDN origin 加入 `Download.AllowedOrigins`。这是完整 origin 白名单，协议和非标准端口都必须一致，不支持通配符。
2. 准备 `secrets/pack_token`，token 至少 16 字节。该目录被 Git 和 Docker 构建上下文排除。
3. 设置环境变量并启动（PowerShell 示例，路径换成实际安装位置）：

```powershell
$env:JAVA_HOME = "C:\path\to\jdk-17"
$env:PATH = "$env:JAVA_HOME\bin;$env:PATH"
$env:ANDROID_SDK_ROOT = "C:\path\to\android-sdk"
$env:GRADLE_USER_HOME = "$PWD\.cache\gradle"
$env:PACK_AUTH_TOKEN_FILE = "$PWD\secrets\pack_token"
go run ./app/packager -f app/packager/etc/packager-etc.yaml
```

服务 token 和对象存储凭据支持同名环境变量或 `*_FILE`（后者优先）。可通过 `PACK_PROJECT_ROOT`、`PACK_DATA_DIR`、`PACK_HOST` 覆盖路径与监听地址。默认本机只监听 127.0.0.1:10010，配置中的目录相对 ProjectRoot。构建工具缺失时存活检查仍可用，构建就绪检查返回 503。

```powershell
$token = (Get-Content -Raw secrets/pack_token).TrimEnd("`r", "`n")
$headers = @{ "X-Pack-Token" = $token }
$body = @{
  url = ""
  dominList = @("https://api.example.com")
  version = "1.0.1"
  last_version_url = "https://cdn.example.com/cocos-1.0.1.zip"
} | ConvertTo-Json -Compress
$service = "http://127.0.0.1:10010"
$result = Invoke-RestMethod "$service/api/packager/v1/packaging/build" -Method Post -Headers $headers -ContentType application/json -Body $body
if ($result.code -ne 0) { throw $result.msg }
if ($result.data.fileUrl.StartsWith("/")) {
  Invoke-WebRequest ($service + $result.data.fileUrl) -Headers $headers -OutFile $result.data.fileName
} else {
  Invoke-WebRequest $result.data.fileUrl -OutFile $result.data.fileName
}
```

`GET /healthz` 是公开的进程存活检查。`GET /readyz` 需鉴权，检查签名、Java、SDK 与已启用的 MinIO。`/health` 和 `/ready` 也可继续调用。默认请求体 16 KiB，每分钟最多 10 个有效打包请求，同项目同一时刻一个构建。两个打包入口共享限流和构建名额。构建由独立工作进程持有内核独占锁，`<DataDir>/build.lock` 是永久保留的 0 字节锁载体，文件存在不表示忙，**不要删除它**。同项目实例应使用同一 DataDir 和支持文件锁的本地文件系统。独占锁持有者用原子更新的 `build-owner.json` 记录 PID、进程出生身份、随机所有权标识及完成状态。

服务退出/请求断开使控制管道 EOF，工作进程取消 Node/Gradle 并回收所属子进程，随后释放内核锁；工作进程自身崩溃时，下一位锁持有者先验证旧进程身份和所属子进程再恢复。Windows 工作进程在启动任何子进程前加入独立 Job Object，关闭时回收其后代；Linux 使用 flock 与 pidfd 定向回收经所有权标识验证的进程，需要 Linux 5.3+ 和对同一 PID namespace 中本服务进程的 /proc 可见性。Kotlin 编译设为 in-process，不复用外部共享 Kotlin daemon。当前不是跨主机分布式构建队列；不要让不同 PID namespace 的并发实例共用未完成构建目录。Docker 重启需确保旧容器已停止，不能把本机 Windows 测试视为 Linux 容器恢复验收。

遇到仍存活的所有者或无法确定归属时返回 429，不会覆盖有效锁。仅旧版本留下的非空 O_EXCL 锁、损坏的所有权记录或无法验证的跨平台迁移需要人工处理：先停服务并确认相关构建进程结束，再归档旧记录后迁移。新实现的普通取消、超时和同环境进程崩溃不需要人工删除锁文件。

下载默认拒绝私网、回环、链路本地和保留地址；Go 每次建立连接解析全部 DNS 地址后直接连接已验证 IP，不使用环境代理，每次重定向重新校验 origin。Android 版本/ZIP 请求使用 OkHttp 自定义 DNS 回调，把全部校验通过的 InetAddress 直接交给连接层，避免“先检查、连接时再解析”的间隙；IP 字面量在连接前单独检查。禁用代理并逐跳检查重定向，保留原域名 Host/SNI 和默认 TLS 主机名/证书校验。该边界只覆盖版本/ZIP 更新请求，不会代理或约束游戏代码/WebView 自己的任意业务网络请求。HTTP 只用于受控测试。内网确有需求时额外设置精确的 AllowedPrivateCIDRs（例如本机测试的 127.0.0.1/32），不能用 /0 关闭控制。请求中的 dominList 不能改变下载白名单。

默认 ZIP 最大 128 MiB、解压总量 512 MiB、单文件 64 MiB、20000 项、压缩比 200，均有配置上限。数据目录持久保存产物和构建日志，请按业务保留期在确认构建空闲后归档/清理历史 jobs；服务不擅自删除已有 APK。

## Docker 部署

`Dockerfile` 构建独立 Linux amd64 镜像，内置 Go 服务、Node、JDK 17 和 Android 构建工具。服务 token 从运行时只读 secret 挂载，签名密钥在每次构建时生成。示例将服务发布到宿主机回环地址。

```sh
# 修改 app/packager/etc/packager-etc.yaml 的 AllowedOrigins / 版本地址，并准备上述 secrets。
docker compose build
docker compose up -d
curl http://127.0.0.1:10010/healthz
docker compose ps
docker compose logs --tail=100 packapp
```

Compose 已固定 linux/amd64，ARM 宿主机需支持该架构仿真，或调整 Go/JDK 架构配置。Compose 持久化 `/app/data` 与 `/app/gradle-cache`，配置和服务 token 以只读方式挂载。容器以 uid 10001 运行，挂载内容需给予该用户相应权限。不要把宿主目录覆盖挂载到 `/app/app`，否则会遮蔽 Android 工程。

镜像 HEALTHCHECK 检查进程存活。部署前用 token 调用 `/readyz` 确认打包环境。镜像构建会下载 Android SDK 并接受构建工具许可。生产构建环境需具备官方 Gradle/Maven/Google SDK 仓库的网络访问能力。TLS 前置代理和对外发布按实际部署环境另行配置。

手动健康检查使用 `./packager -f app/packager/etc/packager-etc.yaml -healthcheck`，需与服务使用相同的配置文件和环境变量。容器内的配置路径为 `/app/etc/packager.yaml`。检查读取实际 Host/Port（含 `PACK_HOST` 覆盖），将 `0.0.0.0` / `::` 分别转换为 `127.0.0.1` / `::1`，然后请求该地址的 `/health`。HTTP 200 退出 0，其他响应或连接失败退出 1。

MinIO 默认关闭。启用时配置 Storage.Enabled、Endpoint、ApkBucket、PublicBaseURL，以及运行时 PACK_STORAGE_ACCESS_KEY[_FILE] / PACK_STORAGE_SECRET_KEY[_FILE]。bucket 需预先创建；PublicBaseURL 必须是正确的 HTTPS bucket 对外路由。该存储与 dominList 无关。

## 验证与证据

```powershell
go test -count=1 -v ./...
go vet ./...
node --check index.js
go build -o .cache/verification/packager.exe ./app/packager
./tools/check-updater.ps1 -JavaHome $env:JAVA_HOME
./tools/check-lifecycle.ps1 -JavaHome $env:JAVA_HOME
python tools/verify_service.py --build
```

- Go 测试含真实回环 HTTP ZIP 下载、SSRF/重定向控制、ZIP 完整性和发布检查；handler 隔离测试中的 stub 不用于声称 APK 构建通过。
- JVM 脚本运行**生产 BundleUpdater**，用真实 HTTP、版本接口和 ZIP 验证升级、同版本跳过、失败保留、损坏后恢复、DNS 检查结果直接用于连接和会话版本固定；它不是 Android 设备/WebView 测试。
- 新增 Go 工作进程测试使用实际父/子/孙进程覆盖取消、超时、服务/工作进程崩溃、重启、并发、PID 身份变化及保护其他有效锁。
- Robolectric 在 JVM 中运行生产 X5WebActivity，检查新会话、普通回前台和配置重建；WebView 使用 Shadow 计数，不是设备或游戏渲染验收。其 Android 测试依赖由 Gradle 缓存到项目内后离线解析。
- 服务脚本用回环 ZIP 和版本服务测试构建取消与自动恢复，并通过两个 HTTP 入口构建 URL 和内置资源 APK。脚本验证同一请求重复打包时的包名与签名证书唯一性，同时检查桌面图标、下载接口和内置资源。
- 运行记录在 `.cache/verification/`；实际结果及未测边界见 [VERIFICATION.md](VERIFICATION.md)。测试 APK 使用本地测试证书及临时回环地址，不应作为生产包发布。

本机新增的官方便携工具仅位于 `.cache/toolchains/`，未改变系统安装。下载来源：[Microsoft OpenJDK](https://learn.microsoft.com/en-us/java/openjdk/download)、[Android SDK](https://developer.android.com/studio/)。

实现依据：[Win32 文件锁](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex)、[Job Object](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)、[Linux flock](https://man7.org/linux/man-pages/man2/flock.2.html)、[pidfd 定向发信号](https://man7.org/linux/man-pages/man2/pidfd_send_signal.2.html)、[OkHttp 4.11 路由如何使用 DNS 地址](https://github.com/square/okhttp/blob/parent-4.11.0/okhttp/src/main/kotlin/okhttp3/internal/connection/RouteSelector.kt)。

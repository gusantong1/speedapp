param([string]$JavaHome = $env:JAVA_HOME)
$ErrorActionPreference = 'Stop'
if (-not $JavaHome) { throw 'Pass a JDK 17 path' }
$taskRoot = Split-Path $PSScriptRoot -Parent
Push-Location $taskRoot
$taskPreviousAssets = $env:PACK_ASSETS_DIR
try {
    $taskAssets = Join-Path $taskRoot '.cache/verification/lifecycle-assets'
    New-Item -ItemType Directory -Force $taskAssets | Out-Null
    # 只用回环版本/ZIP 服务，测试会在运行时填入端口；不发外网请求。
    $taskConfig = @{
        url = ''; dominList = @(); version = '1.0.1'; last_version_url = ''
        buildId = 'lifecycle-test'; versionEndpoints = @()
        download = @{ allowHttp = $false; timeoutSec = 5; maxZipBytes = 1048576; maxExpandedBytes = 2097152; maxFileBytes = 1048576; maxEntries = 100; maxCompressionRatio = 200; allowedOrigins = @(); allowedPrivateCidrs = @() }
    }
    $taskConfig | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $taskAssets 'pack-config.json') -Encoding utf8
    New-Item -ItemType Directory -Force (Join-Path $taskAssets 'html') | Out-Null
    '<!doctype html><html><body>Lifecycle fixture</body></html>' | Set-Content -LiteralPath (Join-Path $taskAssets 'html/index.html') -Encoding utf8
    $env:PACK_ASSETS_DIR = $taskAssets
    & "$JavaHome/bin/java.exe" -classpath gradle/wrapper/gradle-wrapper.jar org.gradle.wrapper.GradleWrapperMain :app:testDebugUnitTest --tests yq.bxgame.com.StartupLifecycleTest --no-daemon --console=plain --stacktrace --project-cache-dir .cache/lifecycle-gradle 2>&1 | Tee-Object -FilePath .cache/verification/lifecycle-check.log
    if ($LASTEXITCODE -ne 0) { throw 'JVM Android lifecycle checks failed' }
} finally {
    $env:PACK_ASSETS_DIR = $taskPreviousAssets
    Pop-Location
}

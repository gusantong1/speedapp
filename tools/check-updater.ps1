param([string]$JavaHome = $env:JAVA_HOME)
$ErrorActionPreference = 'Stop'
if (-not $JavaHome) { throw 'Pass -JavaHome with a JDK path' }
$taskRoot = Split-Path $PSScriptRoot -Parent
Push-Location $taskRoot
try {
    New-Item -ItemType Directory -Force .cache/verification/jvm | Out-Null
    & "$JavaHome/bin/java.exe" -classpath gradle/wrapper/gradle-wrapper.jar org.gradle.wrapper.GradleWrapperMain :app:copyUpdaterRuntime --no-daemon --console=plain --project-cache-dir .cache/updater-gradle
    if ($LASTEXITCODE -ne 0) { throw 'Updater dependencies could not be resolved' }
    $taskClasspath = (Get-ChildItem .cache/verification/updater-libs/*.jar | ForEach-Object FullName) -join [IO.Path]::PathSeparator
    & "$JavaHome/bin/javac.exe" -encoding UTF-8 -classpath $taskClasspath -d .cache/verification/jvm app/src/main/java/yq/bxgame/com/BundleUpdater.java app/src/main/java/yq/bxgame/com/ContentSession.java tools/BundleUpdaterCheck.java
    if ($LASTEXITCODE -ne 0) { throw 'Updater compilation failed' }
    $taskState = '.cache/verification/jvm-state-' + [guid]::NewGuid().ToString('N')
    & "$JavaHome/bin/java.exe" -cp ('.cache/verification/jvm' + [IO.Path]::PathSeparator + $taskClasspath) BundleUpdaterCheck $taskState 2>&1 | Tee-Object -FilePath .cache/verification/updater-check.log
    if ($LASTEXITCODE -ne 0) { throw 'Updater HTTP checks failed' }
} finally { Pop-Location }

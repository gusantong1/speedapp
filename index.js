'use strict'

// Go 服务只传入本次 job.json；签名密码只经环境变量交给 Gradle。
const fs = require('node:fs')
const path = require('node:path')
const { spawnSync } = require('node:child_process')

const args = process.argv.slice(2)
if (args.length !== 2 || args[0] !== '--job') {
  console.error('Usage: node index.js --job /absolute/path/to/job.json')
  process.exit(1)
}
try {
  const job = JSON.parse(fs.readFileSync(args[1], 'utf8'))
  if (!/^[0-9a-f]{32}$/.test(job.id)) throw new Error('Invalid job ID')
  for (const key of ['assetsDir', 'buildDir', 'outputPath']) {
    if (!path.isAbsolute(job[key])) throw new Error('Job paths must be absolute')
  }
  const config = JSON.parse(fs.readFileSync(path.join(job.assetsDir, 'pack-config.json'), 'utf8'))
  if (config.buildId !== job.id) throw new Error('Job/config mismatch')
  if (!config.url && !fs.statSync(path.join(job.assetsDir, 'html', 'index.html')).isFile()) {
    throw new Error('Missing bundled html/index.html')
  }
  const env = { ...process.env,
    PACK_ASSETS_DIR: job.assetsDir,
    PACK_BUILD_DIR: job.buildDir,
    PACK_APP_NAME: job.appName,
    PACK_ALLOW_HTTP: job.allowHttp ? 'true' : 'false',
    PACK_PROJECT_CACHE: path.join(path.dirname(job.assetsDir), 'gradle-project-cache')
  }
  // 直接调用相同的 Gradle Wrapper Java 入口，跨平台且没有 shell 引号/注入问题。
  const commandArgs = ['-Xmx64m', '-Xms64m', '-Dfile.encoding=UTF-8', '-classpath',
    path.join(__dirname, 'gradle', 'wrapper', 'gradle-wrapper.jar'),
    'org.gradle.wrapper.GradleWrapperMain', ':app:assembleRelease', '--no-daemon',
    '--console=plain', '--stacktrace', '--project-cache-dir', env.PACK_PROJECT_CACHE]
  const build = spawnSync('java', commandArgs, { cwd: __dirname, env, stdio: 'inherit', windowsHide: true })
  if (build.error || build.status !== 0) throw new Error('Gradle release build failed')
  const apk = path.join(job.buildDir, 'outputs', 'apk', 'release', 'app-release.apk')
  if (!fs.statSync(apk).isFile()) throw new Error('Signed release APK was not produced')
  const sdk = process.env.ANDROID_SDK_ROOT || process.env.ANDROID_HOME
  if (!sdk) throw new Error('ANDROID_SDK_ROOT is required')
  const signer = path.join(sdk, 'build-tools', '33.0.2', 'lib', 'apksigner.jar')
  const verify = spawnSync('java', ['-jar', signer, 'verify', '--verbose', apk], { env, stdio: 'inherit', windowsHide: true })
  if (verify.error || verify.status !== 0) throw new Error('APK signature verification failed')
  fs.mkdirSync(path.dirname(job.outputPath), { recursive: true })
  fs.copyFileSync(apk, job.outputPath, fs.constants.COPYFILE_EXCL)
  console.log('Signed APK created for job ' + job.id)
} catch (error) {
  console.error(error.message)
  process.exitCode = 1
}

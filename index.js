'use strict'

// Go 服务传入本次 job.json 和独立的签名密码。密码只经环境变量传递。
const fs = require('node:fs')
const path = require('node:path')
const { spawnSync } = require('node:child_process')
const { createHash } = require('node:crypto')

function buildSignedAPK(job, env) {
  if (fs.existsSync(job.keystorePath)) throw new Error('Build keystore already exists')
  const keyArgs = ['-keystore', job.keystorePath, '-storetype', 'PKCS12',
    '-alias', env.PACK_KEYSTORE_ALIAS, '-storepass:env', 'PACK_KEYSTORE_STORE_PASS']
  const options = { cwd: __dirname, env, windowsHide: true }
  try {
    const key = spawnSync('keytool', ['-genkeypair', ...keyArgs, '-keyalg', 'RSA', '-keysize', '2048',
      '-validity', '10000', '-dname', 'CN=' + env.PACK_KEYSTORE_ALIAS],
    { ...options, stdio: ['ignore', 'ignore', 'inherit'] })
    if (key.error || key.status !== 0) throw new Error('Could not generate build keystore')
    fs.chmodSync(job.keystorePath, 0o600)

    // 直接调用 Gradle Wrapper 的 Java 入口，跨平台且避免 shell 参数转义。
    const commandArgs = ['-Xmx64m', '-Xms64m', '-Dfile.encoding=UTF-8', '-classpath',
      path.join(__dirname, 'gradle', 'wrapper', 'gradle-wrapper.jar'),
      'org.gradle.wrapper.GradleWrapperMain', ':app:assembleRelease', '--no-daemon',
      '--console=plain', '--stacktrace', '--project-cache-dir', env.PACK_PROJECT_CACHE]
    const build = spawnSync('java', commandArgs, { ...options, stdio: 'inherit' })
    if (build.error || build.status !== 0) throw new Error('Gradle release build failed')
    const apk = path.join(job.buildDir, 'outputs', 'apk', 'release', 'app-release.apk')
    if (!fs.statSync(apk).isFile()) throw new Error('Signed release APK was not produced')

    const certificate = spawnSync('keytool', ['-exportcert', ...keyArgs], options)
    if (certificate.error || certificate.status !== 0 || !certificate.stdout.length) {
      throw new Error('Could not read build certificate')
    }
    const expected = createHash('sha256').update(certificate.stdout).digest('hex')
    const sdk = env.ANDROID_SDK_ROOT || env.ANDROID_HOME
    const signer = path.join(sdk, 'build-tools', '33.0.2', 'lib', 'apksigner.jar')
    const verify = spawnSync('java', ['-jar', signer, 'verify', '--verbose', '--print-certs', apk],
      { ...options, encoding: 'utf8' })
    if (verify.stderr) process.stderr.write(verify.stderr)
    if (verify.error || verify.status !== 0) throw new Error('APK signature verification failed')
    const actual = /^Signer #1 certificate SHA-256 digest: ([0-9a-f]{64})\r?$/m.exec(verify.stdout)?.[1]
    if (actual !== expected || !/^Number of signers: 1\r?$/m.test(verify.stdout)) {
      throw new Error('APK signer differs from the certificate generated for this build')
    }
    process.stdout.write(verify.stdout)
    return apk
  } finally {
    fs.rmSync(job.keystorePath, { force: true })
  }
}

const args = process.argv.slice(2)
if (args.length !== 2 || args[0] !== '--job') {
  console.error('Usage: node index.js --job /absolute/path/to/job.json')
  process.exit(1)
}
try {
  const job = JSON.parse(fs.readFileSync(args[1], 'utf8'))
  if (!/^[0-9a-f]{32}$/.test(job.id)) throw new Error('Invalid job ID')
  if (!/^yq\.bxgame\.com\.b\d+_[0-9a-f]{32}$/.test(job.packageName) || !job.packageName.endsWith('_' + job.id)) {
    throw new Error('Invalid build package name')
  }
  for (const key of ['assetsDir', 'buildDir', 'outputPath', 'keystorePath']) {
    if (!path.isAbsolute(job[key])) throw new Error('Job paths must be absolute')
  }
  const config = JSON.parse(fs.readFileSync(path.join(job.assetsDir, 'pack-config.json'), 'utf8'))
  if (config.buildId !== job.id) throw new Error('Job/config mismatch')
  if (!config.url && !fs.statSync(path.join(job.assetsDir, 'html', 'index.html')).isFile()) {
    throw new Error('Missing bundled html/index.html')
  }
  if (!process.env.PACK_KEYSTORE_STORE_PASS) throw new Error('Build signing password is missing')
  if (!process.env.ANDROID_SDK_ROOT && !process.env.ANDROID_HOME) throw new Error('ANDROID_SDK_ROOT is required')
  const env = { ...process.env,
    PACK_ASSETS_DIR: job.assetsDir,
    PACK_BUILD_DIR: job.buildDir,
    PACK_APP_NAME: job.appName,
    PACK_APPLICATION_ID: job.packageName,
    PACK_KEYSTORE_PATH: job.keystorePath,
    PACK_KEYSTORE_ALIAS: job.packageName.slice(job.packageName.lastIndexOf('.') + 1),
    PACK_ALLOW_HTTP: job.allowHttp ? 'true' : 'false',
    PACK_PROJECT_CACHE: path.join(path.dirname(job.assetsDir), 'gradle-project-cache')
  }
  const apk = buildSignedAPK(job, env)
  fs.mkdirSync(path.dirname(job.outputPath), { recursive: true })
  fs.copyFileSync(apk, job.outputPath, fs.constants.COPYFILE_EXCL)
  console.log('Signed APK created for ' + job.packageName)
} catch (error) {
  console.error(error.message)
  process.exitCode = 1
}

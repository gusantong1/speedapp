var shell = require('shelljs')
var path = require('path')
var fs = require('fs')
var xml2js = require('xml2js')
var parseXml = xml2js.parseString

// ----------- CLI args -----------
const argv = process.argv.slice(2)
function readArg(name, defVal) {
  const key = `--${name}`
  const idx = argv.indexOf(key)
  if (idx !== -1 && argv[idx + 1]) return argv[idx + 1]
  return defVal
}
const appName = readArg('appName', null)
const webviewUrlArg = readArg('webviewUrl', null)
const agentCode = readArg('agentCode', appName)

if (!appName || !webviewUrlArg) {
  console.error('\nUsage: node index.js --appName "Your App" --webviewUrl "https://example.com" [--agentCode code]\n')
  process.exit(1)
}

// ----------- Paths -----------
const appProjectPath = '/Users/app/pro/demo/speedapp'
const agentApksDir = path.join(appProjectPath, 'data/agent_apks')
const apkPath = path.join(appProjectPath, 'app/build/outputs/apk/release/app-release.apk')
const apktoolPath = path.join(appProjectPath, 'apktool.jar')
const unApkDir = path.join(appProjectPath, '.cache/app-speed-release')
const tempDir = path.join(appProjectPath, '.cache/tmp_speed_release')

shell.mkdir('-p', path.dirname(unApkDir))
shell.mkdir('-p', agentApksDir)

//删除apk
shell.rm('-rf', path.join(agentApksDir, '*'))

// ----------- Build base release APK -----------
if (shell.exec(`cd ${appProjectPath} && ./gradlew assembleRelease`).code !== 0) {
  console.error('Gradle build failed')
  process.exit(1)
}

// ----------- Decode APK -----------
shell.rm('-rf', unApkDir)
if (shell.exec(`java -jar ${apktoolPath} d ${apkPath} -o ${unApkDir}`).code !== 0) {
  console.error('apktool decode failed')
  process.exit(1)
}

// ----------- Prepare temp working dir -----------
shell.rm('-rf', tempDir)
shell.mkdir('-p', tempDir)
shell.exec(`cp -r ${unApkDir}/* ${tempDir}`)

// ----------- Patch strings.xml -----------
const stringsPath = `${tempDir}/res/values/strings.xml`
if (!fs.existsSync(stringsPath)) {
  console.error('strings.xml not found in decoded APK')
  process.exit(1)
}

const stringsFile = fs.readFileSync(stringsPath, { encoding: 'utf8' })
parseXml(stringsFile, { trim: true }, function (err, result) {
  if (err) {
    console.error('Parse strings.xml error:', err)
    process.exit(1)
  }
  const arr = result && result.resources && result.resources.string ? result.resources.string : []
  arr.forEach(function (item) {
    const name = (item.$ && item.$.name) || ''
    if (name === 'app_name') item._ = appName
    if (name === 'app_agent') item._ = agentCode
    if (name === 'app_webview_url') item._ = webviewUrlArg
  })
  const builder = new xml2js.Builder()
  const xml = builder.buildObject(result)
  fs.writeFileSync(stringsPath, xml)
  finalize()
})

function pickLatestBuildTools() {
  const sdkRoot = process.env.ANDROID_SDK_ROOT || process.env.ANDROID_HOME || '/Users/app/Library/Android/sdk'
  const btDir = path.join(sdkRoot, 'build-tools')
  if (!fs.existsSync(btDir)) return null
  const versions = fs.readdirSync(btDir).filter(n => /\d+/.test(n))
  if (!versions.length) return null
  versions.sort((a, b) => a.localeCompare(b, undefined, { numeric: true }))
  const latest = versions[versions.length - 1]
  return path.join(btDir, latest)
}

function sanitizeFileName(name) {
  return String(name).replace(/[^a-zA-Z0-9._-]+/g, '_')
}

function finalize() {
  console.log('资源替换完成，开始重新打包、对齐并签名...')

  const outBase = sanitizeFileName(agentCode || 'custom')
  const preReleaseApkPath = path.join(agentApksDir, `${outBase}-pre-release.apk`)
  const realignedApkPath = path.join(agentApksDir, `${outBase}-release.apk`)
  const targetPath = path.join(agentApksDir, `${outBase}.apk`)

  if (shell.exec(`java -jar ${apktoolPath} b ${tempDir} -o ${preReleaseApkPath}`).code !== 0) {
    console.error('apktool build failed')
    process.exit(1)
  }

  const bt = pickLatestBuildTools()
  if (!bt) {
    console.error('Cannot find Android build-tools. Please set ANDROID_SDK_ROOT correctly.')
    process.exit(1)
  }

  const zipalign = path.join(bt, 'zipalign')
  const apksigner = path.join(bt, 'apksigner')

  if (shell.exec(`${zipalign} -p -f -v 4 ${preReleaseApkPath} ${realignedApkPath}`).code !== 0) {
    console.error('zipalign failed')
    process.exit(1)
  }

  const keystorePath = path.join(appProjectPath, 'app/henry20230831114241-keystore.jks')
  const signCmd = `${apksigner} sign --ks ${keystorePath} --ks-key-alias henry20230831114241 --ks-pass pass:123456 --key-pass pass:123456 --out ${targetPath} ${realignedApkPath}`
  if (shell.exec(signCmd).code !== 0) {
    console.error('apksigner failed')
    process.exit(1)
  }

  console.log('签名完成，输出文件: ', targetPath)

  // 清理中间文件
  shell.rm('-rf', tempDir)
  shell.rm('-f', preReleaseApkPath)
  shell.rm('-f', realignedApkPath)
}





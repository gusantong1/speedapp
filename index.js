var shell = require('shelljs')
var path = require('path')
var fs = require('fs')
var xml2js = require('xml2js')
var parseXml = xml2js.parseString
// 安装jdk  java 18.0.2.1
// 安装 ndk

// 存放代理的apk包的目录
const agentApksDir = '/data/agent_apks'
// app源码项目目录
const appProjectPath = '/data/Speed-APP'
// apk输出路径
const apkPath = path.join(appProjectPath, 'app/build/outputs/apk/release/app-release.apk')
// apktool工具路径
const apktoolPath = path.join(appProjectPath, 'apktool.jar')

// 解压缩输出目录
const unApkDir = path.join(appProjectPath, `./.cache/app-speed-release`)
/**
 * 打基础包
 */
shell.exec(`cd ${appProjectPath} && ./gradlew assembleRelease`)

// 解压apk (如果命令执行报错如果输入目录不存在， 请自行创建)
const unApkCmd = `java -jar ${apktoolPath} d ${apkPath} -o ${unApkDir}`
shell.exec(unApkCmd)



// 反编译逻辑 替换 内容
// 创建零时目录
const tempDir = path.join(appProjectPath, `./.cache/tmp_speed_release`)
shell.exec(`mkdir -p ${tempDir}`)
// 将解压缩的内容拷贝过来
shell.exec(`cp -r ${unApkDir}/* ${tempDir}`)

let finshFileOne = false
let finshFileTwo = false

// 如果需要更换桌面 icon 则自行替换 ${tempDir}/res/mipmap-xxxhdpi/ic_launcher.webp
// 如果需要更换启动图 则自行替换 也可以自行找到对应的目录自行更换
const apkName = 'MiningBay'
const agentCode = 'test1'
const webviewUrl = 'https://www.baidu.com'
const stringsFile = fs.readFileSync(`${unApkDir}/res/values/strings.xml`, {encoding: 'utf8'})
parseXml(stringsFile, {trim: true}, function (err, result) {
  result.resources.string.forEach(function (item) {
    const name = item.$.name || ''
    // apk名字替换
    if (name === 'app_name') {
      item._ = apkName
    }
    // 代理标识
    if (name === 'app_agent') {
      item._ = agentCode
    }
    // 默认打开的网站地址
    if (name === 'app_webview_url') {
      item._ = webviewUrl
    }
  })
  const builder = new xml2js.Builder()
  const xml = builder.buildObject(result)
  fs.writeFileSync(`${unApkDir}/res/values/strings.xml`, xml)
  finshFileOne = true
  startSignFun()
})


// 替换重复安装的包名 换一个包名，避免多个app不能同时安装

const mainFestStr = fs.readFileSync(`${unApkDir}/AndroidManifest.xml`, {encoding: 'utf8'})
mainFestStr = mainFestStr.replace(/dev/g, agentCode)
parseXml(mainFestStr, {trim: true}, function (err, result) {
  const packageName = result.manifest.$.package || ''
  const packageNameArr = packageName.split('.')
  packageNameArr[2] = agentCode
  result.manifest.$.package = packageNameArr.join('.')
  const builder = new xml2js.Builder()
  const xml = builder.buildObject(result)
  fs.writeFileSync(`${unApkDir}/AndroidManifest.xml`, xml)
  finshFileTwo = true
  startSignFun()
})


function startSignFun() {
  if (!finshFileOne || !finshFileTwo) return
  console.log('替换完成')
  // 打包的零时apk文件
  const preReleaseApkPath = path.join(agentApksDir, `/${agentCode}-pre-release.apk`)
  // apk文件内容对齐后的文件
  const realyReleaseApkPath = path.join(agentApksDir, `/${agentCode}-release.apk`)
  // 签名后的文件  也是实际可安装的的文件
  const targetPath = path.join(agentApksDir, `/${agentCode}-signed-release.apk`)
  const buildCmd = `java -jar ${apktoolPath} b ${tempDir} -o ${preReleaseApkPath}`
  shell.exec(buildCmd)
  // 用ndk中的工具将apk文件的内容对其4字节（请自行替换ndk目录）此操作一定要执行，要不然有些环境下反编译出来的apk无法安装
  const zipAlignCmd = `/data/android_sdk/build-tools/34.0.0/zipalign -p -f -v 4 ${preReleaseApkPath} ${realyReleaseApkPath}`
  shell.exec(zipAlignCmd)
  // 将对其后的apk文件签名（请自行替换ndk目录）
  const signCmd = `/data/download/android_sdk/build-tools/34.0.0/apksigner sign --ks ${path.join(appProjectPath, 'henry20230831114241-keystore.jks')} --ks-key-alias henry20230831114241 --ks-pass pass:123456 --key-pass pass:123456 --out ${targetPath} ${realyReleaseApkPath}`
  shell.exec(signCmd)
  
  // 删除零时文件
  shell.exec(`rm -rf ${tempDir}`)
  shell.exec(`rm -rf ${preReleaseApkPath}`)
  shell.exec(`rm -rf ${realyReleaseApkPath}`)
}





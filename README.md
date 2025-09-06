打包命令： ./gradlew assembleRelease
打完包的包地址和文件名 ：.webview_app/app/build/outputs/apk/release/app-release.apk

打包后反编译需替换内容：
   1：.webview_app/app/src/main/res/mipmap-xxxhdpi/ic_launcher.webp
   2：.webview_app/app/src/main/res/values/strings.xml
        里的下面这二个值
          <string name="app_name">BX_DEV</string>  ///应用名
          <string name="app_site">bs8</string>///站点别称 默认巴西验示

    
    注：如需换包名需求按能前那套 （默认值：okbet.bxgame.dev） 换包名需要把 okbet.bxgame.dev 换成okbet.bxgame.(站点编号app_site)

    注： 打包集成-反编译可以复制以前那套做调整,跟以前那套只有包名不一样，在就是app_site 这个值换成站点别称 用于调总控
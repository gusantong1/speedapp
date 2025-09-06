package yq.bxgame.com

import android.app.Application
import android.util.Log
import com.tencent.smtt.sdk.QbSdk
import com.tencent.smtt.sdk.TbsListener

class App : Application() {
    override fun onCreate() {
        super.onCreate()
        initX5()
    }

    private fun initX5() {
        // 非 WiFi 环境下允许下载 X5 内核
        QbSdk.setDownloadWithoutWifi(true)

        // 设置 X5 初始化监听
        QbSdk.setTbsListener(object : TbsListener {
            override fun onDownloadFinish(i: Int) {
                Log.d("X5", "X5 内核下载完成")
            }

            override fun onInstallFinish(i: Int) {
                Log.d("X5", "X5 内核安装完成")
            }

            override fun onDownloadProgress(i: Int) {
                Log.d("X5", "下载进度: $i%")
            }
        })

        // 初始化 X5 环境
        QbSdk.initX5Environment(this, object : QbSdk.PreInitCallback {
            override fun onCoreInitFinished() {
                Log.d("X5", "X5 核心初始化完成")
            }

            override fun onViewInitFinished(isSuccess: Boolean) {
                Log.d("X5", "X5 视图初始化: ${if (isSuccess) "成功" else "失败"}")
                if (!isSuccess) {
                    // 初始化失败，可以回退到系统 WebView
                    Log.e("X5", "X5 初始化失败，将使用系统 WebView")
                }
            }
        })
    }
}
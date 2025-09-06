package yq.bxgame.com

import android.content.Context
import com.tencent.smtt.sdk.QbSdk

object X5Utils {
    /**
     * 检查 X5 内核是否加载成功
     */
    fun isX5Available(context: Context): Boolean {
        return QbSdk.isTbsCoreInited()
    }

    /**
     * 获取 X5 内核版本
     */
    fun getX5Version(context: Context): String {
        return QbSdk.getTbsVersion(context).toString()
    }

    /**
     * 强制使用系统 WebView (调试用)
     */
    fun forceSysWebView(context: Context) {
        QbSdk.reset(context)
    }
}
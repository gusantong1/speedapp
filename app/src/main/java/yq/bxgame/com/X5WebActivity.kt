package yq.bxgame.com

import android.Manifest
import android.R.bool
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.util.Log
import android.view.KeyEvent
import android.view.View
import android.webkit.JavascriptInterface
import android.webkit.WebSettings
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.lifecycleScope
import com.tencent.smtt.export.external.interfaces.WebResourceRequest
import com.tencent.smtt.sdk.QbSdk
import com.tencent.smtt.sdk.ValueCallback
import com.tencent.smtt.sdk.WebChromeClient
import com.tencent.smtt.sdk.WebView
import com.tencent.smtt.sdk.WebViewClient
import kotlinx.coroutines.launch
import org.json.JSONObject
import yq.bxgame.com.R
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import kotlin.concurrent.thread


class X5WebActivity : AppCompatActivity() {
    private var x5WebView: WebView? = null
    private var sysWebView: android.webkit.WebView? = null
    private var useSystemWebView: Boolean = false

    

    ///接口域名
    fun isMasterControl(app_site: String): String {
       return "https://baidu.com/"
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setDarkStatusBar()

        // 决定是否使用 X5
        val canUseX5 = try { QbSdk.isTbsCoreInited() } catch (e: Throwable) { false }

        if (canUseX5) {
            try {
                setContentView(R.layout.activity_x5_web)
                x5WebView = findViewById(R.id.x5_webview)
                if (x5WebView == null) {
                    Log.e("X5", "x5_webview not found, fallback to system WebView")
                    fallbackToSystemWebView()
                } else {
                    initX5WebView()
                }
            } catch (e: Throwable) {
                Log.e("X5", "inflate X5 WebView failed, fallback to system WebView", e)
                fallbackToSystemWebView()
            }
        } else {
            Log.w("X5", "X5 core not inited, fallback to system WebView")
            fallbackToSystemWebView()
        }

        val params = mapOf(
            "pf" to getString(R.string.app_agent),
            "pt" to "2"
        )

        checkPermissions()
        lifecycleScope.launch {
            try {
                postFormData(
                    urlString = isMasterControl(getString(R.string.app_agent)),
                    params = params,
                    callback = {
                        try {
                            if (it != null && it.trim().startsWith("{")) {
                                val jsonObject = JSONObject(it)
                                val urlString = jsonObject.optString("data", null)
                                if (!urlString.isNullOrBlank()) {
                                    val sharedPref = getSharedPreferences("app_cache", MODE_PRIVATE)
                                    sharedPref.edit().putString("cached_url", urlString).apply()
                                }
                            } else {
                                Log.w("X5", "Response is not JSON, skip caching: ${'$'}it")
                            }
                        } catch (e: Exception) {
                            Log.e("X5", "Failed to parse JSON response", e)
                        }
                    }
                )
            } catch (_: Exception) { }
        }

        val sharedPref = getSharedPreferences("app_cache", MODE_PRIVATE)
        val cachedUrl = sharedPref.getString("cached_url", getString(R.string.app_webview_url))
        val finalUrl = cachedUrl ?: getString(R.string.app_webview_url)
        if (useSystemWebView) {
            sysWebView?.loadUrl(finalUrl)
        } else {
            x5WebView?.loadUrl(finalUrl)
        }
    }


    fun Activity.setDarkStatusBar() {
        // 设置状态栏背景为黑色或深色
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            window.statusBarColor = Color.BLACK // 或使用你的深色主题色
        }

        // 设置状态栏文字和图标为白色
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            window.decorView.systemUiVisibility =
                window.decorView.systemUiVisibility and
                        View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR.inv()
        }
    }

    fun postFormData1(urlString: String,  callback: (Boolean) -> Unit) {
        thread {
            try {
                val url = URL("https://"+urlString)
                val connection = url.openConnection() as HttpURLConnection

                // 设置请求方法和头部
                connection.requestMethod = "GET"
                connection.setRequestProperty("Content-Type", "application/x-www-form-urlencoded")
                connection.doOutput = true

                // 获取响应
                val responseCode = connection.responseCode
                if (responseCode == HttpURLConnection.HTTP_OK) {
                    runOnUiThread { callback(true) }
                } else {
                    runOnUiThread { callback(false) }
                }
            } catch (e: Exception) {
                runOnUiThread { callback(false) }
            } finally {
//                connection?.disconnect()
            }
        }
    }


    fun postFormData(urlString: String, params: Map<String, String>, callback: (String) -> Unit) {
        thread {
            try {
                val url = URL(urlString)
                val connection = url.openConnection() as HttpURLConnection

                // 设置请求方法和头部
                connection.requestMethod = "POST"
                connection.setRequestProperty("Content-Type", "application/x-www-form-urlencoded")
                connection.doOutput = true

                // 构建参数字符串
                val postData = params.map { (key, value) ->
                    "$key=${URLEncoder.encode(value, "UTF-8")}"
                }.joinToString("&")

                // 写入参数
                connection.outputStream.bufferedWriter().use {
                    it.write(postData)
                    it.flush()
                }

                // 获取响应
                val responseCode = connection.responseCode
                if (responseCode == HttpURLConnection.HTTP_OK) {
                    val response = connection.inputStream.bufferedReader().use { it.readText() }
                    runOnUiThread { callback(response) }
                } else {
                    runOnUiThread { callback("Error: $responseCode") }
                }
            } catch (e: Exception) {
                runOnUiThread { callback("Error: ${e.message}") }
            } finally {
//                connection?.disconnect()
            }
        }
    }

    class WebAppInterface(private val context: Context) {
        // 暴露给JS的方法名
        @JavascriptInterface
        fun getSpeedAppUid(): String {
            Log.d("getSpeedAppUid",Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID) ?: "")
            return Settings.Secure.getString(
                context.contentResolver,
                Settings.Secure.ANDROID_ID
            ) ?: ""
        }
    }

    private fun initX5WebView() {
        val webSettings = x5WebView!!.settings

        // 基础设置
        webSettings.javaScriptEnabled = true
        webSettings.domStorageEnabled = true
        webSettings.databaseEnabled = true
        webSettings.setAppCacheEnabled(true)
        webSettings.setAppCachePath(cacheDir.absolutePath)


        // 初始化X5 WebView
        // 初始化X5 WebView
        webSettings.setJavaScriptEnabled(true)
        webSettings.setDomStorageEnabled(true)
        webSettings.setDatabaseEnabled(true)
        webSettings.setCacheMode(WebSettings.LOAD_DEFAULT)

// 设置混合内容模式（针对HTTPS页面中的HTTP资源）

// 设置混合内容模式（针对HTTPS页面中的HTTP资源）
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            webSettings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW)
        }




        // 启用必要的WebView设置
        webSettings.setAllowFileAccess(true);
        webSettings.setAllowContentAccess(true);
        webSettings.setAllowFileAccessFromFileURLs(true);
        webSettings.setAllowUniversalAccessFromFileURLs(true);
        webSettings.setDomStorageEnabled(true);


        // 缩放设置
        webSettings.setSupportZoom(true)
        webSettings.builtInZoomControls = true
        webSettings.displayZoomControls = false

        // 自适应屏幕
        webSettings.useWideViewPort = true
        webSettings.loadWithOverviewMode = true

        // 设置 WebViewClient 和 WebChromeClient
        x5WebView!!.webViewClient = object : WebViewClient() {

            override fun onPageFinished(view: WebView, url: String) {
                super.onPageFinished(view, url)

                val deviceId = Settings.Secure.getString(
                    contentResolver,
                    Settings.Secure.ANDROID_ID
                ) ?: ""

                val jsCode = """
            window.deviceId = '$deviceId';
            console.log('Device ID injected:', window.deviceId);
        """.trimIndent()

                Log.d("jsCode",jsCode)

                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.KITKAT) {
                    x5WebView?.evaluateJavascript(jsCode, null)
                } else {
                    x5WebView?.loadUrl("javascript:$jsCode")
                }
            }

            override fun shouldOverrideUrlLoading(
                view: WebView?,
                request: WebResourceRequest?
            ): Boolean {
                view?.loadUrl(request?.url.toString())
                return true
            }

            override fun onReceivedError(
                view: WebView?,
                errorCode: Int,
                description: String?,
                failingUrl: String?
            ) {
                // 处理网页加载错误
            }
        }

        x5WebView!!.webChromeClient = X5WebChromeClient(this)


        x5WebView!!.addJavascriptInterface(WebAppInterface(this@X5WebActivity), "Android")

            val ua = webSettings.userAgentString
            webSettings.userAgentString = "$ua myapp"
    }

    private fun fallbackToSystemWebView() {
        useSystemWebView = true
        setContentView(R.layout.activity_x5_web_sys)
        sysWebView = findViewById(R.id.sys_webview)

        val settings = sysWebView!!.settings
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.databaseEnabled = true
        settings.cacheMode = android.webkit.WebSettings.LOAD_DEFAULT
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            settings.mixedContentMode = android.webkit.WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
        }
        settings.allowFileAccess = true
        settings.allowContentAccess = true
        settings.setSupportZoom(true)
        settings.builtInZoomControls = true
        settings.displayZoomControls = false
        settings.useWideViewPort = true
        settings.loadWithOverviewMode = true

        sysWebView!!.webViewClient = object : android.webkit.WebViewClient() {
            override fun onPageFinished(view: android.webkit.WebView, url: String) {
                super.onPageFinished(view, url)
                val deviceId = Settings.Secure.getString(contentResolver, Settings.Secure.ANDROID_ID) ?: ""
                val jsCode = """
            window.deviceId = '$deviceId';
            console.log('Device ID injected:', window.deviceId);
        """.trimIndent()
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.KITKAT) {
                    sysWebView?.evaluateJavascript(jsCode, null)
                } else {
                    sysWebView?.loadUrl("javascript:$jsCode")
                }
            }
        }
        sysWebView!!.webChromeClient = android.webkit.WebChromeClient()
        sysWebView!!.addJavascriptInterface(WebAppInterface(this@X5WebActivity), "Android")
        settings.userAgentString = settings.userAgentString + " myapp"
    }

    object FileChooserHelper {
        private var mUploadMessage: ValueCallback<Array<Uri>>? = null
        private val handler = Handler(Looper.getMainLooper())
        private var timeoutRunnable: Runnable? = null

        @Synchronized
        fun setUploadMessage(callback: ValueCallback<Array<Uri>>) {
            Log.d("FileChooser", "设置新的上传回调: ${callback.hashCode()}")
            clearCallbacks()
            mUploadMessage = callback
            startTimeout()
        }

        @Synchronized
        fun onActivityResult(resultCode: Int, data: Intent?) {
            Log.d("FileChooser", "处理Activity结果, 当前回调: ${mUploadMessage?.hashCode()}")

            timeoutRunnable?.let { handler.removeCallbacks(it) }
            timeoutRunnable = null

            try {
                when {
                    mUploadMessage == null -> {
                        Log.w("FileChooser", "回调对象已丢失!")
                    }
                    resultCode != Activity.RESULT_OK -> {
                        Log.d("FileChooser", "用户取消选择")
                        mUploadMessage?.onReceiveValue(null)
                    }
                    data == null -> {
                        Log.d("FileChooser", "没有返回数据")
                        mUploadMessage?.onReceiveValue(null)
                    }
                    else -> {
                        val uris = parseResultUris(data)
                        Log.d("FileChooser", "返回URI数量: ${uris.size}")
                        mUploadMessage?.onReceiveValue(uris)
                    }
                }
            } catch (e: Exception) {
                Log.e("FileChooser", "处理结果异常", e)
                mUploadMessage?.onReceiveValue(null)
            } finally {
                mUploadMessage = null
            }
        }

        private fun parseResultUris(data: Intent): Array<Uri> {
            return if (data.clipData != null) {
                Array(data.clipData!!.itemCount) { i ->
                    Log.d("FileChooser", "多选URI: ${data.clipData!!.getItemAt(i).uri}")
                    data.clipData!!.getItemAt(i).uri
                }
            } else {
                Log.d("FileChooser", "单选URI: ${data.data}")
                arrayOf(data.data!!)
            }
        }

        private fun startTimeout() {
            timeoutRunnable = Runnable {
                Log.w("FileChooser", "操作超时，释放回调")
                synchronized(this) {
                    mUploadMessage?.onReceiveValue(null)
                    mUploadMessage = null
                    timeoutRunnable = null
                }
            }
            handler.postDelayed(timeoutRunnable!!, 30000)
        }

        @Synchronized
        fun clearCallbacks() {
            Log.d("FileChooser", "清除所有回调")
            timeoutRunnable?.let { handler.removeCallbacks(it) }
            timeoutRunnable = null
            mUploadMessage?.onReceiveValue(null)
            mUploadMessage = null
        }
    }


    class X5WebChromeClient(private val activity: Activity) : WebChromeClient() {
        private val REQUEST_CODE_FILE_CHOOSER = 100

        override fun onShowFileChooser(
            webView: WebView?,
            filePathCallback: ValueCallback<Array<Uri>>,
            fileChooserParams: FileChooserParams
        ): Boolean {
            FileChooserHelper.setUploadMessage(filePathCallback)

            val intent = Intent(Intent.ACTION_GET_CONTENT).apply {
                addCategory(Intent.CATEGORY_OPENABLE)
                type = "image/*"
                putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
            }

            try {
                activity.startActivityForResult(
                    Intent.createChooser(intent, "选择图片"),
                    REQUEST_CODE_FILE_CHOOSER
                )
                return true
            } catch (e: Exception) {
                return false
            }
        }
    }




    private fun checkPermissions() {
        val permissions = arrayOf(
            Manifest.permission.READ_EXTERNAL_STORAGE,
            Manifest.permission.CAMERA
        )

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val ungranted = permissions.filter {
                checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED
            }
            if (ungranted.isNotEmpty()) {
                requestPermissions(ungranted.toTypedArray(), 1)
            }
        }
    }


    // 在Activity的onDestroy中
    override fun onDestroy() {
//        FileChooserHelper.clearCallbacks()
        super.onDestroy()
    }

    // 处理Activity结果
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == 100) { // 与WebChromeClient中的REQUEST_CODE_FILE_CHOOSER一致
            FileChooserHelper.onActivityResult(resultCode, data)
        }
    }

    // 在Activity中添加
    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        if (useSystemWebView) {
            sysWebView?.saveState(outState)
        } else {
            x5WebView?.saveState(outState)
        }
    }

    override fun onRestoreInstanceState(savedInstanceState: Bundle) {
        super.onRestoreInstanceState(savedInstanceState)
        if (useSystemWebView) {
            sysWebView?.restoreState(savedInstanceState)
        } else {
            x5WebView?.restoreState(savedInstanceState)
        }
    }

    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        if (keyCode == KeyEvent.KEYCODE_BACK) {
            if (useSystemWebView && sysWebView?.canGoBack() == true) {
                sysWebView?.goBack()
                return true
            }
            if (!useSystemWebView && x5WebView?.canGoBack() == true) {
                x5WebView?.goBack()
                return true
            }
        }
        return super.onKeyDown(keyCode, event)
    }

}

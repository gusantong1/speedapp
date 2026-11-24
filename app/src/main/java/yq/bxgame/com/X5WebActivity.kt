package yq.bxgame.com

import android.Manifest
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
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import com.tencent.smtt.export.external.interfaces.WebResourceRequest
import com.tencent.smtt.sdk.QbSdk
import com.tencent.smtt.sdk.ValueCallback
import com.tencent.smtt.sdk.WebChromeClient
import com.tencent.smtt.sdk.WebView
import com.tencent.smtt.sdk.WebViewClient
import org.json.JSONObject
import yq.bxgame.com.R


class X5WebActivity : AppCompatActivity() {
    private var x5WebView: WebView? = null
    private var sysWebView: android.webkit.WebView? = null
    private var useSystemWebView: Boolean = false

    

    private lateinit var contentManager: ContentManager
    private lateinit var contentSession: ContentSession
    private var pageLoaded = false
    private var startupProgress: android.widget.ProgressBar? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.statusBarColor = Color.BLACK
        enterImmersiveMode()
        try {
            contentManager = ContentManager.get(this)
            contentSession = contentManager.openSession(savedInstanceState?.getString("pack_session_id"))
        } catch (e: Exception) {
            android.widget.Toast.makeText(this, "应用资源初始化失败，请重新安装有效 APK", android.widget.Toast.LENGTH_LONG).show()
            finish()
            return
        }

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

        startupProgress = android.widget.ProgressBar(this).also {
            addContentView(it, android.widget.FrameLayout.LayoutParams(android.view.ViewGroup.LayoutParams.WRAP_CONTENT, android.view.ViewGroup.LayoutParams.WRAP_CONTENT, android.view.Gravity.CENTER))
        }
        checkPermissions()
        contentManager.startSession(contentSession) {
            runOnUiThread {
                if (!isFinishing && !isDestroyed && !pageLoaded) {
                    pageLoaded = true
                    startupProgress?.visibility = View.GONE
                    loadContent()
                }
            }
        }
    }

    private fun loadContent() {
        val url = contentManager.pageUrl(contentSession)
        if (useSystemWebView) sysWebView?.loadUrl(url) else x5WebView?.loadUrl(url)
    }

    private fun injectedConfig(): String {
        val deviceId = Settings.Secure.getString(contentResolver, Settings.Secure.ANDROID_ID) ?: ""
        return "window.deviceId = ${JSONObject.quote(deviceId)}; " +
            "window.PackAppConfig = JSON.parse(${JSONObject.quote(contentManager.publicConfig(contentSession))});"
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus) enterImmersiveMode()
    }

    private fun enterImmersiveMode() {
        // 隐藏系统栏，同时让 adjustResize 在键盘弹出时缩小 WebView。
        WindowCompat.getInsetsController(window, window.decorView).apply {
            isAppearanceLightStatusBars = false
            systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            hide(WindowInsetsCompat.Type.systemBars())
        }
    }

    class WebAppInterface(private val context: Context, private val manager: ContentManager, private val session: ContentSession) {
        @JavascriptInterface
        fun getSpeedAppUid(): String = Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID) ?: ""

        @JavascriptInterface
        fun getPackConfig(): String = manager.publicConfig(session)

        // Cocos 启动时同步读取引导域名。
        @JavascriptInterface
        fun getdomin(): String = manager.startupDomain
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
            webSettings.setMixedContentMode(if (contentManager.allowHttp) WebSettings.MIXED_CONTENT_ALWAYS_ALLOW else WebSettings.MIXED_CONTENT_NEVER_ALLOW)
        }




        // 启用必要的WebView设置
        webSettings.setAllowFileAccess(false);
        webSettings.setAllowContentAccess(false);
        webSettings.setAllowFileAccessFromFileURLs(false);
        webSettings.setAllowUniversalAccessFromFileURLs(false);
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

                x5WebView?.evaluateJavascript(injectedConfig(), null)
            }

            override fun shouldInterceptRequest(view: WebView?, request: WebResourceRequest?): com.tencent.smtt.export.external.interfaces.WebResourceResponse? {
                val resource = contentManager.intercept(request?.url?.toString() ?: return null) ?: return null
                return com.tencent.smtt.export.external.interfaces.WebResourceResponse(resource.mime, resource.encoding, resource.status, resource.reason, resource.headers, resource.stream)
            }

            override fun shouldOverrideUrlLoading(view: WebView?, request: WebResourceRequest?): Boolean {
                val scheme = request?.url?.scheme
                return scheme != "https" && !(contentManager.allowHttp && scheme == "http")
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


        x5WebView!!.addJavascriptInterface(WebAppInterface(this@X5WebActivity, contentManager, contentSession), "Android")

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
            settings.mixedContentMode = if (contentManager.allowHttp) android.webkit.WebSettings.MIXED_CONTENT_ALWAYS_ALLOW else android.webkit.WebSettings.MIXED_CONTENT_NEVER_ALLOW
        }
        settings.allowFileAccess = false
        settings.allowContentAccess = false
        settings.allowFileAccessFromFileURLs = false
        settings.allowUniversalAccessFromFileURLs = false
        settings.setSupportZoom(true)
        settings.builtInZoomControls = true
        settings.displayZoomControls = false
        settings.useWideViewPort = true
        settings.loadWithOverviewMode = true

        sysWebView!!.webViewClient = object : android.webkit.WebViewClient() {
            override fun onPageFinished(view: android.webkit.WebView, url: String) {
                super.onPageFinished(view, url)
                sysWebView?.evaluateJavascript(injectedConfig(), null)
            }

            override fun shouldInterceptRequest(view: android.webkit.WebView?, request: android.webkit.WebResourceRequest?): android.webkit.WebResourceResponse? {
                val resource = contentManager.intercept(request?.url?.toString() ?: return null) ?: return null
                return android.webkit.WebResourceResponse(resource.mime, resource.encoding, resource.status, resource.reason, resource.headers, resource.stream)
            }

            override fun shouldOverrideUrlLoading(view: android.webkit.WebView?, request: android.webkit.WebResourceRequest?): Boolean {
                val scheme = request?.url?.scheme
                return scheme != "https" && !(contentManager.allowHttp && scheme == "http")
            }
        }
        sysWebView!!.webChromeClient = android.webkit.WebChromeClient()
        sysWebView!!.addJavascriptInterface(WebAppInterface(this@X5WebActivity, contentManager, contentSession), "Android")
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
        if (isFinishing && ::contentSession.isInitialized) contentManager.releaseSession(contentSession)
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
        if (::contentSession.isInitialized) outState.putString("pack_session_id", contentSession.id)
        super.onSaveInstanceState(outState)
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

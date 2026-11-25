package yq.bxgame.com

import android.content.Context
import android.content.res.AssetManager
import android.net.Uri
import android.util.Log
import android.webkit.MimeTypeMap
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.io.File
import java.io.FileInputStream
import java.io.InputStream
import java.io.SequenceInputStream
import java.util.concurrent.Executors

/** X5 与系统 WebView 共用同一套资源及更新状态。 */
class ContentManager private constructor(private val context: Context) {
    private val config = context.assets.open("pack-config.json").bufferedReader().use { JSONObject(it.readText()) }
    private val download = config.getJSONObject("download")
    private val options = BundleUpdater.Options().apply {
        allowHttp = download.getBoolean("allowHttp")
        timeoutSec = download.getInt("timeoutSec")
        maxZipBytes = download.getLong("maxZipBytes")
        maxExpandedBytes = download.getLong("maxExpandedBytes")
        maxFileBytes = download.getLong("maxFileBytes")
        maxEntries = download.getInt("maxEntries")
        maxCompressionRatio = download.getLong("maxCompressionRatio")
        download.optJSONArray("allowedOrigins")?.let { a -> for (i in 0 until a.length()) allowedOrigins.add(a.getString(i)) }
        download.optJSONArray("allowedPrivateCidrs")?.let { a -> for (i in 0 until a.length()) allowedPrivateCidrs.add(a.getString(i)) }
    }
    @Volatile private var updater: BundleUpdater? = null
    private val executor = Executors.newSingleThreadExecutor()
    private val sessions = mutableMapOf<String, ContentSession>()
    val allowHttp: Boolean get() = options.allowHttp
    val isLocal: Boolean get() = config.getString("url").isEmpty()
    private val firstBackendOrigin: String get() = config.optJSONArray("dominList")?.optString(0).orEmpty()
    val startupDomain: String get() = config.optString("getdomin", firstBackendOrigin)
    val gameApiBaseUrl: String get() {
        val origin = firstBackendOrigin
        return if (origin.isEmpty()) "" else "$origin/api/game/v1"
    }

    @Synchronized
    fun openSession(restoredId: String?): ContentSession = sessions[restoredId] ?: ContentSession().also { sessions[it.id] = it }

    @Synchronized
    fun releaseSession(session: ContentSession) { sessions.remove(session.id) }

    fun pageUrl(session: ContentSession): String = if (isLocal) "$ORIGIN/html/${requireNotNull(session.selected()).id}/index.html" else config.getString("url")

    fun publicConfig(session: ContentSession): String = JSONObject().apply {
        put("url", config.getString("url"))
        put("dominList", config.getJSONArray("dominList"))
        put("GAME_API_BASE_URL", gameApiBaseUrl)
        put("version", session.selected()?.version ?: config.getString("version"))
        put("last_version_url", config.getString("last_version_url"))
    }.toString()

    fun startSession(session: ContentSession, onReady: () -> Unit) {
        val available = session.selected() ?: updater?.current()
        if (available != null) {
            session.selectOnce(available)
            onReady()
            executor.execute { checkForNextSession(session, updater) }
            return
        }
        executor.execute {
            var core = updater
            try {
                core = core ?: BundleUpdater(File(context.filesDir, "web-content/${config.getString("buildId")}"), config.getString("version"), options)
                updater = core
                session.selectOnce(core.current())
            } catch (e: Exception) {
                session.selectOnce(BundleUpdater.State(BundleUpdater.BUILTIN, config.getString("version")))
                Log.w("PackUpdate", "Resource restore failed; bundled resources selected")
            }
            // 首次页面只加载一次；后续网络响应只准备下一次新会话的资源。
            onReady()
            checkForNextSession(session, core)
        }
    }

    private fun checkForNextSession(session: ContentSession, core: BundleUpdater?) {
            if (!session.beginStartupCheck() || core == null) return
            try {
                val a = config.getJSONArray("versionEndpoints")
                val endpoints = (0 until a.length()).map { a.getString(it) }
                core.checkAndUpdate(endpoints) { text ->
                    val json = JSONObject(text)
                    BundleUpdater.VersionInfo(json.getString("version"), json.optString("last_version_url"), json.optString("sha256"))
                }
            } catch (e: Exception) {
                // 不输出含签名查询参数的下载 URL；失败继续读取旧目录/内置资源。
                Log.w("PackUpdate", "Update failed; current resources retained (${e.javaClass.simpleName})")
            }
    }

    data class Resource(val mime: String, val encoding: String?, val status: Int, val reason: String, val stream: InputStream) {
        val headers: Map<String, String> = mapOf("Cache-Control" to "no-store", "X-Content-Type-Options" to "nosniff")
    }

    fun intercept(raw: String): Resource? {
        val uri = Uri.parse(raw)
        if (uri.host != HOST) return null
        try {
            require(uri.scheme == "https" && (uri.port == -1 || uri.port == 443))
            val parts = uri.pathSegments
            require(parts.size >= 3 && parts[0] == "html")
            val generation = parts[1]
            val path = parts.drop(2).joinToString("/")
            require(path.isNotEmpty() && path.length <= 1024)
            require(!path.contains('\\') && !path.contains(':') && !path.contains('\u0000'))
            require(path.split('/').all { it.isNotEmpty() && it != "." && it != ".." && !it.endsWith('.') && !it.endsWith(' ') })
            val input = if (generation == BundleUpdater.BUILTIN) {
                context.assets.open("html/$path", AssetManager.ACCESS_STREAMING)
            } else {
                FileInputStream(requireNotNull(updater).resource(generation, path))
            }
            val extension = path.substringAfterLast('.', "").lowercase()
            val mime = when (extension) {
                "js", "mjs" -> "application/javascript"
                "wasm" -> "application/wasm"
                "json" -> "application/json"
                "html", "htm" -> "text/html"
                "css" -> "text/css"
                else -> MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension) ?: "application/octet-stream"
            }
            val encoding = if (mime.startsWith("text/") || mime in listOf("application/javascript", "application/json", "image/svg+xml")) "UTF-8" else null
            val stream = if (isLocal && path == "index.html" && gameApiBaseUrl.isNotEmpty()) {
                withStartupBridge(input)
            } else {
                input
            }
            return Resource(mime, encoding, 200, "OK", stream)
        } catch (_: Exception) {
            // 本地资源未命中必须返回 404，不能转成外网请求。
            return Resource("text/plain", "UTF-8", 404, "Not Found", ByteArrayInputStream("Resource unavailable".toByteArray()))
        }
    }

    // 在入口脚本运行前提供桥接配置，原始 HTML 和资源校验数据保持完整。
    private fun withStartupBridge(input: InputStream): InputStream {
        try {
            val prefix = ByteArray(8192)
            var size = 0
            while (size < prefix.size) {
                val count = input.read(prefix, size, prefix.size - size)
                if (count <= 0) break
                size += count
            }
            val header = String(prefix, 0, size, Charsets.ISO_8859_1)
            var offset = if (header.startsWith("\u00ef\u00bb\u00bf")) 3 else 0
            while (true) {
                val match = htmlEntryToken.matchAt(header, offset) ?: break
                offset = match.range.last + 1
                if (match.value.startsWith("<head", ignoreCase = true)) break
            }
            val initial = prefix.copyOfRange(0, offset) + startupBridgeScript + prefix.copyOfRange(offset, size)
            return SequenceInputStream(ByteArrayInputStream(initial), input)
        } catch (e: Exception) {
            input.close()
            throw e
        }
    }

    companion object {
        private val htmlEntryToken = Regex(
            """[ \t\r\n\f]+|<!--.*?-->|<(?:!doctype|html|head)(?=[\s>])(?:[^>"']++|"[^"]*"|'[^']*')*+>""",
            setOf(RegexOption.IGNORE_CASE, RegexOption.DOT_MATCHES_ALL)
        )
        private val startupBridgeScript = ("<script>window.getdomin=window.Android.getdomin();" +
            "window.GAME_API_BASE_URL=window.Android.getGameApiBaseUrl();</script>").toByteArray(Charsets.UTF_8)

        const val HOST = "appassets.androidplatform.net"
        const val ORIGIN = "https://appassets.androidplatform.net"
        @Volatile private var instance: ContentManager? = null
        fun get(context: Context): ContentManager = instance ?: synchronized(this) {
            instance ?: ContentManager(context.applicationContext).also { instance = it }
        }
    }
}

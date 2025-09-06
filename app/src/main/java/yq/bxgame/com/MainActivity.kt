package yq.bxgame.com

import android.content.Intent
import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity
import androidx.navigation.ui.AppBarConfiguration
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import yq.bxgame.com.X5WebActivity
import yq.bxgame.dev.R


class MainActivity : AppCompatActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
            val intent = Intent(this, X5WebActivity::class.java)
            intent.putExtra("url", getString(R.string.app_site))
            startActivity(intent)

    }
}
package vn.trcf.bridge.app

import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.WebView
import android.webkit.WebViewClient

/**
 * Màn bán hàng: POS web của TRCF (`/pos-app`) toàn màn hình.
 *
 * Chưa ghép thì chuyển sang màn thiết lập. Đã ghép thì bảo đảm dịch vụ in đang chạy
 * — mở app là đủ, không có nút "bật in" nào để quên bấm.
 */
class MainActivity : Activity() {
    private lateinit var web: WebView
    private lateinit var display: CustomerDisplay

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (!App.isPaired(this)) {
            startActivity(Intent(this, SetupActivity::class.java))
            finish()
            return
        }
        App.startBridge(this)

        display = CustomerDisplay(this) { App.serverUrl(this) }
        display.attach()

        CookieManager.getInstance().setAcceptCookie(true)
        web = WebView(this).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.mediaPlaybackRequiresUserGesture = false
            webViewClient = WebViewClient() // mọi trang mở ngay trong app, không bật trình duyệt
            addJavascriptInterface(Bridge(), "AndroidBridge")
        }
        setContentView(web)
        immersive()

        if (savedInstanceState != null) web.restoreState(savedInstanceState)
        else web.loadUrl(App.serverUrl(this).trimEnd('/') + "/pos-app")
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // Vừa cắm máy in (USB_DEVICE_ATTACHED): quyền đã có, lõi tự thấy máy ở lần in kế.
        App.startBridge(this)
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        if (::web.isInitialized) web.saveState(outState)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus) immersive()
    }

    @Deprecated("Activity gốc — không dùng androidx")
    override fun onBackPressed() {
        if (::web.isInitialized && web.canGoBack()) web.goBack()
        else @Suppress("DEPRECATION") super.onBackPressed()
    }

    override fun onDestroy() {
        if (::display.isInitialized) display.detach()
        if (::web.isInitialized) web.destroy()
        super.onDestroy()
    }

    private fun immersive() {
        if (Build.VERSION.SDK_INT >= 30) {
            window.insetsController?.let {
                it.hide(WindowInsets.Type.systemBars())
                it.systemBarsBehavior = WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            }
        } else {
            @Suppress("DEPRECATION")
            window.decorView.systemUiVisibility = (View.SYSTEM_UI_FLAG_FULLSCREEN
                or View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                or View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY)
        }
    }

    /**
     * `window.AndroidBridge` cho POS web (xem `lib/android-bridge.ts` bên frontend).
     * Chạy trên luồng JavaBridge — mọi việc giao diện đẩy về luồng chính.
     */
    private inner class Bridge {
        @JavascriptInterface
        fun openCustomerDisplay(posId: String) {
            if (posId.isNotBlank()) display.open(posId)
        }

        @JavascriptInterface
        fun closeCustomerDisplay() {
            display.close()
        }

        @JavascriptInterface
        fun isAndroidApp(): Boolean = true

        /** Mở màn thiết lập (xem máy in, nhật ký, ghép thêm máy in). */
        @JavascriptInterface
        fun openSetup() {
            runOnUiThread { startActivity(Intent(this@MainActivity, SetupActivity::class.java)) }
        }
    }
}

package vn.trcf.bridge.app

import android.annotation.SuppressLint
import android.app.Activity
import android.app.Presentation
import android.content.Context
import android.hardware.display.DisplayManager
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.Display
import android.webkit.WebView
import android.webkit.WebViewClient

/**
 * Màn khách (045) trên màn PHỤ của máy POS — Android Presentation API.
 *
 * Trang `/customer-display/{pos_id}` là kênh CHỈ-ĐỌC cần đăng nhập. WebView ở đây dùng
 * chung `CookieManager` với WebView màn bán hàng (một tiến trình, một kho cookie), nên
 * nhân viên đăng nhập POS là màn khách cũng có phiên — không đăng nhập lần hai.
 *
 * Quầy nào đang mở ca do POS web báo qua `AndroidBridge.openCustomerDisplay`; máy không
 * khai quầy ở đâu cả. Không có màn phụ thì mọi lệnh ở đây là không làm gì.
 */
class CustomerDisplay(private val activity: Activity, private val baseUrl: () -> String) {
    private val dm = activity.getSystemService(Context.DISPLAY_SERVICE) as DisplayManager
    private val ui = Handler(Looper.getMainLooper())
    private var presentation: Screen? = null
    private var posId: String? = null

    private val listener = object : DisplayManager.DisplayListener {
        override fun onDisplayAdded(displayId: Int) = sync()
        override fun onDisplayRemoved(displayId: Int) = sync()
        override fun onDisplayChanged(displayId: Int) {}
    }

    fun attach() = dm.registerDisplayListener(listener, ui)

    fun detach() {
        dm.unregisterDisplayListener(listener)
        dismiss()
    }

    /** POS web báo quầy `id` đang mở ca. Gọi lại với cùng quầy thì không nạp lại trang. */
    fun open(id: String) = ui.post {
        posId = id
        sync()
    }

    fun close() = ui.post {
        posId = null
        sync()
    }

    /** Đưa màn phụ về đúng trạng thái: có quầy + có màn phụ → hiện; còn lại → tắt. */
    private fun sync() {
        val id = posId
        val display = dm.getDisplays(DisplayManager.DISPLAY_CATEGORY_PRESENTATION).firstOrNull()
        if (id == null || display == null || activity.isFinishing) {
            dismiss()
            return
        }
        val url = baseUrl().trimEnd('/') + "/customer-display/" + id
        val cur = presentation
        if (cur != null && cur.display.displayId == display.displayId && cur.url == url && cur.isShowing) return
        dismiss()
        try {
            presentation = Screen(activity, display, url).also { it.show() }
        } catch (e: Exception) {
            // Màn phụ vừa rút ra giữa chừng (WindowManager.InvalidDisplayException).
            Log.w(App.TAG, "không mở được màn khách: ${e.message}")
            presentation = null
        }
    }

    private fun dismiss() {
        presentation?.let { it.webView.destroy(); it.dismiss() }
        presentation = null
    }

    private class Screen(ctx: Context, display: Display, val url: String) : Presentation(ctx, display) {
        lateinit var webView: WebView

        @SuppressLint("SetJavaScriptEnabled")
        override fun onCreate(savedInstanceState: Bundle?) {
            super.onCreate(savedInstanceState)
            webView = WebView(context).apply {
                settings.javaScriptEnabled = true
                settings.domStorageEnabled = true
                settings.mediaPlaybackRequiresUserGesture = false // slideshow quảng cáo tự chạy
                webViewClient = WebViewClient()
                loadUrl(this@Screen.url)
            }
            setContentView(webView)
        }
    }
}

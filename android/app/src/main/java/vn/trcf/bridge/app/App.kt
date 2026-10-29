package vn.trcf.bridge.app

import android.app.Application
import android.content.Context
import android.content.Intent
import android.os.Build
import android.util.Log
import vn.trcf.bridge.trcfcore.Logger
import vn.trcf.bridge.trcfcore.Trcfcore

/**
 * Nối lõi Go với Android đúng MỘT lần cho cả tiến trình: cắm đường USB và nhật ký.
 * Mọi nơi khác (dịch vụ nền, màn thiết lập) chỉ gọi thẳng `Trcfcore`.
 */
class App : Application() {
    override fun onCreate() {
        super.onCreate()
        val tz = java.util.TimeZone.getDefault()
        Trcfcore.setTimezone(tz.id, tz.getOffset(System.currentTimeMillis()) / 1000L)
        Trcfcore.setUsbHost(UsbPrinterHost(this))
        Trcfcore.setLogger(LogBuffer)
    }

    companion object {
        const val TAG = "trcf-bridge"

        /** Nơi lõi lưu token + bản gán máy in. Bộ nhớ riêng của app, chỉ app đọc được. */
        fun configPath(ctx: Context): String = ctx.filesDir.resolve("config.json").absolutePath

        fun version(): String = "android-${BuildConfig.VERSION_NAME}"

        /** Tên máy quầy hiện ở màn Máy in trên ERP. */
        fun hostname(): String = "${Build.MANUFACTURER} ${Build.MODEL}".trim()

        fun isPaired(ctx: Context): Boolean = Trcfcore.isPaired(configPath(ctx))

        fun serverUrl(ctx: Context): String = Trcfcore.serverURL(configPath(ctx))

        /** Bật dịch vụ in nền (không làm gì nếu chưa ghép). */
        fun startBridge(ctx: Context) {
            if (!isPaired(ctx)) return
            val i = Intent(ctx, BridgeService::class.java)
            if (Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(i) else ctx.startService(i)
        }
    }
}

/** Nhật ký lõi: đẩy sang Logcat và giữ 200 dòng cuối cho màn trạng thái. */
object LogBuffer : Logger {
    private val lines = ArrayDeque<String>()

    override fun log(line: String) {
        Log.i(App.TAG, line)
        synchronized(lines) {
            lines.addLast(line)
            while (lines.size > 200) lines.removeFirst()
        }
    }

    fun snapshot(): List<String> = synchronized(lines) { lines.toList() }
}

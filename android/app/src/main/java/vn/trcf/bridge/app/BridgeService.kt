package vn.trcf.bridge.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import vn.trcf.bridge.trcfcore.Trcfcore

/**
 * Dịch vụ in chạy nền suốt ngày — vai của systemd bên Linux.
 *
 * Foreground Service (có thông báo thường trực) để Android không diệt khi tiết kiệm
 * pin. Toàn bộ việc lấy lệnh in / in / báo kết quả là lõi Go (`Trcfcore.start`).
 */
class BridgeService : Service() {
    private var wakeLock: PowerManager.WakeLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startInForeground()
        try {
            Trcfcore.start(App.configPath(this), App.version())
        } catch (e: Exception) {
            // Chưa ghép / cấu hình hỏng: không có gì để chạy.
            Log.w(App.TAG, "không khởi động được lõi in: ${e.message}")
            stopSelf()
            return START_NOT_STICKY
        }
        // Giữ CPU thức để long-poll không ngủ quên khi màn hình tắt — máy POS cắm điện
        // cả ngày nên cái giá pin là không đáng kể, còn lỡ một bill thì đáng kể.
        if (wakeLock == null) {
            wakeLock = (getSystemService(Context.POWER_SERVICE) as PowerManager)
                .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "trcf:bridge")
                .apply { setReferenceCounted(false); acquire() }
        }
        return START_STICKY
    }

    override fun onDestroy() {
        // Chỉ báo dừng, KHÔNG chờ: không được chặn luồng giao diện. Lõi tự in nốt lệnh
        // đang dở trước khi thoát.
        Trcfcore.stop(0)
        wakeLock?.let { if (it.isHeld) it.release() }
        wakeLock = null
        super.onDestroy()
    }

    private fun startInForeground() {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, "Dịch vụ in", NotificationManager.IMPORTANCE_LOW),
            )
        }
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, SetupActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val builder = if (Build.VERSION.SDK_INT >= 26) Notification.Builder(this, CHANNEL) else Notification.Builder(this)
        val n = builder
            .setSmallIcon(R.drawable.ic_print)
            .setContentTitle("TRCF — đang sẵn sàng in")
            .setContentText("Chạm để xem máy in và nhật ký")
            .setContentIntent(open)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(NOTIFY_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
        } else {
            startForeground(NOTIFY_ID, n)
        }
    }

    companion object {
        private const val CHANNEL = "bridge"
        private const val NOTIFY_ID = 1
    }
}

/** Bật lại dịch vụ in sau khi khởi động máy hoặc cập nhật app — không ai phải chạm vào. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> App.startBridge(context)
        }
    }
}

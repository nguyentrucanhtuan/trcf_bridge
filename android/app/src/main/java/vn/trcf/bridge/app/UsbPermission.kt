package vn.trcf.bridge.app

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.hardware.usb.UsbDevice
import android.hardware.usb.UsbManager
import android.os.Build

/**
 * Xin quyền USB cho mọi máy in đang cắm mà chưa có quyền, lần lượt từng chiếc, rồi gọi
 * `done` khi hỏi xong.
 *
 * Đường chính để quyền được NHỚ là hộp thoại "mở bằng TRCF POS" khi cắm máy in (tích
 * "Luôn dùng"). Đường này là dự phòng cho máy in đã cắm sẵn trước khi cài app.
 */
object UsbPermission {
    private const val ACTION = "vn.trcf.bridge.USB_PERMISSION"

    fun requestAll(ctx: Context, done: () -> Unit) {
        val usb = ctx.getSystemService(Context.USB_SERVICE) as UsbManager
        val pending = UsbPrinterHost(ctx).printers().filter { !usb.hasPermission(it) }.toMutableList()
        next(ctx.applicationContext, usb, pending, done)
    }

    private fun next(ctx: Context, usb: UsbManager, pending: MutableList<UsbDevice>, done: () -> Unit) {
        val dev = pending.removeFirstOrNull() ?: return done()
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(c: Context, i: Intent) {
                ctx.unregisterReceiver(this)
                next(ctx, usb, pending, done)
            }
        }
        val filter = IntentFilter(ACTION)
        if (Build.VERSION.SDK_INT >= 33) {
            ctx.registerReceiver(receiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            @Suppress("UnspecifiedRegisterReceiverFlag")
            ctx.registerReceiver(receiver, filter)
        }
        // MUTABLE bắt buộc: hệ thống điền kết quả (thiết bị + cho/không cho) vào intent.
        val flags = if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0
        val pi = PendingIntent.getBroadcast(ctx, 0, Intent(ACTION).setPackage(ctx.packageName), flags)
        usb.requestPermission(dev, pi)
    }
}

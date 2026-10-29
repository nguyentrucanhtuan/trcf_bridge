package vn.trcf.bridge.app

import android.app.Activity
import android.content.Intent
import android.hardware.usb.UsbManager
import android.os.Bundle

/**
 * Cửa vào của app, KHÔNG có giao diện: biểu tượng ngoài màn hình chính + tín hiệu cắm
 * máy in (`USB_DEVICE_ATTACHED`).
 *
 * App chỉ làm cầu nối in — màn bán hàng mở bằng trình duyệt như mọi máy khác. Bản đầu
 * nhúng `/pos-app` vào WebView ở đây, nhưng ERP trên cloud thường tách domain backend
 * với frontend: địa chỉ nhập để ghép là backend, nên WebView mở ra trang trắng, và vì
 * Activity này nhận cả tín hiệu cắm máy in, trang trắng còn tự bật lên che màn đang
 * dùng mỗi lần cắm máy in hay khởi động máy.
 *
 * Nên giờ: bảo đảm dịch vụ in đang chạy, rồi bấm biểu tượng thì mở màn thiết lập, còn
 * cắm máy in thì đóng lặng.
 */
class MainActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Vừa cắm máy in: quyền USB đã có, lõi tự thấy máy ở lần in kế.
        App.startBridge(this)
        if (intent?.action != UsbManager.ACTION_USB_DEVICE_ATTACHED) {
            startActivity(Intent(this, SetupActivity::class.java))
        }
        // Theme.NoDisplay đòi finish() TRƯỚC onResume, không thì app ném lỗi.
        finish()
    }
}

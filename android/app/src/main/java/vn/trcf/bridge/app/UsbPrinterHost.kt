package vn.trcf.bridge.app

import android.content.Context
import android.hardware.usb.UsbConstants
import android.hardware.usb.UsbDevice
import android.hardware.usb.UsbDeviceConnection
import android.hardware.usb.UsbEndpoint
import android.hardware.usb.UsbInterface
import android.hardware.usb.UsbManager
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap
import vn.trcf.bridge.trcfcore.UsbConn
import vn.trcf.bridge.trcfcore.UsbHost

/**
 * Đường USB của Android cho lõi Go — tương đương `/dev/usb/lp*` + sysfs bên Linux.
 *
 * Chỉ làm hai việc: liệt kê máy in (kèm danh tính) và mở một luồng ghi CHẶN. Phân
 * giải danh tính trước mỗi lần in, chờ 15s + 45s, cờ "có thể đã in" đều nằm ở lõi Go
 * dùng chung — KHÔNG viết lại ở đây.
 */
class UsbPrinterHost(ctx: Context) : UsbHost {
    private val usb = ctx.getSystemService(Context.USB_SERVICE) as UsbManager

    /** Chuỗi IEEE 1284 theo `deviceName`. `deviceName` đổi mỗi lần rút cắm nên khoá này
     *  tự hết hạn đúng lúc; đọc lại mỗi lần in thì phải mở thiết bị mỗi lần dò. */
    private val deviceIdCache = ConcurrentHashMap<String, String>()

    override fun listJSON(): String {
        val arr = JSONArray()
        for (dev in printers()) {
            if (!usb.hasPermission(dev)) continue // chưa cấp quyền: không đọc được serial, không mở được
            val iface = printerInterface(dev) ?: continue
            val info = parse1284(deviceIdOf(dev, iface))
            arr.put(JSONObject().apply {
                put("path", dev.deviceName)
                put("mfg", info["MFG"] ?: dev.manufacturerName.orEmpty())
                put("model", info["MDL"] ?: dev.productName.orEmpty())
                put("cmd", info["CMD"].orEmpty())
                put("serial", serialOf(dev) ?: info["SN"].orEmpty())
                put("ids", "%04x:%04x".format(dev.vendorId, dev.productId))
                // Android không có API công khai cho vị trí cổng USB (đường dẫn
                // /dev/bus/usb/… đổi mỗi lần cắm). Hằng số này khiến máy KHÔNG khai serial
                // chỉ khớp theo mã thiết bị: có hai chiếc cùng model cùng không serial thì
                // lõi từ chối in (báo "nhiều máy cùng khớp") chứ không đoán bừa.
                put("usb_path", "android")
            })
        }
        return arr.toString()
    }

    override fun open(path: String): UsbConn {
        val dev = usb.deviceList[path]
            ?: throw IOException("không thấy máy in tại $path — kiểm tra dây USB và nguồn máy in")
        if (!usb.hasPermission(dev)) {
            throw IOException("chưa cấp quyền USB cho máy in ${dev.productName ?: path} — mở app TRCF POS, bấm Tìm máy in rồi chọn Cho phép")
        }
        val iface = printerInterface(dev)
            ?: throw IOException("thiết bị $path không phải máy in USB")
        val out = bulkOut(iface) ?: throw IOException("máy in $path không có cổng ghi")
        val conn = usb.openDevice(dev)
            ?: throw IOException("máy in ${dev.productName ?: path} đang TẮT hoặc đã rớt kết nối USB — bật nguồn máy in lên, hoặc rút cắm lại dây USB")
        if (!conn.claimInterface(iface, true)) {
            conn.close()
            throw IOException("máy in $path đang bị ứng dụng khác giữ")
        }
        return Writer(conn, iface, out)
    }

    /** Thiết bị có ít nhất một interface lớp máy in. */
    fun printers(): List<UsbDevice> = usb.deviceList.values.filter { printerInterface(it) != null }

    private fun printerInterface(dev: UsbDevice): UsbInterface? {
        for (i in 0 until dev.interfaceCount) {
            val it = dev.getInterface(i)
            if (it.interfaceClass == UsbConstants.USB_CLASS_PRINTER && bulkOut(it) != null) return it
        }
        return null
    }

    private fun bulkOut(iface: UsbInterface): UsbEndpoint? {
        for (i in 0 until iface.endpointCount) {
            val ep = iface.getEndpoint(i)
            if (ep.type == UsbConstants.USB_ENDPOINT_XFER_BULK && ep.direction == UsbConstants.USB_DIR_OUT) return ep
        }
        return null
    }

    private fun serialOf(dev: UsbDevice): String? =
        try {
            dev.serialNumber?.trim()?.takeIf { it.isNotEmpty() }
        } catch (_: SecurityException) {
            null
        }

    /**
     * Đọc chuỗi tự khai IEEE 1284 (`MFG:…;MDL:…;CMD:ESC/POS;`) bằng yêu cầu GET_DEVICE_ID
     * của lớp máy in. Đây là nguồn đáng tin nhất về NGÔN NGỮ LỆNH — ERP dựa vào nó để
     * chặn gán máy tem cho vai in bill. Đọc hỏng thì trả rỗng: máy vẫn dùng được, chỉ
     * mất lớp chặn đó (ERP cho gán kèm cảnh báo).
     */
    private fun deviceIdOf(dev: UsbDevice, iface: UsbInterface): String {
        deviceIdCache[dev.deviceName]?.let { return it }
        val conn: UsbDeviceConnection = usb.openDevice(dev) ?: return ""
        try {
            val buf = ByteArray(1024)
            val n = conn.controlTransfer(
                0xA1, 0, 0, (iface.id shl 8) or iface.alternateSetting, buf, buf.size, 2000,
            )
            if (n < 2) return ""
            val len = ((buf[0].toInt() and 0xff) shl 8) or (buf[1].toInt() and 0xff)
            val end = minOf(maxOf(len, 2), n)
            val id = String(buf, 2, end - 2, Charsets.ISO_8859_1)
            deviceIdCache[dev.deviceName] = id
            return id
        } finally {
            conn.close()
        }
    }

    private fun parse1284(raw: String): Map<String, String> {
        val out = HashMap<String, String>()
        for (part in raw.split(';')) {
            val k = part.substringBefore(':', "").trim().uppercase()
            val v = part.substringAfter(':', "").replace("\u0000", "").trim()
            if (k.isEmpty() || v.isEmpty()) continue
            when (k) {
                "MFG", "MANUFACTURER" -> out["MFG"] = v
                "MDL", "MODEL" -> out["MDL"] = v
                "CMD", "COMMAND SET", "COMMANDSET" -> out["CMD"] = v
                "SN", "SERN", "SERIALNUMBER" -> out["SN"] = v
            }
        }
        return out
    }

    /**
     * Luồng ghi CHẶN: `bulkTransfer` chỉ trả về khi dữ liệu đã đi qua dây.
     *
     * Hợp đồng với lõi (xem `UsbConn` bên Go): CHỈ ném lỗi khi lần gọi này chưa đẩy được
     * byte nào. Đã đẩy được một phần thì TRẢ VỀ số đó — ném ở đây là con số mất, lõi
     * tưởng "chưa chạm giấy" và ERP in lại tờ thứ hai.
     */
    private class Writer(
        private val conn: UsbDeviceConnection,
        private val iface: UsbInterface,
        private val ep: UsbEndpoint,
    ) : UsbConn {
        override fun write(b: ByteArray): Long {
            var sent = 0
            while (sent < b.size) {
                val len = minOf(CHUNK, b.size - sent)
                val n = conn.bulkTransfer(ep, b, sent, len, TIMEOUT_MS)
                if (n <= 0) {
                    if (sent == 0) {
                        throw IOException("ghi ra máy in không được (${b.size} byte) — kiểm tra giấy, nắp máy và dây USB")
                    }
                    return sent.toLong()
                }
                sent += n
            }
            return sent.toLong()
        }

        override fun close() {
            try {
                conn.releaseInterface(iface)
            } finally {
                conn.close()
            }
        }

        companion object {
            const val CHUNK = 16 * 1024

            // Trần cho MỘT khúc 16 KB. Lõi có mốc 15s + 45s cho cả lệnh in; khúc nào
            // quá 30 giây là máy in đã treo thật.
            const val TIMEOUT_MS = 30_000
        }
    }
}

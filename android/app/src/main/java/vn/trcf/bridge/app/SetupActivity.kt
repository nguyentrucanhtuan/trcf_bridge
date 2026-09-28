package vn.trcf.bridge.app

import android.app.Activity
import android.content.Intent
import android.graphics.Typeface
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Spinner
import android.widget.TextView
import org.json.JSONArray
import java.util.concurrent.Executors
import vn.trcf.bridge.trcfcore.Trcfcore

/**
 * Màn thiết lập DUY NHẤT: nhập địa chỉ ERP + mã 6 số, chọn máy in, bấm Ghép.
 *
 * Ghép xong thì cùng địa chỉ đó dùng để mở màn bán hàng — người lắp chỉ nhập MỘT lần.
 * Mở lại màn này (chạm thông báo "đang sẵn sàng in") để xem nhật ký hoặc ghép THÊM
 * máy in (vd máy in bếp): lõi gửi kèm token cũ nên vẫn một máy quầy bên ERP.
 */
class SetupActivity : Activity() {
    private val io = Executors.newSingleThreadExecutor()
    private val ui = Handler(Looper.getMainLooper())

    private lateinit var server: EditText
    private lateinit var code: EditText
    private lateinit var printerPick: Spinner
    private lateinit var status: TextView
    private lateinit var logView: TextView
    private lateinit var openPos: Button
    private var printers: List<Pair<String, String>> = emptyList() // key → label

    private val refreshLog = object : Runnable {
        override fun run() {
            logView.text = LogBuffer.snapshot().takeLast(40).joinToString("\n")
            ui.postDelayed(this, 2000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= 33) {
            // Không bắt buộc để in; chỉ để thông báo "đang sẵn sàng in" hiện ra.
            requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 1)
        }
        setContentView(buildLayout())
        server.setText(App.serverUrl(this))
        refreshPrinters()
    }

    override fun onResume() {
        super.onResume()
        ui.post(refreshLog)
        openPos.visibility = if (App.isPaired(this)) View.VISIBLE else View.GONE
    }

    override fun onPause() {
        ui.removeCallbacks(refreshLog)
        super.onPause()
    }

    override fun onDestroy() {
        io.shutdown()
        super.onDestroy()
    }

    private fun refreshPrinters() {
        say("Đang tìm máy in USB…")
        UsbPermission.requestAll(this) {
            io.execute {
                val result = runCatching {
                    val arr = JSONArray(Trcfcore.listPrintersJSON())
                    (0 until arr.length()).map {
                        val o = arr.getJSONObject(it)
                        val fam = when (o.optString("family")) {
                            "escpos" -> " — máy in bill"
                            "tspl" -> " — máy in TEM"
                            else -> ""
                        }
                        o.getString("key") to (o.getString("label") + fam)
                    }
                }
                ui.post {
                    printers = result.getOrDefault(emptyList())
                    printerPick.adapter = ArrayAdapter(
                        this, android.R.layout.simple_spinner_dropdown_item,
                        printers.map { it.second }.ifEmpty { listOf("(chưa thấy máy in nào)") },
                    )
                    result.exceptionOrNull()?.let { say("Lỗi dò máy in: ${it.message}") }
                        ?: say(if (printers.isEmpty()) "Không thấy máy in USB. Kiểm tra dây, nguồn máy in rồi bấm Tìm lại." else "Thấy ${printers.size} máy in.")
                }
            }
        }
    }

    private fun selectedKey(): String? = printers.getOrNull(printerPick.selectedItemPosition)?.first

    private fun testPrint() {
        val key = selectedKey() ?: return say("Chưa có máy in để in thử.")
        run("Đang in thử…") {
            Trcfcore.testPrint(key, App.version(), true, 0)
            "✓ Đã in thử và mở két. Kiểm: giấy có ĐỨT RỜI không, chữ có dấu có ra đúng không."
        }
    }

    private fun pair() {
        val key = selectedKey() ?: return say("Chưa có máy in để ghép.")
        val url = server.text.toString().trim()
        val c = code.text.toString().trim()
        if (url.isEmpty() || c.isEmpty()) return say("Nhập địa chỉ ERP và mã ghép 6 số.")
        run("Đang in thử rồi ghép với ERP…") {
            val name = Trcfcore.pair(App.configPath(this), url, c, key, App.hostname(), App.version())
            // Ghép THÊM máy in khi lõi đang chạy: dừng hẳn (chờ in nốt lệnh dở — cắt
            // ngang là ERP in lại tờ thứ hai) rồi chạy lại để nạp cấu hình vừa ghi.
            Trcfcore.stop(90_000)
            ui.post {
                code.setText("")
                openPos.visibility = View.VISIBLE
                App.startBridge(this)
            }
            "✓ Đã ghép với máy in \"$name\". Dịch vụ in đã chạy nền."
        }
    }

    private fun run(start: String, block: () -> String) {
        say(start)
        io.execute {
            val msg = try {
                block()
            } catch (e: Exception) {
                "✗ ${e.message}"
            }
            ui.post { say(msg) }
        }
    }

    private fun say(msg: String) {
        status.text = msg
    }

    private fun buildLayout(): View {
        val pad = (16 * resources.displayMetrics.density).toInt()
        val col = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad, pad, pad)
        }
        fun label(t: String) = TextView(this).apply {
            text = t
            setPadding(0, pad, 0, pad / 4)
            setTypeface(typeface, Typeface.BOLD)
        }
        col.addView(TextView(this).apply {
            text = "TRCF POS — thiết lập máy in"
            textSize = 22f
            setTypeface(typeface, Typeface.BOLD)
        })
        col.addView(label("Địa chỉ ERP"))
        server = EditText(this).apply {
            hint = "vd http://192.168.1.50"
            inputType = InputType.TYPE_TEXT_VARIATION_URI
            isSingleLine = true
        }
        col.addView(server)
        col.addView(label("Mã ghép 6 số (ERP → Máy in → Lấy mã ghép)"))
        code = EditText(this).apply {
            inputType = InputType.TYPE_CLASS_NUMBER
            isSingleLine = true
        }
        col.addView(code)
        col.addView(label("Máy in"))
        printerPick = Spinner(this)
        col.addView(printerPick)

        val row = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL; gravity = Gravity.START }
        row.addView(Button(this).apply { text = "Tìm lại máy in"; setOnClickListener { refreshPrinters() } })
        row.addView(Button(this).apply { text = "In thử"; setOnClickListener { testPrint() } })
        row.addView(Button(this).apply { text = "Ghép & bắt đầu"; setOnClickListener { pair() } })
        col.addView(row)

        status = TextView(this).apply { textSize = 16f; setPadding(0, pad, 0, pad) }
        col.addView(status)

        openPos = Button(this).apply {
            text = "Mở màn bán hàng"
            setOnClickListener {
                startActivity(Intent(this@SetupActivity, MainActivity::class.java))
                finish()
            }
        }
        col.addView(openPos)

        col.addView(label("Nhật ký"))
        logView = TextView(this).apply {
            typeface = Typeface.MONOSPACE
            textSize = 11f
        }
        col.addView(logView)
        return ScrollView(this).apply { addView(col) }
    }
}

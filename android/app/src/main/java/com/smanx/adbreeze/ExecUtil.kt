package com.smanx.adbreeze

import android.os.Build
import java.io.File

/**
 * 二进制执行工具。
 *
 * Android 10+ 的 SELinux W^X 策略禁止 untrusted_app 直接 execve 应用私有目录里的
 * 二进制（app_data_file:execute 被 neverallow）。绕法（Termux termux-exec 同款）：
 * 内核只允许 exec 系统文件，于是 execve("/system/bin/linker64", [binary, args...])，
 * 由动态链接器代为加载目标 ELF。bionic linker 直接调用模式下，目标程序看到的
 * argv[0] 仍是自己的真实路径。
 *
 * ABI 策略（一期只打 arm64-v8a 资产）：
 *  - 真机（首选 ABI = arm64-v8a）：/system/bin/linker64 直接加载；
 *  - x86_64 模拟器（MuMu 等带 houdini 转译）：/system/bin/arm64/linker64——
 *    这是 NativeBridge 自带的 ARM bionic linker，可转译执行 arm64 ELF 及其
 *    arm64 so 依赖（x86_64 的系统 linker64 会直接拒绝 EM_AARCH64，实测报
 *    "is for EM_AARCH64 (183) instead of EM_X86_64 (62)"）；
 *  - 不支持 arm64 的设备：无可用资产，明确报错。
 */
object ExecUtil {

    private const val OUR_ABI = "arm64-v8a"

    /** 设备能跑我们资产时返回资产 ABI，否则 null。 */
    fun pickAbi(): String? {
        return if (OUR_ABI in Build.SUPPORTED_ABIS) OUR_ABI else null
    }

    /** 按设备首选 ABI 选择加载器路径（NativeBridge 的 ARM linker 目录名是 arm64）。 */
    fun linkerPath(): String {
        val primary = Build.SUPPORTED_ABIS.firstOrNull() ?: ""
        return if (primary == OUR_ABI) "/system/bin/linker64" else "/system/bin/arm64/linker64"
    }

    /**
     * 经 linker 启动一个进程。
     *
     * @param binary 目标二进制绝对路径（应用私有目录内）
     * @param args   传给目标程序的参数（不含 argv[0]）
     * @param env    附加环境变量（在系统环境之上叠加）
     * @param output 非空时把 stdout/stderr 重定向到该文件（长驻进程防管道堵死）
     */
    fun exec(
        binary: String,
        args: List<String>,
        env: Map<String, String> = emptyMap(),
        output: File? = null,
    ): Process {
        val pb = ProcessBuilder(listOf(linkerPath(), binary) + args)
        pb.environment().putAll(env)
        pb.redirectErrorStream(true)
        if (output != null) pb.redirectOutput(output)
        return pb.start()
    }

    /** 等待 TCP 端口可连（本机服务就绪探测），超时返回 false。 */
    fun waitPort(port: Int, timeoutMs: Long): Boolean {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            try {
                java.net.Socket().use { s ->
                    s.connect(java.net.InetSocketAddress("127.0.0.1", port), 500)
                }
                return true
            } catch (_: Exception) {
                try { Thread.sleep(200) } catch (_: InterruptedException) { return false }
            }
        }
        return false
    }
}

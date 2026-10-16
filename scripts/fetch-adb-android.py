#!/usr/bin/env python3
"""拉取安卓版 adb（Termux android-tools）及依赖，产出 APK assets。

流程（纯 Python，无第三方依赖，本地/CI 通用）：
  1. 拉取 Termux 仓库的 Packages 索引，解析 android-tools 及依赖的 deb 文件名
  2. 下载 deb，内存中解析（ar -> data.tar.xz -> 成员文件）
  3. 解析 adb 的 DT_NEEDED 依赖闭包（ELF 解析用纯 struct，不依赖 readelf）
  4. adb + 闭包 so + termux-exec 库落盘到 android/app/src/main/assets/adb/<abi>

用法：
  python scripts/fetch-adb-android.py            # 全部 ABI（aarch64 + x86_64）
  python scripts/fetch-adb-android.py aarch64    # 单 ABI
"""
import gzip
import io
import os
import struct
import sys
import tarfile
import urllib.request

REPO = "https://packages.termux.dev/apt/termux-main"
ARCHES = ("aarch64", "x86_64")
ABI = {"aarch64": "arm64-v8a", "x86_64": "x86_64"}
MACHINE = {"aarch64": 0xB7, "x86_64": 0x3E}
ASSETS = os.path.normpath(os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "..", "android/app/src/main/assets/adb"))

# android-tools 的运行时依赖（apt Depends 去掉 liblog/libc/libdl/libm 等系统库）
DEPS = ["abseil-cpp", "brotli", "fmt", "libc++", "liblz4", "libprotobuf", "pcre2", "zlib", "zstd"]
# termux-exec：LD_PRELOAD 钩子，拦截 adb 内部 execv 自己 fork-server 的调用并改走
# /system/bin/linker64（Android 10+ W^X 限制的官方绕法，见 termux/termux-exec-package）
EXEC_LIBS = [
    "libtermux-exec.so",
    "libtermux-exec-direct-ld-preload.so",
    "libtermux-exec-linker-ld-preload.so",
    "libtermux-exec-ld-preload.so",
    "libtermux-exec_nos_c_tre.so",
]


def fetch(url):
    with urllib.request.urlopen(url, timeout=180) as r:
        return r.read()


def parse_packages_index(arch):
    text = gzip.decompress(
        fetch(f"{REPO}/dists/stable/main/binary-{arch}/Packages.gz")
    ).decode("utf-8", "replace")
    out, pkg = {}, None
    for line in text.splitlines():
        if line.startswith("Package: "):
            pkg = line[9:].strip()
        elif line.startswith("Filename: ") and pkg:
            out[pkg] = line[10:].strip()
            pkg = None
    return out


def deb_data_members(data):
    """ar 归档 -> {data.tar.* 成员名: bytes}"""
    assert data[:8] == b"!<arch>\n", "不是 ar 归档"
    off, out = 8, {}
    while off + 60 <= len(data):
        hdr = data[off:off + 60]
        name = hdr[0:16].decode().strip()
        size = int(hdr[48:58].decode().strip())
        body = data[off + 60:off + 60 + size]
        off += 60 + size
        if name.startswith("data.tar"):
            out[name] = body
    return out


def tar_files(data):
    """tar.xz/tar.gz/tar -> {成员路径: bytes}"""
    for mode in ("r:xz", "r:gz", "r:"):
        try:
            with tarfile.open(fileobj=io.BytesIO(data), mode=mode) as t:
                return {m.name: t.extractfile(m).read() for m in t.getmembers() if m.isfile()}
        except Exception:
            continue
    raise RuntimeError("无法解包 tar")


def elf_info(data):
    """64 位 LE ELF：返回 (e_machine, soname|None, [DT_NEEDED...])；非 ELF 返回 None"""
    if data[:4] != b"\x7fELF" or data[4] != 2 or data[5] != 1:
        return None
    e_machine, = struct.unpack_from("<H", data, 18)
    e_shoff, = struct.unpack_from("<Q", data, 0x28)
    e_shentsize, e_shnum, e_shstrndx = struct.unpack_from("<HHH", data, 0x3A)
    if e_shoff == 0 or e_shnum == 0:
        return None
    secs = []
    for i in range(e_shnum):
        off = e_shoff + i * e_shentsize
        sh_name, _t, _f, _a, sh_offset, sh_size, sh_link = struct.unpack_from("<IIQQQQI", data, off)
        secs.append((sh_name, sh_offset, sh_size, sh_link))
    shstr_off = secs[e_shstrndx][1]

    def nm(strtab_off, o):
        return data[strtab_off + o:data.index(b"\0", strtab_off + o)].decode()

    needed, soname = [], None
    for sh_name, sh_offset, sh_size, sh_link in secs:
        if nm(shstr_off, sh_name) == ".dynamic":
            dynstr_off = secs[sh_link][1]
            for i in range(0, sh_size, 16):
                tag, val = struct.unpack_from("<qQ", data, sh_offset + i)
                if tag == 0:
                    break
                if tag == 1:  # DT_NEEDED
                    needed.append(nm(dynstr_off, val))
                elif tag == 14:  # DT_SONAME
                    soname = nm(dynstr_off, val)
    return e_machine, soname, needed


def build_abi(arch, idx):
    abi = ABI[arch]
    dest = os.path.join(ASSETS, abi)
    print(f"==> {arch} -> {dest}")

    pkgs = ["android-tools"] + DEPS
    for p in pkgs + ["termux-exec"]:
        if p not in idx:
            raise RuntimeError(f"{arch} 索引中找不到包 {p}")

    # 下载 + 解包，建立 soname -> bytes 索引
    lib_index, adb_bytes = {}, None
    for p in pkgs:
        deb = fetch(f"{REPO}/{idx[p]}")
        for _name, body in deb_data_members(deb).items():
            for path, content in tar_files(body).items():
                base = os.path.basename(path)
                if base == "adb" and "/bin/" in path:
                    adb_bytes = content
                elif ".so" in base:
                    info = elf_info(content)
                    if info and info[1]:
                        lib_index.setdefault(info[1], content)
    assert adb_bytes, f"{arch}: adb 二进制未找到"

    # 依赖闭包（BFS）
    m, _soname, needed = elf_info(adb_bytes)
    assert m == MACHINE[arch], f"{arch}: adb 机器类型不符 ({m:#x})"
    result, queue, missing, seen = {}, list(needed), [], set()
    while queue:
        n = queue.pop()
        if n in seen:
            continue
        seen.add(n)
        content = lib_index.get(n)
        if content is None:
            missing.append(n)  # libc/libdl/liblog/libm 等由 Android 系统提供
            continue
        result[n] = content
        queue.extend(elf_info(content)[2])

    os.makedirs(dest, exist_ok=True)
    for f in os.listdir(dest):
        os.remove(os.path.join(dest, f))
    with open(os.path.join(dest, "adb"), "wb") as f:
        f.write(adb_bytes)
    for n, content in result.items():
        with open(os.path.join(dest, n), "wb") as f:
            f.write(content)

    # termux-exec 的 LD_PRELOAD 库（libtermux-exec.so 是指向 -ld-preload 的软链，
    # tarfile 的 isfile 会滤掉 symlink，这里一并处理：软链落成实体拷贝）
    deb = fetch(f"{REPO}/{idx['termux-exec']}")
    members: dict[str, bytes] = {}
    links: dict[str, str] = {}
    for _name, body in deb_data_members(deb).items():
        with tarfile.open(fileobj=io.BytesIO(body)) as t:
            for m in t.getmembers():
                if m.issym() and os.path.basename(m.name) in EXEC_LIBS:
                    links[os.path.basename(m.name)] = os.path.basename(m.linkname)
                elif m.isfile() and os.path.basename(m.name) in EXEC_LIBS:
                    members[os.path.basename(m.name)] = t.extractfile(m).read()
    for target, source in links.items():
        if target not in members and source in members:
            members[target] = members[source]
    for base, content in members.items():
        with open(os.path.join(dest, base), "wb") as f:
            f.write(content)
    exec_cnt = len(members)

    files = os.listdir(dest)
    total = sum(os.path.getsize(os.path.join(dest, f)) for f in files)
    print(f"    adb + {len(result)} so + termux-exec({exec_cnt})，共 {len(files)} 个文件 / {total/1e6:.1f} MB")
    print(f"    系统库（不打包）: {sorted(missing)}")


def main():
    arches = sys.argv[1:] or list(ARCHES)
    for arch in arches:
        if arch not in ARCHES:
            raise SystemExit(f"未知架构: {arch}（可选 {ARCHES}）")
        build_abi(arch, parse_packages_index(arch))
    print("完成")


if __name__ == "__main__":
    main()

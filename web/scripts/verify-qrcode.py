#!/usr/bin/env python3
"""QR 编码器交叉验证的 Python 侧：用 qrcode 库生成参考矩阵。

用法：verify-qrcode.py <text>
输出：JSON { version, matrix } —— matrix 为 0/1 二维数组，无静默区（border=0）。
"""
import json
import sys

import qrcode


def main() -> None:
    text = sys.argv[1]
    qr = qrcode.QRCode(error_correction=qrcode.ERROR_CORRECT_M, border=0)
    qr.add_data(text)
    qr.make(fit=True)
    matrix = [[1 if cell else 0 for cell in row] for row in qr.get_matrix()]
    print(json.dumps({"version": qr.version, "matrix": matrix}))


if __name__ == "__main__":
    main()

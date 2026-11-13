#!/usr/bin/env python3
"""生成桩对象（www/ 下，gitignored）。

刻意让文件体积可预测：video.mp4 恰好 65536 字节，这样 Range 断言里的
`Content-Range: bytes 0-99/65536` 是写死的常量，不用在脚本里反算。
"""
import base64
from pathlib import Path

HERE = Path(__file__).resolve().parent / "www"

# 1x1 PNG（67 字节）—— 平台素材库图标
PNG_1PX = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
)


def put(rel: str, data: bytes) -> None:
    p = HERE / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(data)


# 平台 Go 侧（现状：/uploads/ 由 Go 的 r.Static 出盘）
put("go-uploads/uploads/legacy32hex.jpg", b"\xff\xd8\xff\xe0" + b"L" * 2044 + b"\xff\xd9")
put("go-uploads/uploads/p/library/icon-house.png", PNG_1PX)

# 租户 5 的桶
put("bucket-a/t/5/only-a.jpg", b"\xff\xd8\xff\xe0" + b"A" * 2044 + b"\xff\xd9")
put("bucket-a/t/5/video.mp4", b"\x00" * 65536)

# 租户 6 的桶（刻意不含 only-a.jpg —— 用来证明没串桶）
put("bucket-b/t/6/only-b.jpg", b"\xff\xd8\xff\xe0" + b"B" * 2044 + b"\xff\xd9")

print("✓ 桩对象已生成（video.mp4 = 65536 字节，Range 断言依赖这个常量）")

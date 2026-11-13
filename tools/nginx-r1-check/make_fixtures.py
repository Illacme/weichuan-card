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

# 第六十三轮：生成器改成**路径风格**——桶名进 path（/wc-t5/t/5/x.jpg），
# 否则真 S3 会把键的第一段（t）当成桶名，回 400 InvalidBucketName。
# 桩桶的根目录在这里就代表「桶」，所以真实布局本来就是「桶根 + 对象键」；
# 加一个以桶名命名的自指目录，让同一批桩对象在 /<桶名>/<键> 下也取得到。
# 这不是为了迁就测试：真实 S3 的路径风格就是 /<桶名>/<键>，这里把同一件事
# 用文件系统表达出来，R1 的断言因此一条都不用改。
for bucket_dir, bucket_name in (("bucket-a", "wc-t5"), ("bucket-b", "wc-t6")):
    link = HERE / bucket_dir / bucket_name
    if link.is_symlink() or link.exists():
        link.unlink()
    link.symlink_to(".")

print("✓ 桩对象已生成（video.mp4 = 65536 字节，Range 断言依赖这个常量）")

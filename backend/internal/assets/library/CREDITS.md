# 常用开源素材库 · 许可与来源

本目录由 `gen_library.py` 生成，内嵌进后端二进制（见 `assets.go`）。

- **图标（icons/）**：取自 [Lucide](https://lucide.dev) v0.469.0（ISC License），
  经 rsvg-convert 光栅化并合成到渐变圆角方块。ISC 许可全文见
  `../library-src/LICENSE-Lucide.txt`。
- **封面（covers/）、背景（backgrounds/）、LOGO（logos/）、头像（avatars/）
  与海报（posters/）**：由生成脚本程序绘制（渐变 + 几何图形/人形剪影），
  无第三方素材，可自由使用。海报上的中文标题使用系统宋体渲染。

重新生成：`python3 gen_library.py`
（依赖 Pillow 与 rsvg-convert；Lucide SVG 已留存 library-src/icons/，离线可再生成）。

#!/usr/bin/env python3
# 生成朗汇机电小程序设计还原用的蓝色主题 PNG 占位素材。
# 这些素材仅用于本地联调/演示占位；正式上线请在 PC 后台上传真实图片替换。
import os
from PIL import Image, ImageDraw, ImageFont

OUT = os.path.join(os.path.dirname(__file__), "seedassets")

PRIMARY = (26, 140, 255)   # #1A8CFF 科技亮蓝
DARK = (14, 111, 224)      # #0E6FE0
LIGHT = (232, 243, 255)    # #E8F3FF
WHITE = (255, 255, 255)

FONT_PATH = "/System/Library/Fonts/STHeiti Medium.ttc"


def font(size, idx=0):
    try:
        return ImageFont.truetype(FONT_PATH, size, index=idx)
    except Exception:
        return ImageFont.load_default()


def gradient(w, h, top, bottom):
    img = Image.new("RGB", (w, h), top)
    d = ImageDraw.Draw(img)
    for y in range(h):
        t = y / max(1, h - 1)
        c = tuple(int(top[i] + (bottom[i] - top[i]) * t) for i in range(3))
        d.line([(0, y), (w, y)], fill=c)
    return img


def rr(d, box, r, fill):
    d.rounded_rectangle(box, radius=r, fill=fill)


def center_text(d, box, text, fnt, fill):
    d.text((box[0] + (box[2] - box[0]) / 2, box[1] + (box[3] - box[1]) / 2),
           text, font=fnt, fill=fill, anchor="mm")


def save(img, name):
    p = os.path.join(OUT, name)
    img.save(p, "PNG")
    print("wrote", p)


def make_logo():
    img = gradient(200, 200, PRIMARY, DARK)
    d = ImageDraw.Draw(img)
    d.ellipse([58, 40, 142, 124], fill=WHITE)
    center_text(d, [58, 40, 142, 124], "朗汇", font(40), PRIMARY)
    center_text(d, [0, 130, 200, 180], "机电设备", font(24), WHITE)
    save(img, "logo.png")


def make_avatar(char):
    img = gradient(200, 200, LIGHT, (210, 232, 255))
    d = ImageDraw.Draw(img)
    d.ellipse([20, 20, 180, 180], fill=PRIMARY)
    d.ellipse([34, 34, 166, 166], outline=WHITE, width=4)
    center_text(d, [20, 20, 180, 180], char, font(96), WHITE)
    save(img, "avatar-gaolixin.png")


def make_icon(name, label):
    img = gradient(160, 160, PRIMARY, DARK)
    d = ImageDraw.Draw(img)
    rr(d, [10, 10, 150, 150], 28, None)
    d.rounded_rectangle([10, 10, 150, 150], radius=28, outline=WHITE, width=3)
    center_text(d, [10, 40, 150, 140], label, font(46), WHITE)
    save(img, name)


def make_cover(name, title, subtitle=""):
    img = gradient(640, 360, PRIMARY, DARK)
    d = ImageDraw.Draw(img)
    # 简单楼宇剪影
    d.rectangle([60, 150, 200, 320], fill=(255, 255, 255, 60))
    d.rectangle([230, 90, 360, 320], fill=(255, 255, 255, 80))
    d.rectangle([390, 180, 540, 320], fill=(255, 255, 255, 60))
    for x in range(70, 195, 26):
        d.rectangle([x, 165, x + 12, 305], fill=(255, 255, 255, 120))
    center_text(d, [0, 250, 640, 320], title, font(44), WHITE)
    if subtitle:
        center_text(d, [0, 320, 640, 360], subtitle, font(24), (235, 245, 255))
    save(img, name)


def make_album(name, label):
    img = gradient(400, 300, (110, 170, 255), DARK)
    d = ImageDraw.Draw(img)
    center_text(d, [0, 0, 400, 300], label, font(34), WHITE)
    save(img, name)


def main():
    os.makedirs(OUT, exist_ok=True)
    # logo.png 已换成客户真实 LOGO（去白底透明版），**不要再生成占位图覆盖**。
    print("skip: logo.png 为真实 LOGO，不生成占位图")
    make_avatar("高")
    # 分类图标 cat-*.png 与业务服务 svc-*.png 已换成真实设计图，不生成。
    print("skip: cat-*.png / svc-*.png 为真实设计图，不生成占位图")
    # 公司简介封面 cover-company.jpg 已换成 AI 实景照片（蓝色玻璃观光电梯，
    # 与项目封面同风格）；旧 make_cover 占位图不再生成。
    print("skip: cover-company.jpg 为 AI 实景封面，不生成占位图")
    # 项目封面 cover-*.jpg 已换成 AI 实景照片（见 seedassets），旧 cover-*.png 已删除。
    print("skip: cover-<项目>.jpg 为 AI 实景封面，不生成占位图")
    make_album("album-1.png", "项目实景 ①")
    make_album("album-2.png", "项目实景 ②")
    print("done")


if __name__ == "__main__":
    main()

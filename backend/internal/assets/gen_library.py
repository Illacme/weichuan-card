#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
生成「常用开源素材库」（输出到 ./library/，由 assets.go 用 go:embed 内嵌进后端二进制）。

素材库目标：PC 后台所有需要图片的位置（分类图标 / 服务图标 / 项目封面 / 相册 /
公司简介封面 / 公司 LOGO / 员工头像 / 小程序首页背景）都能直接选内置素材，
免去四处找图、按尺寸处理图的过程。

四类素材：
  icons/   Lucide（ISC 许可）线形图标，rsvg-convert 光栅化为白色图形后合成到
           渐变圆角方块上——与小程序 .cat-placeholder（120rpx 圆角蓝砖 + 白字）
           的既有观感一致，客户已确认的设计语言。
  covers/  程序生成的通用封面（渐变 + 几何 motif，不含文字，任何企业/项目可复用）。
  logos/   程序生成的通用 LOGO 标识（渐变圆角方块 + 白色几何标记）。
  avatars/ 员工暂无照片时的默认头像（渐变圆角方块 + 白色人形剪影）。
  backgrounds/ 背景图（750×1334 竖幅：深色高端商务渐变 + 几何 motif，
           白卡片浮层其上，营造层次感；亦含浅色系供浅色品牌调性选用。

素材按「构图 motif + 配色」解耦：同一 motif 换配色即可派生新款式，
因此扩充时往往只需在 COVERS / LOGOS / BACKGROUNDS 里加一行。

依赖：Pillow（PIL）+ rsvg-convert（librsvg）。Lucide SVG 首次运行从 jsdelivr 下载，
留存到 library-src/icons/（附 LICENSE，ISC 许可），之后离线也可再生成。

用法：
  python3 gen_library.py
"""
import json
import math
import os
import random
import subprocess
import tempfile

from PIL import Image, ImageDraw, ImageFilter, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "library")
SRC = os.path.join(HERE, "library-src", "icons")
LUCIDE_VERSION = "0.469.0"
LUCIDE_BASE = "https://unpkg.com/lucide-static@%s/icons" % LUCIDE_VERSION

# ── 配色 ──────────────────────────────────────────────────────────────
# 分组配色：分类=品牌蓝（与已确认的设计稿一致）、服务=靛蓝、通用=青
# 图标底色：同一套白色线形图标，按企业气质换底色，使图标也能参与调性筛选。
#
# 只做 clean / nature / luxury 三档——这是白色线形图标的物理限制：
# soft（浅色底）与 vivid（高饱和底）会让白色线条对比度不足、小尺寸下糊成一团，
# 做出来反而是劣质素材，故不提供。
ICON_PALETTES = {
    "clean": {  # 冷色科技（原版）：蓝 / 靛 / 青
        "cat": ((26, 140, 255), (14, 111, 224)),
        "svc": ((91, 108, 255), (61, 79, 224)),
        "misc": ((20, 184, 166), (13, 148, 136)),
    },
    "nature": {  # 暖色大地：橙 / 琥珀 / 橄榄，适配农林牧渔、食品、家居、园林
        "cat": ((234, 88, 12), (194, 65, 12)),
        "svc": ((217, 119, 6), (180, 83, 9)),
        "misc": ((132, 204, 22), (101, 163, 13)),
    },
    "luxury": {  # 金铜：金 / 古铜 / 深青铜，适配高端制造、金融、珠宝、酒店
        "cat": ((234, 179, 8), (161, 98, 7)),
        "svc": ((180, 83, 9), (120, 53, 15)),
        "misc": ((146, 64, 14), (69, 26, 3)),
    },
    "soft": {  # 素雅浅底（客户原型图风格）：白→极浅蓝渐变底 + 蓝色细线图形，
        # 三组同底色（统一素净，靠图形本身区分），适配轻盈清新的商务调性
        "cat": ((240, 247, 255), (219, 234, 254)),
        "svc": ((240, 247, 255), (219, 234, 254)),
        "misc": ((240, 247, 255), (219, 234, 254)),
    },
}

# 图标调性变体：(风格 key, id 后缀, 名称后缀, 线条颜色)。
# 线条颜色 None = 白色（深底）；素雅浅底用蓝色线条保证对比度（见客户原型图）。
ICON_VARIANTS = [
    ("clean", "", "", None),
    ("nature", "-warm", " · 暖阳", None),
    ("luxury", "-gold", " · 鎏金", None),
    ("soft", "-soft", " · 素雅", "#2F7CD8"),
]

# (lucide 名, 中文名, 分组, 搜索标签)
ICONS = [
    # ── 业绩分类 ──
    ("house", "居住住宅", "cat", ["住宅", "楼宇", "home"]),
    ("landmark", "政务办公", "cat", ["政务", "government", "机关"]),
    ("hospital", "医院", "cat", ["医疗", "hospital", "卫生"]),
    ("school", "学校", "cat", ["教育", "school", "校园"]),
    ("shopping-bag", "商业综合体", "cat", ["商业", "购物", "商场"]),
    ("train-front", "地铁站点", "cat", ["地铁", "交通", "轨道"]),
    ("factory", "生产制造", "cat", ["工厂", "制造", "车间"]),
    ("warehouse", "仓储物流", "cat", ["仓储", "物流", "仓库"]),
    ("building-2", "楼宇建筑", "cat", ["楼宇", "建筑", "building"]),
    ("briefcase", "商务办公", "cat", ["商务", "办公", "写字楼"]),
    ("graduation-cap", "教育培训", "cat", ["教育", "培训", "高校"]),
    ("hotel", "酒店住宿", "cat", ["酒店", "住宿", "宾馆"]),
    ("utensils", "餐饮美食", "cat", ["餐饮", "美食", "餐厅"]),
    ("bus", "公共交通", "cat", ["公交", "交通", "客运"]),
    ("zap", "能源电力", "cat", ["能源", "电力", "电气"]),
    ("leaf", "绿色环保", "cat", ["环保", "绿色", "节能"]),
    ("shield-check", "安防保障", "cat", ["安防", "保障", "安全"]),
    ("truck", "物流配送", "cat", ["物流", "配送", "运输"]),
    ("plane", "航空机场", "cat", ["航空", "机场", "飞机"]),
    ("ship", "港口航运", "cat", ["港口", "航运", "船舶"]),
    ("store", "商铺零售", "cat", ["商铺", "零售", "门店"]),
    ("library", "文化场馆", "cat", ["文化", "场馆", "图书"]),
    ("flask-conical", "科研实验", "cat", ["科研", "实验", "检测"]),
    ("microscope", "生物医疗", "cat", ["生物", "医疗", "显微"]),
    ("atom", "高新科技", "cat", ["科技", "高新", "原子"]),
    ("dumbbell", "文体健身", "cat", ["健身", "文体", "运动"]),
    ("trees", "园林绿化", "cat", ["园林", "绿化", "景观"]),
    ("wind", "风力新能源", "cat", ["风电", "新能源", "风力"]),
    ("sun", "光伏太阳能", "cat", ["光伏", "太阳能", "发电"]),
    ("droplet", "水务水利", "cat", ["水务", "水利", "给排水"]),
    ("flame", "热力能源", "cat", ["热力", "能源", "燃烧"]),
    ("recycle", "再生资源", "cat", ["再生", "资源", "循环"]),
    ("car", "汽车交通", "cat", ["汽车", "交通", "车行"]),
    ("bike", "慢行系统", "cat", ["慢行", "骑行", "绿道"]),
    ("anchor", "港航码头", "cat", ["港航", "码头", "锚地"]),
    ("church", "文化宗教", "cat", ["文化", "宗教", "礼堂"]),
    ("banknote", "金融服务", "cat", ["金融", "银行", "货币"]),
    ("piggy-bank", "投资理财", "cat", ["投资", "理财", "财富"]),
    ("hammer", "工程施工", "cat", ["施工", "工程", "建造"]),
    ("mountain-snow", "山地景区", "cat", ["山地", "景区", "雪峰"]),
    # ── 更多行业（覆盖细分领域企业）──
    ("wheat", "粮油农产", "cat", ["粮油", "农产", "种植"]),
    ("tractor", "农机农业", "cat", ["农机", "农业", "耕作"]),
    ("apple", "生鲜果品", "cat", ["生鲜", "果品", "果蔬"]),
    ("fish-symbol", "水产海鲜", "cat", ["水产", "海鲜", "渔业"]),
    ("milk", "乳品饮料", "cat", ["乳品", "饮料", "奶制品"]),
    ("croissant", "烘焙面点", "cat", ["烘焙", "面点", "食品"]),
    ("utensils-crossed", "团餐宴席", "cat", ["团餐", "宴席", "食堂"]),
    ("dog", "宠物服务", "cat", ["宠物", "兽医", "萌宠"]),
    ("baby", "母婴护理", "cat", ["母婴", "育儿", "护理"]),
    ("dna", "生物医药", "cat", ["生物", "医药", "基因"]),
    ("brain", "智库咨询", "cat", ["智库", "咨询", "策略"]),
    ("sofa", "家居软装", "cat", ["家居", "软装", "家具"]),
    ("bed-double", "酒店客房", "cat", ["客房", "住宿", "民宿"]),
    ("tent", "户外露营", "cat", ["户外", "露营", "拓展"]),
    ("luggage", "旅游出行", "cat", ["旅游", "出行", "度假"]),
    ("sailboat", "游艇帆船", "cat", ["游艇", "帆船", "水上"]),
    ("fuel", "油品石化", "cat", ["油品", "石化", "加油站"]),
    ("radiation", "核电放射", "cat", ["核电", "放射", "特检"]),
    ("brick-wall", "建材砌筑", "cat", ["建材", "砌筑", "墙体"]),
    ("traffic-cone", "市政工程", "cat", ["市政", "道路", "养护"]),
    ("drill", "钻井机加", "cat", ["钻井", "机加", "钻孔"]),
    ("pickaxe", "矿山采掘", "cat", ["矿山", "采掘", "矿业"]),
    ("anvil", "冶金锻造", "cat", ["冶金", "锻造", "铸造"]),
    ("radar", "探测雷达", "cat", ["雷达", "探测", "传感"]),
    ("satellite-dish", "卫星通信", "cat", ["卫星", "通信", "天线"]),
    ("radio-tower", "通信铁塔", "cat", ["铁塔", "基站", "信号"]),
    ("cpu", "芯片半导体", "cat", ["芯片", "半导体", "电子"]),
    ("bot", "机器人智能", "cat", ["机器人", "智能", "AI"]),
    ("gavel", "法律律所", "cat", ["法律", "律所", "法务"]),
    ("newspaper", "新闻出版", "cat", ["新闻", "出版", "报刊"]),
    ("mic", "会议主持", "cat", ["会议", "主持", "会务"]),
    ("radio", "广播电台", "cat", ["广播", "电台", "音频"]),
    ("tv", "电视媒体", "cat", ["电视", "媒体", "栏目"]),
    ("gamepad-2", "游戏电竞", "cat", ["游戏", "电竞", "娱乐"]),
    ("watch", "钟表珠宝", "cat", ["钟表", "珠宝", "腕表"]),
    ("fence", "围栏护栏", "cat", ["围栏", "护栏", "隔离"]),
    ("fire-extinguisher", "消防设备", "cat", ["消防", "设备", "灭火"]),
    ("heart-handshake", "公益养老", "cat", ["公益", "养老", "助残"]),
    ("trash-2", "环卫清运", "cat", ["环卫", "清运", "保洁"]),
    # ── 业务服务 ──
    ("arrow-up-down", "电梯", "svc", ["电梯", "垂直交通", "elevator"]),
    ("activity", "运行平稳", "svc", ["运行", "平稳", "activity"]),
    ("wrench", "维修服务", "svc", ["维修", "检修", "wrench"]),
    ("cog", "维保服务", "svc", ["维保", "保养", "运维"]),
    ("clipboard-check", "检测验收", "svc", ["检测", "验收", "质检"]),
    ("ruler", "测量定制", "svc", ["测量", "定制", "勘察"]),
    ("hard-hat", "安装施工", "svc", ["安装", "施工", "工程"]),
    ("phone", "咨询预约", "svc", ["咨询", "预约", "电话"]),
    ("clock", "快速响应", "svc", ["响应", "时效", "时钟"]),
    ("headset", "售后客服", "svc", ["售后", "客服", "支持"]),
    ("package", "交付配送", "svc", ["交付", "配送", "发货"]),
    ("handshake", "合作共赢", "svc", ["合作", "共赢", "签约"]),
    ("star", "品质服务", "svc", ["品质", "优质", "星级"]),
    ("life-buoy", "全程保障", "svc", ["保障", "救援", "支持"]),
    ("timer", "限时响应", "svc", ["限时", "计时", "加急"]),
    ("badge-check", "认证合格", "svc", ["认证", "合格", "资质"]),
    ("shield", "安全防护", "svc", ["安全", "防护", "防卫"]),
    ("siren", "应急警报", "svc", ["应急", "警报", "突发"]),
    ("calendar", "排期预约", "svc", ["排期", "预约", "计划"]),
    ("pen-tool", "设计定制", "svc", ["设计", "定制", "方案"]),
    ("settings-2", "系统调试", "svc", ["调试", "系统", "参数"]),
    ("refresh-cw", "循环运维", "svc", ["循环", "运维", "更新"]),
    ("trending-up", "增效增长", "svc", ["增长", "增效", "提升"]),
    ("route", "路线规划", "svc", ["路线", "规划", "路径"]),
    ("paint-roller", "装饰装修", "svc", ["装饰", "装修", "涂装"]),
    ("wifi", "智能网络", "svc", ["智能", "网络", "无线"]),
    ("server", "机房运维", "svc", ["机房", "运维", "服务器"]),
    ("database", "数据服务", "svc", ["数据", "存储", "管理"]),
    ("monitor", "监控值守", "svc", ["监控", "值守", "大屏"]),
    ("thermometer", "温控调节", "svc", ["温控", "调节", "温度"]),
    ("fan", "通风换气", "svc", ["通风", "换气", "新风"]),
    ("snowflake", "制冷冷藏", "svc", ["制冷", "冷藏", "冷链"]),
    ("lock", "安全锁护", "svc", ["安全", "锁定", "防护"]),
    ("fingerprint", "身份核验", "svc", ["身份", "核验", "认证"]),
    ("credit-card", "支付结算", "svc", ["支付", "结算", "收款"]),
    ("receipt", "票据凭证", "svc", ["票据", "凭证", "回单"]),
    ("hourglass", "时效承诺", "svc", ["时效", "承诺", "工期"]),
    ("megaphone", "品牌传播", "svc", ["品牌", "传播", "推广"]),
    # ── 更多服务场景 ──
    ("package-plus", "送货上门", "svc", ["送货", "上门", "配送安装"]),
    ("container", "集装箱运输", "svc", ["集装箱", "海运", "货运"]),
    ("forklift", "装卸搬运", "svc", ["装卸", "搬运", "叉车"]),
    ("boxes", "仓储托管", "svc", ["仓储", "托管", "库存"]),
    ("refrigerator", "冷链配送", "svc", ["冷链", "冷藏", "保鲜"]),
    ("spray-can", "喷涂工艺", "svc", ["喷涂", "工艺", "表面处理"]),
    ("plug-zap", "电气安装", "svc", ["电气", "安装", "布线"]),
    ("battery-charging", "充电服务", "svc", ["充电", "补能", "充电桩"]),
    ("battery-full", "储能备电", "svc", ["储能", "备电", "电源"]),
    ("cable", "线缆敷设", "svc", ["线缆", "敷设", "桥架"]),
    ("power", "配电工程", "svc", ["配电", "供电", "电力"]),
    ("gauge", "仪表校准", "svc", ["仪表", "校准", "计量"]),
    ("scan-line", "扫码溯源", "svc", ["扫码", "溯源", "防伪"]),
    ("barcode", "条码标识", "svc", ["条码", "标识", "编码"]),
    ("scan", "扫描查验", "svc", ["扫描", "查验", "识别"]),
    ("cctv", "视频监控", "svc", ["视频", "监控", "摄像头"]),
    ("shield-alert", "风险预警", "svc", ["风险", "预警", "防控"]),
    ("file-search", "尽职调查", "svc", ["尽调", "调查", "核查"]),
    ("file-check", "合规报批", "svc", ["合规", "报批", "审批"]),
    ("scroll-text", "合同文书", "svc", ["合同", "文书", "签约"]),
    ("party-popper", "活动策划", "svc", ["活动", "策划", "庆典"]),
    ("mail-check", "邮件确认", "svc", ["邮件", "确认", "通知"]),
    ("list-checks", "清单核对", "svc", ["清单", "核对", "逐项"]),
    ("square-check-big", "验收通过", "svc", ["验收", "通过", "确认"]),
    ("cloud-upload", "云端备份", "svc", ["云端", "备份", "同步"]),
    ("languages", "多语翻译", "svc", ["翻译", "多语", "本地化"]),
    # ── 通用 ──
    ("image", "图片", "misc", ["图片", "照片", "image"]),
    ("users", "人群团队", "misc", ["团队", "人群", "用户"]),
    ("award", "荣誉奖项", "misc", ["荣誉", "奖项", "奖杯"]),
    ("gem", "精品案例", "misc", ["精品", "案例", "宝石"]),
    ("lightbulb", "方案创意", "misc", ["创意", "方案", "点子"]),
    ("map-pin", "位置服务", "misc", ["位置", "地图", "定位"]),
    ("globe", "全球业务", "misc", ["全球", "国际", "业务"]),
    ("qr-code", "扫码入口", "misc", ["扫码", "二维码", "入口"]),
    ("share-2", "分享传播", "misc", ["分享", "传播", "转发"]),
    ("message-circle", "消息沟通", "misc", ["消息", "沟通", "对话"]),
    ("thumbs-up", "口碑好评", "misc", ["好评", "口碑", "点赞"]),
    ("target", "目标达成", "misc", ["目标", "达成", "聚焦"]),
    ("compass", "方向指引", "misc", ["方向", "指引", "导航"]),
    ("layout-grid", "矩阵陈列", "misc", ["矩阵", "陈列", "网格"]),
    ("camera", "影像拍摄", "misc", ["影像", "拍摄", "摄影"]),
    ("printer", "印刷输出", "misc", ["印刷", "输出", "打印"]),
    ("shirt", "服饰定制", "misc", ["服饰", "定制", "服装"]),
    ("lamp", "照明亮化", "misc", ["照明", "亮化", "灯具"]),
    ("coffee", "商务洽谈", "misc", ["洽谈", "商务", "会客"]),
    ("cake", "庆典活动", "misc", ["庆典", "活动", "仪式"]),
    ("music", "文娱演出", "misc", ["文娱", "演出", "音乐"]),
    ("film", "影视制作", "misc", ["影视", "制作", "影片"]),
    ("palette", "创意设计", "misc", ["设计", "创意", "美术"]),
    ("gift", "礼遇回馈", "misc", ["礼遇", "回馈", "福利"]),
    ("box", "产品包装", "misc", ["产品", "包装", "货品"]),
    ("tag", "标签分类", "misc", ["标签", "分类", "标记"]),
    # ── 更多通用 ──
    ("history", "发展历程", "misc", ["历程", "发展", "沿革"]),
    ("infinity", "永续合作", "misc", ["永续", "长期", "合作"]),
    ("layers", "体系架构", "misc", ["体系", "架构", "层级"]),
    ("component", "模块单元", "misc", ["模块", "单元", "组件"]),
    ("terminal", "技术研发", "misc", ["研发", "技术", "工程"]),
    ("binary", "数据算法", "misc", ["数据", "算法", "算力"]),
    ("hard-drive", "数据存储", "misc", ["存储", "数据", "备份"]),
    ("milestone", "里程碑", "misc", ["里程碑", "节点", "进展"]),
    ("book-marked", "知识文档", "misc", ["知识", "文档", "资料"]),
    ("contact", "客户名录", "misc", ["客户", "名录", "通讯录"]),
    ("id-card", "证件资质", "misc", ["证件", "资质", "证照"]),
    ("book-user", "讲师培训", "misc", ["讲师", "培训", "授课"]),
    ("sparkles", "精工品质", "misc", ["精工", "品质", "匠心"]),
    ("rocket", "快速上线", "misc", ["上线", "提速", "交付"]),
    ("blend", "融合集成", "misc", ["融合", "集成", "整合"]),
    ("search", "搜索查询", "misc", ["搜索", "查询", "检索"]),
    ("cloud", "云托管", "misc", ["云", "托管", "SaaS"]),
    ("link", "资源对接", "misc", ["对接", "资源", "链接"]),
]

# ── 视觉调性（style）───────────────────────────────────────────────────
# 与 category（用途/位置）正交的第二筛选维度，描述素材的"气质"而非技术手法。
# 命名原则：用用户能直接感知的形容词，不暴露 mesh / grain / noise 等实现术语
# ——客户不知道什么是网格渐变，但知道自己要"沉稳"还是要"温暖"。
STYLES = [
    ("clean", "简约清晰"),
    ("nature", "温暖自然"),
    ("luxury", "轻奢尊贵"),
    ("soft", "柔美清新"),
    ("vivid", "光影流动"),
]

# 调性归类：按素材 id 登记；未登记的缺省 clean（新增素材漏标也能安全降级）。
# 只有构图/氛围存在差异的分类（背景、封面）需要登记；图标/LOGO/头像视觉语言
# 统一，全部落 clean，选择器的风格行会自动隐藏当前分类下 0 结果的调性。
# 海报不在此登记：它的调性直接写在 POSTERS 元组里（节气与商务场景的调性是
# 逐张手定的，拆成一张 id 表反而难维护）。
_STYLE_GROUPS = {
    "clean": (
        # 背景：深色、几何、克制
        "bg-deepspace", "bg-mountain", "bg-citynight", "bg-grid", "bg-diagonal",
        "bg-noir", "bg-ridge", "bg-tech", "bg-charcoal", "bg-royal", "bg-topo",
        "bg-hexcell", "bg-dotmatrix", "bg-dotmatrix-royal", "bg-sparkdust",
        "bg-dusk", "bg-arcstripe", "bg-lines", "bg-geo", "bg-ocean",
        # 封面：明确几何构图
        "cover-skyline", "cover-dots", "cover-blocks", "cover-shield",
        "cover-motion", "cover-stars", "cover-hex", "cover-hex-steel",
        "cover-gridline", "cover-minimal", "cover-night", "cover-circuit",
        "cover-rings", "cover-stripes", "cover-cityline", "cover-prism",
        "cover-topo", "cover-mountain", "cover-meshnodes", "cover-meshnodes-teal",
        "cover-radar", "cover-radar-slate", "cover-chevrons", "cover-stairs",
        "cover-stairs-indigo", "cover-arclines", "cover-confetti",
        "cover-estate-dusk",
    ),
    "nature": (
        "bg-canyon", "bg-forest", "bg-sand", "bg-amber", "bg-sunrise",
        "bg-linen", "bg-weave",
        "cover-veins", "cover-skyline-warm", "cover-minimal-sand",
        "cover-stars-dawn", "cover-orbs-amber", "cover-chevrons-emerald",
        # LOGO / 头像：暖色大地系（橙、叶绿、青柠、琥珀、橄榄、咖啡）
        "logo-bolt", "logo-leaf", "logo-hex-lime", "logo-wave-amber",
        "avatar-5", "avatar-6", "avatar-19", "avatar-23",
    ),
    "luxury": (
        "bg-gold", "bg-marble", "bg-marble-white", "bg-silk", "bg-silk-rose",
        "bg-hexcell-copper",
        "cover-goldline", "cover-goldline-emerald",
        # LOGO / 头像：金、古铜、深酒红、绛红（尊贵感配色）
        "logo-ringstar", "logo-crown", "logo-gear-gold", "logo-compass",
        "logo-peak-bronze",
        "avatar-9", "avatar-12", "avatar-14", "avatar-20",
    ),
    "soft": (
        "bg-ink", "bg-pearl", "bg-mint", "bg-bloom", "bg-bloom-lavender",
        "bg-skyline-day", "bg-ripple",
        "cover-arcs-rose", "cover-ripple-violet", "cover-arclines-rose",
        "cover-confetti-mint", "cover-blocks-mint",
        # LOGO / 头像：高明度淡色（淡紫、浅灰、浅玫红、薄荷、珊瑚、靛紫）
        "logo-orbit-violet", "logo-tower-slate", "logo-diamond-rose",
        "avatar-16", "avatar-17", "avatar-18", "avatar-21",
        # 浅蓝素雅背景（客户原型图风格）与浅底建筑封面
        "bg-skyblue", "bg-cloudmist", "bg-iceblue",
        "cover-glasstower", "cover-estate", "cover-plaza",
    ),
    "vivid": (
        "bg-bokeh", "bg-glow", "bg-mesh", "bg-aurora", "bg-waves", "bg-nebula",
        "bg-coral", "bg-sunburst",
        "cover-beams", "cover-arcs", "cover-waves", "cover-waves-deep",
        "cover-bokeh", "cover-bubbles", "cover-ripple", "cover-orbs",
        # LOGO / 头像：高饱和撞色（紫、玫红、正红）
        "logo-hex", "logo-orbit", "logo-cube", "logo-flame", "logo-spiral",
        "logo-crown-violet",
        "avatar-4", "avatar-7",
    ),
}
STYLE_MAP = {i: s for s, ids in _STYLE_GROUPS.items() for i in ids}
STYLE_NAMES = dict(STYLES)


def style_of(item_id):
    """素材调性：查表，未登记缺省 clean。同时把调性中文名并入搜索标签。"""
    return STYLE_MAP.get(item_id, "clean")

# 封面：渐变 + 几何 motif，无文字。
# 四元组 (id, 名称, 上下渐变色, motif 名)：motif 与 id 解耦，
# 同一 motif 换配色即可派生新款式（扩充素材时只需加一行）。
COVERS = [
    ("cover-skyline", "天际线", ((11, 61, 145), (21, 101, 192)), "skyline"),
    ("cover-dots", "点阵", ((67, 56, 202), (99, 102, 241)), "dots"),
    ("cover-beams", "光束", ((15, 118, 110), (20, 184, 166)), "beams"),
    ("cover-arcs", "弧光", ((109, 40, 217), (139, 92, 246)), "arcs"),
    ("cover-blocks", "矩阵", ((51, 65, 85), (100, 116, 139)), "blocks"),
    ("cover-shield", "守护", ((21, 128, 61), (34, 197, 94)), "shield"),
    ("cover-motion", "疾驰", ((234, 88, 12), (249, 115, 22)), "motion"),
    ("cover-stars", "星网", ((15, 23, 42), (30, 58, 138)), "stars"),
    ("cover-waves", "波浪", ((14, 116, 144), (6, 182, 212)), "waves"),
    ("cover-hex", "蜂巢", ((190, 18, 60), (244, 63, 94)), "hex"),
    ("cover-mountain", "山峦", ((30, 41, 59), (71, 85, 105)), "mountain"),
    ("cover-bokeh", "光斑", ((49, 46, 129), (129, 60, 160)), "bokeh"),
    ("cover-gridline", "格线", ((17, 24, 39), (55, 65, 81)), "gridline"),
    ("cover-goldline", "金绶", ((20, 24, 33), (120, 53, 15)), "goldline"),
    ("cover-minimal", "极简", ((71, 85, 105), (148, 163, 184)), "minimal"),
    ("cover-night", "夜幕", ((2, 6, 23), (30, 27, 75)), "night"),
    # ── 新 motif ──
    ("cover-circuit", "电路", ((8, 47, 73), (2, 132, 199)), "circuit"),
    ("cover-rings", "同心环", ((76, 29, 149), (147, 51, 234)), "rings"),
    ("cover-stripes", "斜纹带", ((120, 53, 15), (217, 119, 6)), "stripes"),
    ("cover-bubbles", "气泡", ((6, 95, 108), (34, 211, 238)), "bubbles"),
    ("cover-cityline", "城市线", ((30, 27, 75), (79, 70, 229)), "cityline"),
    ("cover-veins", "叶脉", ((22, 101, 52), (74, 222, 128)), "veins"),
    ("cover-prism", "棱镜", ((157, 23, 77), (251, 113, 133)), "prism"),
    ("cover-topo", "等高线", ((17, 94, 89), (45, 212, 191)), "topo"),
    # ── 同 motif 换配色（同一款式满足不同品牌调性）──
    ("cover-skyline-warm", "暖阳天际线", ((120, 53, 15), (234, 88, 12)), "skyline"),
    ("cover-waves-deep", "深海波浪", ((12, 74, 110), (8, 145, 178)), "waves"),
    ("cover-hex-steel", "钢蓝蜂巢", ((51, 65, 85), (148, 163, 184)), "hex"),
    ("cover-minimal-sand", "沙调极简", ((120, 113, 108), (214, 211, 209)), "minimal"),
    ("cover-stars-dawn", "晨曦星网", ((76, 29, 149), (236, 72, 153)), "stars"),
    ("cover-blocks-mint", "薄荷矩阵", ((15, 118, 110), (94, 234, 212)), "blocks"),
    ("cover-arcs-rose", "玫红弧光", ((136, 19, 55), (244, 114, 182)), "arcs"),
    ("cover-goldline-emerald", "翡翠金绶", ((6, 78, 59), (16, 185, 129)), "goldline"),
    # ── 新 motif（网阵/雷达/波纹/山形/光球/阶梯/弧线/碎点）──
    ("cover-meshnodes", "网阵", ((30, 58, 138), (2, 132, 199)), "meshnodes"),
    ("cover-radar", "雷达", ((6, 95, 108), (20, 184, 166)), "radar"),
    ("cover-ripple", "波纹环", ((12, 74, 110), (56, 189, 248)), "ripple"),
    ("cover-chevrons", "山形", ((234, 88, 12), (251, 146, 60)), "chevrons"),
    ("cover-orbs", "光球", ((157, 23, 77), (244, 114, 182)), "orbs"),
    ("cover-stairs", "阶梯", ((30, 41, 59), (100, 116, 139)), "stairs"),
    ("cover-arclines", "弧线束", ((76, 29, 149), (167, 139, 250)), "arclines"),
    ("cover-confetti", "碎点", ((190, 24, 93), (251, 113, 133)), "confetti"),
    # ── 新 motif 的配色变体 ──
    ("cover-meshnodes-teal", "青网阵", ((6, 95, 108), (45, 212, 191)), "meshnodes"),
    ("cover-radar-slate", "石墨雷达", ((39, 39, 42), (113, 113, 122)), "radar"),
    ("cover-ripple-violet", "紫波纹", ((49, 46, 129), (129, 140, 248)), "ripple"),
    ("cover-chevrons-emerald", "翡翠山形", ((6, 78, 59), (16, 185, 129)), "chevrons"),
    ("cover-orbs-amber", "琥珀光球", ((146, 64, 14), (251, 191, 36)), "orbs"),
    ("cover-stairs-indigo", "靛蓝阶梯", ((30, 27, 75), (99, 102, 241)), "stairs"),
    ("cover-arclines-rose", "玫红弧线", ((136, 19, 55), (251, 113, 133)), "arclines"),
    ("cover-confetti-mint", "薄荷碎点", ((15, 118, 110), (153, 246, 228)), "confetti"),
    # ── 建筑主题（客户原型图：播放封面 / 项目封面需要楼宇实拍感素材）──
    ("cover-glasstower", "玻璃塔楼", ((224, 242, 254), (96, 165, 250)), "glasstower"),
    ("cover-estate", "楼盘社区", ((224, 242, 254), (125, 211, 252)), "estate"),
    ("cover-plaza", "商务广场", ((241, 245, 249), (148, 163, 184)), "glasstower"),
    ("cover-estate-dusk", "暮色楼盘", ((30, 41, 59), (71, 85, 105)), "estate"),
]

# LOGO：渐变圆角方块 + 白色几何标记。
# 四元组 (id, 名称, 上下渐变色, mark 名)：mark 与 id 解耦，同款换色即可派生新 LOGO。
LOGOS = [
    ("logo-building", "建筑", ((26, 140, 255), (14, 111, 224)), "building"),
    ("logo-bolt", "闪电", ((245, 158, 11), (217, 119, 6)), "bolt"),
    ("logo-gear", "齿轮", ((71, 85, 105), (30, 41, 59)), "gear"),
    ("logo-shield", "盾牌", ((21, 128, 61), (22, 101, 52)), "shield"),
    ("logo-hex", "六边", ((109, 40, 217), (91, 33, 182)), "hex"),
    ("logo-arrow", "上升", ((20, 184, 166), (15, 118, 110)), "arrow"),
    ("logo-tower", "塔楼", ((91, 108, 255), (61, 79, 224)), "tower"),
    ("logo-orbit", "环轨", ((244, 63, 94), (190, 18, 60)), "orbit"),
    ("logo-diamond", "菱钻", ((14, 165, 233), (2, 132, 199)), "diamond"),
    ("logo-ringstar", "星环", ((234, 179, 8), (180, 83, 9)), "ringstar"),
    ("logo-peak", "山峰", ((16, 122, 87), (6, 78, 59)), "peak"),
    ("logo-flow", "流动", ((99, 102, 241), (67, 56, 202)), "flow"),
    ("logo-link", "链接", ((100, 116, 139), (51, 65, 85)), "link"),
    ("logo-cube", "立方", ((225, 29, 72), (159, 18, 57)), "cube"),
    # ── 新 mark ──
    ("logo-wave", "波浪", ((6, 182, 212), (14, 116, 144)), "wave"),
    ("logo-flame", "火焰", ((239, 68, 68), (185, 28, 28)), "flame"),
    ("logo-leaf", "叶片", ((34, 197, 94), (21, 128, 61)), "leaf"),
    ("logo-drop", "水滴", ((59, 130, 246), (29, 78, 216)), "drop"),
    ("logo-crown", "冠冕", ((217, 119, 6), (161, 98, 7)), "crown"),
    ("logo-spiral", "螺旋", ((139, 92, 246), (91, 33, 182)), "spiral"),
    # ── 同 mark 换配色 ──
    ("logo-bolt-cyan", "闪电 · 青", ((34, 211, 238), (8, 145, 178)), "bolt"),
    ("logo-gear-gold", "齿轮 · 金", ((234, 179, 8), (161, 98, 7)), "gear"),
    ("logo-shield-sky", "盾牌 · 天蓝", ((56, 189, 248), (2, 132, 199)), "shield"),
    ("logo-hex-lime", "六边 · 青柠", ((163, 230, 53), (77, 148, 60)), "hex"),
    ("logo-orbit-violet", "环轨 · 紫", ((167, 139, 250), (109, 40, 217)), "orbit"),
    ("logo-tower-slate", "塔楼 · 石板", ((148, 163, 184), (51, 65, 85)), "tower"),
    # ── 新 mark（船锚/拱桥/罗盘/尖括号/三环/九宫格）──
    ("logo-anchor", "船锚", ((14, 116, 144), (8, 47, 73)), "anchor"),
    ("logo-bridge", "拱桥", ((71, 85, 105), (30, 41, 59)), "bridge"),
    ("logo-compass", "罗盘", ((217, 119, 6), (161, 98, 7)), "compass"),
    ("logo-brackets", "尖括号", ((2, 132, 199), (12, 74, 110)), "brackets"),
    ("logo-circles", "三环", ((20, 184, 166), (13, 148, 136)), "circles"),
    ("logo-grid", "九宫格", ((99, 102, 241), (67, 56, 202)), "grid"),
    # ── 更多配色变体 ──
    ("logo-wave-amber", "波浪 · 琥珀", ((251, 191, 36), (217, 119, 6)), "wave"),
    ("logo-flame-royal", "火焰 · 宝蓝", ((29, 78, 216), (30, 58, 138)), "flame"),
    ("logo-leaf-teal", "叶片 · 青碧", ((13, 148, 136), (6, 95, 108)), "leaf"),
    ("logo-crown-violet", "冠冕 · 紫", ((139, 92, 246), (91, 33, 182)), "crown"),
    ("logo-spiral-ocean", "螺旋 · 海蓝", ((2, 132, 199), (8, 47, 73)), "spiral"),
    ("logo-diamond-rose", "菱钻 · 玫红", ((244, 114, 182), (190, 24, 93)), "diamond"),
    ("logo-cube-graphite", "立方 · 石墨", ((113, 113, 122), (39, 39, 42)), "cube"),
    ("logo-peak-bronze", "山峰 · 古铜", ((180, 83, 9), (120, 53, 15)), "peak"),
]

COVER_W, COVER_H = 960, 504
THUMB_W, THUMB_H = 480, 252

# 小程序首页背景图：750×1334 竖幅（750rpx 设计稿 2x），深色高端商务风
BG_W, BG_H = 750, 1334
BG_THUMB_W, BG_THUMB_H = 300, 534

# (id, 名称, 上下渐变色, motif 名, 搜索标签)
# motif 与 id 解耦：同款构图换配色即可派生新背景（浅色系供浅色品牌调性选用）。
BACKGROUNDS = [
    ("bg-deepspace", "深空", ((8, 15, 40), (23, 37, 84)), "stars", ["深空", "星空", "商务蓝"]),
    ("bg-mountain", "山峦", ((15, 32, 46), (30, 66, 82)), "mountain", ["山峦", "层叠", "沉稳"]),
    ("bg-citynight", "城市夜景", ((10, 10, 30), (49, 27, 90)), "city", ["城市", "夜景", "楼宇"]),
    ("bg-bokeh", "光斑", ((30, 27, 75), (76, 29, 110)), "bokeh", ["光斑", "虚化", "梦幻"]),
    ("bg-waves", "涟漪", ((8, 51, 68), (14, 116, 144)), "waves", ["涟漪", "水波", "清新"]),
    ("bg-grid", "格线", ((15, 23, 42), (30, 41, 59)), "grid", ["格线", "科技", "理性"]),
    ("bg-diagonal", "斜纹", ((23, 37, 84), (30, 58, 138)), "diagonal", ["斜纹", "条纹", "简约"]),
    ("bg-glow", "光晕", ((30, 27, 75), (67, 56, 202)), "glow", ["光晕", "聚焦", "高级"]),
    ("bg-gold", "金线", ((12, 15, 23), (66, 48, 20)), "gold", ["金线", "奢华", "尊贵"]),
    ("bg-mesh", "星链", ((6, 46, 53), (15, 118, 110)), "mesh", ["星链", "节点", "网络"]),
    ("bg-aurora", "极光", ((6, 48, 60), (13, 90, 92)), "aurora", ["极光", "光带", "灵动"]),
    ("bg-sand", "流沙", ((41, 37, 36), (87, 73, 60)), "sand", ["流沙", "暖沙", "质感"]),
    # ── 新 motif ──
    ("bg-canyon", "峡谷", ((69, 26, 24), (154, 52, 18)), "canyon", ["峡谷", "岩壁", "暖调"]),
    ("bg-forest", "森林", ((10, 38, 28), (22, 78, 56)), "forest", ["森林", "树木", "自然"]),
    ("bg-ink", "水墨", ((228, 228, 231), (161, 161, 170)), "ink", ["水墨", "留白", "浅色"]),
    ("bg-geo", "几何", ((30, 58, 138), (12, 74, 110)), "geo", ["几何", "拼接", "现代"]),
    ("bg-lines", "光束线", ((15, 23, 42), (55, 65, 81)), "lines", ["光束", "细线", "秩序"]),
    ("bg-nebula", "星云", ((23, 37, 84), (76, 29, 149)), "nebula", ["星云", "梦幻", "渐变"]),
    ("bg-noir", "暗夜", ((9, 9, 11), (39, 39, 42)), "noir", ["暗夜", "极简", "克制"]),
    ("bg-ocean", "深海", ((8, 47, 73), (12, 74, 110)), "ocean", ["深海", "光束", "静谧"]),
    ("bg-pearl", "珠光", ((243, 244, 246), (203, 213, 225)), "pearl", ["珠光", "浅色", "柔和"]),
    ("bg-ridge", "山脊", ((30, 41, 59), (51, 65, 85)), "ridge", ["山脊", "细线", "冷峻"]),
    ("bg-tech", "科技", ((12, 74, 110), (2, 132, 199)), "tech", ["科技", "电路", "智能"]),
    ("bg-linen", "亚麻", ((231, 229, 228), (168, 162, 158)), "linen", ["亚麻", "暖灰", "浅色"]),
    # ── 同 motif 换配色 ──
    ("bg-dusk", "暮色", ((76, 29, 149), (190, 24, 93)), "mountain", ["暮色", "紫粉", "渐变"]),
    ("bg-mint", "薄荷", ((209, 250, 229), (94, 234, 212)), "waves", ["薄荷", "清新", "浅色"]),
    ("bg-coral", "珊瑚", ((127, 29, 29), (225, 29, 72)), "bokeh", ["珊瑚", "热情", "暖调"]),
    ("bg-royal", "皇家蓝", ((12, 35, 128), (29, 78, 216)), "glow", ["皇家蓝", "深邃", "大气"]),
    ("bg-charcoal", "炭黑", ((24, 24, 27), (63, 63, 70)), "grid", ["炭黑", "沉稳", "中性"]),
    ("bg-amber", "琥珀", ((254, 243, 199), (217, 119, 6)), "diagonal", ["琥珀", "暖阳", "浅色"]),
    # ── 浅蓝素雅系（客户原型图：页面浅蓝背景，若隐若现、不抢内容）──
    ("bg-skyblue", "晴空", ((224, 242, 254), (147, 197, 253)), "bokeh", ["晴空", "浅蓝", "清新", "淡雅"]),
    ("bg-cloudmist", "云雾", ((239, 246, 255), (191, 219, 254)), "glow", ["云雾", "极浅", "素雅", "轻盈"]),
    ("bg-iceblue", "冰蓝", ((224, 242, 254), (186, 230, 253)), "waves", ["冰蓝", "水色", "浅色", "通透"]),
    # ── 新 motif（行业调性 + 更多浅色）──
    ("bg-marble", "大理石", ((60, 60, 67), (24, 24, 27)), "marble", ["大理石", "石材", "建材"]),
    ("bg-silk", "丝绸", ((30, 58, 138), (76, 29, 149)), "silk", ["丝绸", "柔顺", "服装"]),
    ("bg-hexcell", "蜂巢", ((15, 23, 42), (51, 65, 85)), "hexcell", ["蜂巢", "工业", "材料"]),
    ("bg-dotmatrix", "点阵", ((8, 47, 73), (2, 132, 199)), "dotmatrix", ["点阵", "数据", "科技"]),
    ("bg-sunburst", "光芒", ((120, 53, 15), (217, 119, 6)), "sunburst", ["光芒", "能源", "光伏"]),
    ("bg-topo", "等高线", ((6, 78, 59), (16, 185, 129)), "topo", ["等高线", "测绘", "工程"]),
    ("bg-bloom", "柔光", ((76, 29, 149), (147, 51, 234)), "bloom", ["柔光", "美业", "婚庆"]),
    ("bg-skyline-day", "晨曦", ((186, 230, 253), (125, 211, 252)), "skyline-day", ["晨曦", "地产", "浅色"]),
    ("bg-ripple", "水波环", ((8, 51, 68), (14, 116, 144)), "ripple", ["水波", "水务", "环保"]),
    ("bg-arcstripe", "弧纹", ((30, 27, 75), (67, 56, 202)), "arcstripe", ["弧纹", "金融", "稳重"]),
    ("bg-sparkdust", "星尘", ((15, 23, 42), (30, 64, 175)), "sparkdust", ["星尘", "互联网", "科技"]),
    ("bg-weave", "织纹", ((245, 245, 244), (214, 211, 209)), "weave", ["织纹", "纺织", "浅色"]),
    # ── 新 motif 的配色变体 ──
    ("bg-marble-white", "白云石", ((228, 228, 231), (161, 161, 170)), "marble", ["白云石", "浅色", "素雅"]),
    ("bg-hexcell-copper", "铜蜂巢", ((66, 32, 6), (146, 64, 14)), "hexcell", ["铜", "金属", "暖调"]),
    ("bg-silk-rose", "玫瑰丝绸", ((131, 24, 67), (190, 24, 93)), "silk", ["玫瑰", "美业", "浪漫"]),
    ("bg-dotmatrix-royal", "宝蓝点阵", ((12, 35, 128), (29, 78, 216)), "dotmatrix", ["宝蓝", "深邃", "大气"]),
    ("bg-bloom-lavender", "薰衣草", ((237, 233, 254), (196, 181, 253)), "bloom", ["薰衣草", "浅色", "柔和"]),
    ("bg-sunrise", "日出", ((127, 29, 29), (251, 146, 60)), "sunburst", ["日出", "暖调", "希望"]),
]

# 头像：员工暂无照片时的默认头像（渐变圆角方块 + 白色人形剪影）
AVATARS = [
    ("avatar-1", "默认头像 · 蓝", ((26, 140, 255), (14, 111, 224))),
    ("avatar-2", "默认头像 · 靛", ((91, 108, 255), (61, 79, 224))),
    ("avatar-3", "默认头像 · 青", ((20, 184, 166), (13, 148, 136))),
    ("avatar-4", "默认头像 · 紫", ((139, 92, 246), (109, 40, 217))),
    ("avatar-5", "默认头像 · 绿", ((34, 197, 94), (21, 128, 61))),
    ("avatar-6", "默认头像 · 橙", ((249, 115, 22), (234, 88, 12))),
    ("avatar-7", "默认头像 · 玫", ((244, 63, 94), (190, 18, 60))),
    ("avatar-8", "默认头像 · 灰", ((100, 116, 139), (51, 65, 85))),
    ("avatar-9", "默认头像 · 金", ((234, 179, 8), (180, 83, 9))),
    ("avatar-10", "默认头像 · 深蓝", ((30, 64, 175), (30, 58, 138))),
    ("avatar-11", "默认头像 · 青碧", ((13, 148, 136), (15, 118, 110))),
    ("avatar-12", "默认头像 · 绛红", ((190, 24, 93), (157, 23, 77))),
    ("avatar-13", "默认头像 · 藏青", ((30, 58, 138), (23, 37, 84))),
    ("avatar-14", "默认头像 · 琥珀", ((217, 119, 6), (161, 98, 7))),
    ("avatar-15", "默认头像 · 石板", ((71, 85, 105), (30, 41, 59))),
    ("avatar-16", "默认头像 · 薄荷", ((94, 234, 212), (13, 148, 136))),
    ("avatar-17", "默认头像 · 珊瑚", ((251, 113, 133), (225, 29, 72))),
    ("avatar-18", "默认头像 · 靛紫", ((129, 140, 248), (67, 56, 202))),
    ("avatar-19", "默认头像 · 橄榄", ((132, 204, 22), (63, 98, 18))),
    ("avatar-20", "默认头像 · 酒红", ((153, 27, 27), (127, 29, 29))),
    ("avatar-21", "默认头像 · 玫瑰", ((251, 113, 133), (225, 29, 72))),
    ("avatar-22", "默认头像 · 天青", ((56, 189, 248), (2, 132, 199))),
    ("avatar-23", "默认头像 · 咖啡", ((146, 64, 14), (87, 83, 78))),
    ("avatar-24", "默认头像 · 墨绿", ((6, 95, 108), (6, 78, 59))),
]


# ── 海报素材 ──────────────────────────────────────────────────────────
#
# 海报是员工在小程序里分享出去的「门面」：画布 320×460（CSS px），按 dpr 放大
# 后在 2x 屏上要 640×920 物理像素，故海报原图取 640×920（2x 稿，比例 0.6957
# 与画布严格一致——canvas drawImage 铺满时零裁切、零变形）。
#
# 构图固定为「上部标题区 + 下部信息区」：
#   · 上部 0~150px 烧入主标题（大字）+ 副标题 + 印章，视觉主体；
#   · 下部 150~460px 是压深的纯色区，小程序 canvas 在其上绘制员工信息
#     （姓名/职位/公司/电话/头像/小程序码），浅色文字压深底才读得出。
# 素材只管「背景与标题」，员工信息一律由小程序端实时绘制——这样一张海报
# 可以给任何员工用，不需要为每个人出一张图。
#
# 海报 motif 与配色解耦（同款构图换色派生），与 backgrounds 同思路。
# 字段：id, 素材库显示名, 图上主标题, 图上副标题, 上下渐变色, motif, 调性, 标签
POSTER_W, POSTER_H = 640, 920
POSTER_THUMB_W, POSTER_THUMB_H = 320, 460
# 信息区起始（与小程序 canvas 的信息区对齐：标题区 0~150，画布上从 y=150 起绘制）
POSTER_INFO_TOP = 150

# 标题用衬线中文字体（宋体系）：宋体的横细竖粗在渐变底上比黑体更有节庆/东方感，
# 且大字与英文小字混排时重心更稳。找不到任何候选字体时退化为「无标题版」
# （只出渐变 + 装饰），不阻塞素材库生成。
_SERIF_CANDIDATES = [
    ("/System/Library/Fonts/Supplemental/Songti.ttc", 1),   # macOS 宋体（Bold）
    ("/System/Library/Fonts/STHeiti Medium.ttc", 0),
    ("/usr/share/fonts/truetype/arphic/uming.ttc", 0),      # Linux 明体
    ("/usr/share/fonts/opentype/noto/NotoSerifCJK-Regular.ttc", 0),
]
_SANS_CANDIDATES = [
    # 副标题里混了中文（"LI CHUN · 二十四节气"），必须用**有中文字形**的字体：
    # Helvetica / DejaVu 没有 CJK 字形，中文会画成一片豆腐块（踩过）。
    ("/System/Library/Fonts/Hiragino Sans GB.ttc", 0),   # macOS 冬青黑体（简）
    ("/System/Library/Fonts/STHeiti Medium.ttc", 0),
    ("/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc", 0),  # Linux 文泉驿
    ("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", 0),  # 仅拉丁（中文会缺字）
]

POSTERS = [
    # ── 二十四节气 ──
    ("poster-lichun", "立春", "立春", "LI CHUN · 二十四节气", ((22, 92, 68), (108, 176, 132)), "willow", "nature", ["立春", "春", "节气", "东风"]),
    ("poster-yushui", "雨水", "雨水", "YU SHUI · 二十四节气", ((24, 74, 104), (96, 168, 200)), "rain", "nature", ["雨水", "春", "节气", "春雨"]),
    ("poster-jingzhe", "惊蛰", "惊蛰", "JING ZHE · 二十四节气", ((46, 38, 96), (118, 92, 178)), "thunder", "vivid", ["惊蛰", "春", "节气", "雷"]),
    ("poster-chunfen", "春分", "春分", "CHUN FEN · 二十四节气", ((196, 92, 124), (244, 186, 200)), "blossom", "soft", ["春分", "春", "节气", "桃花"]),
    ("poster-qingming", "清明", "清明", "QING MING · 二十四节气", ((44, 88, 76), (140, 184, 168)), "mist", "soft", ["清明", "春", "节气", "细雨"]),
    ("poster-guyu", "谷雨", "谷雨", "GU YU · 二十四节气", ((36, 92, 68), (122, 190, 150)), "rain", "nature", ["谷雨", "春", "节气", "雨生百谷"]),
    ("poster-lixia", "立夏", "立夏", "LI XIA · 二十四节气", ((18, 96, 72), (108, 196, 156)), "sun", "nature", ["立夏", "夏", "节气", "万物并秀"]),
    ("poster-xiaoman", "小满", "小满", "XIAO MAN · 二十四节气", ((134, 100, 26), (226, 190, 96)), "wheat", "nature", ["小满", "夏", "节气", "麦粒"]),
    ("poster-mangzhong", "芒种", "芒种", "MANG ZHONG · 二十四节气", ((112, 82, 16), (214, 172, 74)), "wheat", "nature", ["芒种", "夏", "节气", "收麦"]),
    ("poster-xiazhi", "夏至", "夏至", "XIA ZHI · 二十四节气", ((12, 76, 104), (78, 176, 200)), "sun", "vivid", ["夏至", "夏", "节气", "烈日"]),
    ("poster-xiaoshu", "小暑", "小暑", "XIAO SHU · 二十四节气", ((150, 62, 22), (238, 142, 66)), "sun", "vivid", ["小暑", "夏", "节气", "暑气"]),
    ("poster-dashu", "大暑", "大暑", "DA SHU · 二十四节气", ((126, 38, 18), (222, 108, 48)), "sun", "vivid", ["大暑", "夏", "节气", "酷热"]),
    ("poster-liqiu", "立秋", "立秋", "LI QIU · 二十四节气", ((132, 82, 24), (226, 162, 78)), "leaf", "nature", ["立秋", "秋", "节气", "落叶"]),
    ("poster-chushu", "处暑", "处暑", "CHU SHU · 二十四节气", ((116, 68, 36), (208, 146, 96)), "leaf", "nature", ["处暑", "秋", "节气", "暑退"]),
    ("poster-bailu", "白露", "白露", "BAI LU · 二十四节气", ((66, 100, 126), (168, 196, 212)), "dew", "soft", ["白露", "秋", "节气", "露珠"]),
    ("poster-qiufen", "秋分", "秋分", "QIU FEN · 二十四节气", ((132, 100, 24), (226, 184, 80)), "moon", "luxury", ["秋分", "秋", "节气", "月圆"]),
    ("poster-hanlu", "寒露", "寒露", "HAN LU · 二十四节气", ((100, 68, 36), (184, 148, 102)), "leaf", "nature", ["寒露", "秋", "节气", "菊"]),
    ("poster-shuangjiang", "霜降", "霜降", "SHUANG JIANG · 二十四节气", ((84, 58, 38), (162, 130, 100)), "frost", "clean", ["霜降", "秋", "节气", "霜"]),
    ("poster-lidong", "立冬", "立冬", "LI DONG · 二十四节气", ((28, 54, 90), (92, 132, 178)), "snow", "clean", ["立冬", "冬", "节气", "初雪"]),
    ("poster-xiaoxue", "小雪", "小雪", "XIAO XUE · 二十四节气", ((54, 74, 100), (146, 172, 198)), "snow", "soft", ["小雪", "冬", "节气", "轻雪"]),
    ("poster-daxue", "大雪", "大雪", "DA XUE · 二十四节气", ((24, 38, 72), (68, 100, 158)), "snow", "clean", ["大雪", "冬", "节气", "瑞雪"]),
    ("poster-dongzhi", "冬至", "冬至", "DONG ZHI · 二十四节气", ((16, 26, 56), (54, 76, 128)), "moon", "luxury", ["冬至", "冬", "节气", "夜长"]),
    ("poster-xiaohan", "小寒", "小寒", "XIAO HAN · 二十四节气", ((28, 56, 66), (96, 146, 156)), "snow", "clean", ["小寒", "冬", "节气", "严寒"]),
    ("poster-dahan", "大寒", "大寒", "DA HAN · 二十四节气", ((20, 32, 58), (60, 84, 116)), "snow", "clean", ["大寒", "冬", "节气", "极寒"]),
    # ── 节日 / 商务场景 ──
    ("poster-chunjie", "春节", "新春快乐", "SPRING FESTIVAL", ((132, 22, 22), (216, 78, 44)), "lantern", "vivid", ["春节", "节日", "新年", "红灯笼"]),
    ("poster-yuanxiao", "元宵", "元宵节", "LANTERN FESTIVAL", ((86, 30, 74), (188, 78, 146)), "lantern", "vivid", ["元宵", "节日", "灯会"]),
    ("poster-duanwu", "端午", "端午安康", "DRAGON BOAT FESTIVAL", ((22, 92, 72), (92, 178, 132)), "bamboo", "nature", ["端午", "节日", "粽子"]),
    ("poster-zhongqiu", "中秋", "花好月圆", "MID-AUTUMN FESTIVAL", ((22, 26, 62), (86, 66, 132)), "moon", "luxury", ["中秋", "节日", "月亮", "团圆"]),
    ("poster-guoqing", "国庆", "欢度国庆", "NATIONAL DAY", ((142, 22, 22), (224, 78, 58)), "confetti", "vivid", ["国庆", "节日", "红旗"]),
    ("poster-yuandan", "元旦", "新岁如意", "HAPPY NEW YEAR", ((20, 32, 58), (72, 106, 162)), "confetti", "vivid", ["元旦", "节日", "新年"]),
    ("poster-shengdan", "圣诞", "圣诞快乐", "MERRY CHRISTMAS", ((16, 46, 30), (48, 108, 70)), "pine", "nature", ["圣诞", "节日", "冬青"]),
    ("poster-shengri", "生日", "生日快乐", "HAPPY BIRTHDAY", ((96, 28, 70), (196, 84, 148)), "confetti", "soft", ["生日", "节日", "庆祝"]),
    # ── 农历节日（小程序端按当天日期自动选，见 miniprogram/utils/festival.ts）──
    ("poster-chuxi", "除夕", "团圆除夕", "NEW YEAR'S EVE", ((104, 18, 18), (208, 68, 40)), "lantern", "vivid", ["除夕", "节日", "团圆", "年夜饭"]),
    ("poster-laba", "腊八", "腊八安康", "LA BA FESTIVAL", ((92, 46, 20), (198, 124, 58)), "sun", "nature", ["腊八", "节日", "腊八粥"]),
    ("poster-xiaonian", "小年", "小年祭灶", "XIAO NIAN", ((116, 34, 20), (206, 96, 52)), "lantern", "vivid", ["小年", "节日", "祭灶", "扫尘"]),
    ("poster-longtaitou", "龙抬头", "龙抬头", "LONG TAI TOU", ((30, 84, 74), (104, 168, 128)), "willow", "nature", ["龙抬头", "节日", "二月二", "剃龙头"]),
    ("poster-qixi", "七夕", "七夕星河", "QI XI FESTIVAL", ((46, 30, 88), (128, 84, 168)), "moon", "luxury", ["七夕", "节日", "星河", "乞巧"]),
    ("poster-chongyang", "重阳", "重阳安康", "CHONG YANG", ((120, 74, 24), (206, 150, 70)), "leaf", "nature", ["重阳", "节日", "登高", "敬老"]),
    # ── 公历节日（同上）──
    ("poster-qingrenjie", "情人节", "怦然心动", "VALENTINE'S DAY", ((140, 30, 66), (222, 108, 140)), "blossom", "soft", ["情人节", "节日", "告白"]),
    ("poster-nvshenjie", "女神节", "女神节快乐", "WOMEN'S DAY", ((158, 44, 96), (232, 138, 176)), "blossom", "soft", ["女神节", "节日", "三八", "女性"]),
    ("poster-laodongjie", "劳动节", "劳动光荣", "LABOUR DAY", ((20, 60, 96), (66, 138, 172)), "gear", "clean", ["劳动节", "节日", "五一", "劳动者"]),
    ("poster-muqinjie", "母亲节", "感恩母亲", "MOTHER'S DAY", ((170, 66, 96), (238, 158, 178)), "blossom", "soft", ["母亲节", "节日", "妈妈", "感恩"]),
    ("poster-ertongjie", "儿童节", "童心未泯", "CHILDREN'S DAY", ((36, 96, 130), (110, 186, 214)), "confetti", "vivid", ["儿童节", "节日", "六一", "童年"]),
    ("poster-fuqinjie", "父亲节", "父爱如山", "FATHER'S DAY", ((28, 48, 84), (86, 118, 164)), "pine", "clean", ["父亲节", "节日", "爸爸", "感恩"]),
    ("poster-jiaoshijie", "教师节", "桃李芬芳", "TEACHERS' DAY", ((52, 62, 40), (140, 152, 92)), "bamboo", "nature", ["教师节", "节日", "老师", "感恩"]),
    ("poster-ganenjie", "感恩节", "感恩有你", "THANKSGIVING", ((110, 52, 22), (198, 118, 58)), "wheat", "nature", ["感恩节", "节日", "感谢"]),
    ("poster-pinganye", "平安夜", "平安夜", "CHRISTMAS EVE", ((18, 52, 36), (56, 112, 76)), "pine", "nature", ["平安夜", "节日", "圣诞", "平安"]),
    ("poster-zhaopin", "招聘", "诚聘英才", "JOIN OUR TEAM", ((16, 54, 92), (56, 138, 168)), "gear", "clean", ["招聘", "商务", "人才"]),
    ("poster-hezuo", "商务合作", "合作共赢", "WIN-WIN PARTNERSHIP", ((16, 46, 88), (52, 120, 150)), "arcs", "clean", ["商务", "合作", "共赢"]),
    ("poster-kaiye", "开业大吉", "开业大吉", "GRAND OPENING", ((118, 84, 16), (226, 176, 66)), "rays", "luxury", ["开业", "商务", "庆典"]),
    ("poster-zhanhui", "展会邀请", "诚邀莅临", "EXHIBITION INVITATION", ((22, 40, 84), (86, 104, 170)), "grid", "clean", ["展会", "商务", "邀请"]),
    ("poster-xinpin", "新品上市", "新品上市", "NEW ARRIVAL", ((56, 26, 92), (128, 84, 176)), "rays", "vivid", ["新品", "商务", "发布"]),
    ("poster-jiaofu", "项目交付", "顺利交付", "PROJECT DELIVERED", ((20, 62, 48), (76, 148, 116)), "pine", "clean", ["交付", "商务", "项目"]),
    ("poster-qiaojian", "乔迁之喜", "乔迁之喜", "NEW HOME", ((86, 56, 26), (186, 136, 72)), "wave", "luxury", ["乔迁", "商务", "新居"]),
    ("poster-daxie", "客户答谢", "感谢有你", "THANK YOU", ((70, 24, 40), (166, 66, 96)), "arcs", "luxury", ["答谢", "商务", "客户"]),
    ("poster-zhufu", "节日祝福", "万事顺意", "BEST WISHES", ((20, 58, 78), (78, 142, 164)), "wave", "soft", ["祝福", "节日", "问候"]),
    ("poster-kaigong", "开工大吉", "开工大吉", "BACK TO WORK", ((118, 52, 20), (216, 128, 56)), "sun", "nature", ["开工", "商务", "开工"]),
    ("poster-xiaoqi", "校企合作", "校企同行", "UNIVERSITY & INDUSTRY", ((24, 48, 88), (72, 108, 168)), "building", "clean", ["校企", "商务", "合作"]),
    ("poster-brand", "匠心品质", "匠心筑造", "CRAFTED WITH CARE", ((20, 30, 46), (76, 100, 128)), "building", "clean", ["品牌", "商务", "品质"]),
]

# 盖「節慶」章的海报；其余（节气走副标题判定、商务场景不盖章）都不盖。
# 新增节日海报必须同步登记这里，否则图上不会出现那枚朱红印章——
# 而「有没有章」正是用户在素材库里区分「节气图 / 节日图 / 商务图」的最快线索。
POSTER_FESTIVALS = frozenset({
    "poster-chunjie", "poster-yuanxiao", "poster-duanwu", "poster-zhongqiu",
    "poster-guoqing", "poster-yuandan", "poster-shengdan", "poster-shengri",
    "poster-kaigong", "poster-qiaojian",
    # 农历节日
    "poster-chuxi", "poster-laba", "poster-xiaonian", "poster-longtaitou",
    "poster-qixi", "poster-chongyang",
    # 公历节日
    "poster-qingrenjie", "poster-nvshenjie", "poster-laodongjie",
    "poster-muqinjie", "poster-ertongjie", "poster-fuqinjie",
    "poster-jiaoshijie", "poster-ganenjie", "poster-pinganye",
})

# ── 基础绘制 ──────────────────────────────────────────────────────────
def vgrad(size, top, bottom):
    """竖直渐变 RGB 图。"""
    w, h = size
    img = Image.new("RGB", (w, h), top)
    d = ImageDraw.Draw(img)
    for y in range(h):
        t = y / max(1, h - 1)
        c = tuple(int(top[i] + (bottom[i] - top[i]) * t) for i in range(3))
        d.line([(0, y), (w, y)], fill=c)
    return img


def rounded_tile(size, top, bottom, radius_ratio=0.22, ss=4):
    """圆角渐变方块（RGBA，圆角外透明）；ss 倍超采样抗锯齿。"""
    S = size * ss
    img = vgrad((S, S), top, bottom).convert("RGBA")
    mask = Image.new("L", (S, S), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        [0, 0, S - 1, S - 1], radius=int(S * radius_ratio), fill=255
    )
    img.putalpha(mask)
    return img.resize((size, size), Image.LANCZOS)


def http_get(url, dst=None, retries=4, valid=None):
    """经 curl 下载（代理环境下比 urllib 稳，带重试）；dst 为 None 时返回字节。
    valid(bytes) -> bool：内容校验（防代理截断出半截文件），不合法则重试。"""
    last_err = "unknown"
    for attempt in range(retries):
        cmd = ["curl", "-sSL", "--max-time", "30", "--retry", "2"]
        if dst:
            cmd += ["-o", dst]
        cmd.append(url)
        r = subprocess.run(cmd, capture_output=True)
        if r.returncode == 0:
            if dst:
                with open(dst, "rb") as f:
                    data = f.read()
            else:
                data = r.stdout
            if valid is None or valid(data):
                return data if not dst else True
            last_err = "invalid content (%d bytes)" % len(data)
            if dst and os.path.exists(dst):
                os.unlink(dst)
        else:
            last_err = "curl exit %d" % r.returncode
        print("  download retry %d (%s): %s" % (attempt + 1, last_err, url))
    raise RuntimeError("download failed: " + url + " (" + last_err + ")")


def is_svg(data):
    """SVG 内容校验：非空且以 </svg> 收尾（防代理截断出半截文件）。"""
    return len(data) > 100 and data.rstrip().endswith(b"</svg>")


def fetch_lucide(name):
    """下载 Lucide SVG 到 library-src/icons/（已存在则跳过）。"""
    os.makedirs(SRC, exist_ok=True)
    dst = os.path.join(SRC, name + ".svg")
    if os.path.exists(dst):
        return dst
    data = http_get("%s/%s.svg" % (LUCIDE_BASE, name), valid=is_svg)
    with open(dst, "wb") as f:
        f.write(data)
    print("  fetched %s (%d bytes)" % (name, len(data)))
    return dst


def render_glyph(svg_path, size, stroke_scale=0.75, color="#FFFFFF"):
    """Lucide SVG → 指定尺寸的线形图形 PNG（透明底，rsvg-convert 光栅化）。
    color 默认白色（深底用）；浅底变体传蓝色等深色以保证对比度。"""
    src = open(svg_path, encoding="utf-8").read()
    src = src.replace('stroke="currentColor"', 'stroke="%s"' % color)
    src = src.replace('stroke-width="2"', 'stroke-width="%.2f"' % (2 * stroke_scale))
    with tempfile.TemporaryDirectory() as td:
        svg = os.path.join(td, "g.svg")
        png = os.path.join(td, "g.png")
        with open(svg, "w", encoding="utf-8") as f:
            f.write(src)
        subprocess.run(
            ["rsvg-convert", "-w", str(size), "-h", str(size), "-o", png, svg],
            check=True, capture_output=True,
        )
        return Image.open(png).convert("RGBA")


def make_icon(item_id, name, lucide, group, tags, colors, style, glyph_color="#FFFFFF"):
    S = 256
    tile = rounded_tile(S, *colors)
    glyph = render_glyph(fetch_lucide(lucide), int(S * 0.62), color=glyph_color)
    tile.alpha_composite(glyph, ((S - glyph.width) // 2, (S - glyph.height) // 2))
    path = os.path.join(OUT, "icons", item_id + ".png")
    tile.save(path, "PNG")
    return {"id": item_id, "name": name, "category": "icon", "style": style,
            "file": "icons/%s.png" % item_id, "thumb": "icons/%s.png" % item_id,
            "width": S, "height": S, "tags": tags + [STYLE_NAMES[style]]}


# ── 封面 motif ────────────────────────────────────────────────────────
def overlay(base):
    """在渐变底上叠一层全幅 RGBA 画布（统一用半透明白色作图形色）。"""
    ov = Image.new("RGBA", base.size, (0, 0, 0, 0))
    return ov, ImageDraw.Draw(ov)


def motif_skyline(img):
    ov, d = overlay(img)
    w, h = img.size
    random.seed(11)
    x = 40
    while x < w - 60:
        bw = random.choice([70, 90, 110, 130])
        bh = random.randint(int(h * 0.35), int(h * 0.72))
        top = h - bh
        d.rectangle([x, top, x + bw, h], fill=(255, 255, 255, 46))
        for wy in range(top + 14, h - 12, 22):
            for wx in range(x + 10, x + bw - 8, 20):
                if random.random() < 0.55:
                    d.rectangle([wx, wy, wx + 9, wy + 11], fill=(255, 255, 255, 120))
        x += bw + random.randint(14, 26)
    img.alpha_composite(ov)


def motif_dots(img):
    ov, d = overlay(img)
    w, h = img.size
    for gy in range(30, h, 44):
        for gx in range(30, w, 44):
            d.ellipse([gx - 3, gy - 3, gx + 3, gy + 3], fill=(255, 255, 255, 70))
    d.ellipse([w * 0.62, h * 0.12, w * 0.62 + 190, h * 0.12 + 190],
              outline=(255, 255, 255, 150), width=10)
    img.alpha_composite(ov)


def motif_beams(img):
    ov, d = overlay(img)
    w, h = img.size
    for i, alpha in enumerate((40, 70, 110, 160)):
        x0 = -200 + i * 190
        d.polygon([(x0, h), (x0 + 90, h), (x0 + 90 + h, 0), (x0 + h, 0)],
                  fill=(255, 255, 255, alpha))
    img.alpha_composite(ov)


def motif_arcs(img):
    ov, d = overlay(img)
    w, h = img.size
    cx, cy = w * 0.78, h * 1.05
    for i, r in enumerate(range(120, 620, 90)):
        d.arc([cx - r, cy - r, cx + r, cy + r], 180, 360,
              fill=(255, 255, 255, 60 + i * 22), width=12)
    img.alpha_composite(ov)


def motif_blocks(img):
    ov, d = overlay(img)
    w, h = img.size
    random.seed(23)
    for row in range(4):
        for col in range(9):
            bw = random.randint(50, 110)
            bh = random.randint(30, 90)
            x = 30 + col * 105 + random.randint(-8, 8)
            y = 30 + row * 120 + random.randint(-8, 8)
            d.rounded_rectangle([x, y, x + bw, y + bh], radius=10,
                                fill=(255, 255, 255, random.randint(28, 84)))
    img.alpha_composite(ov)


def motif_shield(img):
    ov, d = overlay(img)
    w, h = img.size
    cx, cy, s = w * 0.5, h * 0.52, 130
    pts = [(cx, cy - s), (cx + s * 0.85, cy - s * 0.62), (cx + s * 0.85, cy + s * 0.18),
           (cx, cy + s), (cx - s * 0.85, cy + s * 0.18), (cx - s * 0.85, cy - s * 0.62)]
    d.line(pts + [pts[0]], fill=(255, 255, 255, 200), width=12, joint="curve")
    d.line([(cx - 52, cy - 4), (cx - 12, cy + 40), (cx + 60, cy - 44)],
           fill=(255, 255, 255, 220), width=16, joint="curve")
    img.alpha_composite(ov)


def motif_motion(img):
    ov, d = overlay(img)
    w, h = img.size
    random.seed(31)
    for i in range(16):
        y = 26 + i * 30
        ln = random.randint(int(w * 0.25), int(w * 0.85))
        d.rounded_rectangle([40, y, 40 + ln, y + 12], radius=6,
                            fill=(255, 255, 255, random.randint(36, 120)))
    img.alpha_composite(ov)


def motif_stars(img):
    ov, d = overlay(img)
    w, h = img.size
    random.seed(41)
    for _ in range(90):
        x, y = random.randint(0, w), random.randint(0, int(h * 0.7))
        r = random.choice([2, 2, 3, 4])
        d.ellipse([x - r, y - r, x + r, y + r], fill=(255, 255, 255, random.randint(90, 200)))
    # 透视网格
    horizon = int(h * 0.66)
    for i in range(-8, 9):
        d.line([(w / 2 + i * 60, horizon), (w / 2 + i * 260, h)],
               fill=(255, 255, 255, 60), width=2)
    for j in range(1, 7):
        y = horizon + (h - horizon) * (j / 6) ** 1.6
        d.line([(0, y), (w, y)], fill=(255, 255, 255, 50), width=2)
    img.alpha_composite(ov)


def motif_waves(img):
    import math
    ov, d = overlay(img)
    w, h = img.size
    for k, alpha in enumerate((50, 80, 120)):
        base = h * (0.55 + k * 0.14)
        amp = 26 - k * 5
        pts = [(x, base + amp * math.sin((x / w) * math.pi * 2 + k * 1.2))
               for x in range(0, w + 8, 8)]
        d.polygon(pts + [(w, h), (0, h)], fill=(255, 255, 255, alpha))
    img.alpha_composite(ov)


def motif_hex(img):
    import math
    ov, d = overlay(img)
    w, h = img.size
    r = 46
    for row in range(-1, 8):
        for col in range(-1, 13):
            cx = col * r * 1.5
            cy = row * r * math.sqrt(3) + (col % 2) * r * math.sqrt(3) / 2
            pts = [(cx + r * math.cos(math.radians(60 * i)),
                    cy + r * math.sin(math.radians(60 * i))) for i in range(6)]
            d.line(pts + [pts[0]], fill=(255, 255, 255, 70), width=3, joint="curve")
    img.alpha_composite(ov)


def motif_mountain(img):
    """层叠山脉剪影（远山浅近山深，稳重商务）。"""
    ov, d = overlay(img)
    w, h = img.size
    layers = [(0.52, 60, 0.62), (0.66, 90, 0.78), (0.80, 130, 1.0)]
    random.seed(57)
    for base_y, alpha, _ in layers:
        pts = [(0, h)]
        x = 0
        peak = h * base_y
        while x < w:
            step = random.randint(90, 170)
            ph = random.randint(40, 130)
            pts += [(x + step / 2, peak - ph), (x + step, peak)]
            x += step
        pts += [(w, h)]
        d.polygon(pts, fill=(255, 255, 255, alpha))
    img.alpha_composite(ov)


def motif_bokeh(img):
    """柔和光斑（大小不一的半透明圆，梦幻高级）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(63)
    for _ in range(26):
        r = random.randint(26, 96)
        x = random.randint(-40, w + 40)
        y = random.randint(-40, int(h * 0.9))
        a = random.randint(18, 70)
        d.ellipse([x - r, y - r, x + r, y + r], fill=(255, 255, 255, a))
        d.ellipse([x - r, y - r, x + r, y + r], outline=(255, 255, 255, a + 40), width=2)
    img.alpha_composite(ov)


def motif_gridline(img):
    """细网格 + 高亮节点（科技理性）。"""
    ov, d = overlay(img)
    w, h = img.size
    step = 72
    for x in range(0, w + 1, step):
        d.line([(x, 0), (x, h)], fill=(255, 255, 255, 34), width=1)
    for y in range(0, h + 1, step):
        d.line([(0, y), (w, y)], fill=(255, 255, 255, 34), width=1)
    for cx, cy in ((w * 0.24, h * 0.30), (w * 0.68, h * 0.56), (w * 0.42, h * 0.80)):
        r = 10
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=(255, 255, 255, 150))
        d.ellipse([cx - 22, cy - 22, cx + 22, cy + 22],
                  outline=(255, 255, 255, 90), width=2)
    img.alpha_composite(ov)


def motif_goldline(img):
    """金色装饰线框（奢华尊贵，深底金字）。"""
    ov, d = overlay(img)
    w, h = img.size
    gold = (212, 175, 55, 200)
    m = 44
    d.rectangle([m, m, w - m, h - m], outline=gold, width=3)
    d.rectangle([m + 12, m + 12, w - m - 12, h - m - 12],
                outline=(212, 175, 55, 90), width=1)
    cx, cy = w / 2, h / 2
    d.line([(cx - 90, cy), (cx + 90, cy)], fill=gold, width=3)
    d.ellipse([cx - 8, cy - 8, cx + 8, cy + 8], fill=gold)
    img.alpha_composite(ov)


def motif_minimal(img):
    """极简：一个大圆环 + 一根细线（大量留白）。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy, r = w * 0.5, h * 0.44, min(w, h) * 0.30
    d.ellipse([cx - r, cy - r, cx + r, cy + r],
              outline=(255, 255, 255, 170), width=8)
    d.line([(w * 0.18, h * 0.80), (w * 0.82, h * 0.80)],
           fill=(255, 255, 255, 110), width=3)
    img.alpha_composite(ov)


def motif_night(img):
    """夜幕楼宇：黑色楼体剪影 + 窗格灯火。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(71)
    x = -20
    while x < w + 20:
        bw = random.randint(70, 130)
        bh = random.randint(int(h * 0.28), int(h * 0.62))
        top = h - bh
        d.rectangle([x, top, x + bw, h], fill=(0, 0, 0, 120))
        for wy in range(top + 16, h - 14, 26):
            for wx in range(x + 12, x + bw - 10, 24):
                if random.random() < 0.42:
                    d.rectangle([wx, wy, wx + 10, wy + 12],
                                fill=(255, 214, 130, random.randint(120, 220)))
        x += bw + random.randint(10, 30)
    img.alpha_composite(ov)


def motif_circuit(img):
    """电路走线：直角折线 + 焊点（科技/智造）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(131)
    for _ in range(14):
        x, y = random.randint(0, w), random.randint(0, h)
        pts = [(x, y)]
        for _ in range(random.randint(2, 4)):
            if random.random() < 0.5:
                x += random.choice([-1, 1]) * random.randint(60, 180)
            else:
                y += random.choice([-1, 1]) * random.randint(40, 120)
            pts.append((x, y))
        d.line(pts, fill=(255, 255, 255, random.randint(50, 130)),
               width=3, joint="curve")
        d.ellipse([pts[-1][0] - 6, pts[-1][1] - 6, pts[-1][0] + 6, pts[-1][1] + 6],
                  fill=(255, 255, 255, 190))
    img.alpha_composite(ov)


def motif_rings(img):
    """同心环：右下角发散圆环。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy = w * 0.74, h * 0.60
    for i, r in enumerate(range(70, 700, 62)):
        d.ellipse([cx - r, cy - r, cx + r, cy + r],
                  outline=(255, 255, 255, 46 + (i % 4) * 26), width=4)
    img.alpha_composite(ov)


def motif_stripes(img):
    """斜纹带：不等宽斜条（动感）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(133)
    x = -h
    while x < w + h:
        bw = random.choice([26, 42, 64]) if random.random() < 0.5 else 12
        d.polygon([(x, 0), (x + 90, 0), (x + 90 + h, h), (x + h, h)],
                  fill=(255, 255, 255, random.randint(24, 96)))
        x += bw + 34
    img.alpha_composite(ov)


def motif_bubbles(img):
    """气泡：大小圆错落悬浮。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(137)
    for _ in range(34):
        r = random.randint(16, 74)
        x, y = random.randint(0, w), random.randint(0, h)
        d.ellipse([x - r, y - r, x + r, y + r],
                  outline=(255, 255, 255, random.randint(50, 140)), width=3)
        if r > 44:
            d.ellipse([x - r * 0.42, y - r * 0.52, x - r * 0.14, y - r * 0.24],
                      fill=(255, 255, 255, 120))
    img.alpha_composite(ov)


def motif_cityline(img):
    """城市线：白色线描天际线（简约现代）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(139)
    x = -20
    pts = [(0, h)]
    while x < w + 20:
        bw = random.randint(50, 110)
        bh = random.randint(60, 220)
        pts += [(x, h - bh), (x + bw, h - bh)]
        x += bw
    pts += [(w, h)]
    d.line(pts, fill=(255, 255, 255, 190), width=5, joint="curve")
    for i in range(9):
        y = h - random.randint(30, 200)
        d.line([(0, y), (w, y)], fill=(255, 255, 255, 26), width=2)
    img.alpha_composite(ov)


def motif_veins(img):
    """叶脉：主脉 + 对称侧脉（生态/农业）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(149)
    d.line([(w * 0.5, h), (w * 0.5, -20)], fill=(255, 255, 255, 150), width=6)
    for i in range(11):
        y = h - i * (h / 11)
        ln = 90 + i * 26
        for sign in (-1, 1):
            d.line([(w * 0.5, y), (w * 0.5 + sign * ln, y - 46)],
                   fill=(255, 255, 255, 70 + (i % 3) * 22), width=3)
    img.alpha_composite(ov)


def motif_prism(img):
    """棱镜：色散三角光带。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy = w * 0.22, h * 0.52
    for i, col in enumerate(((255, 138, 138), (255, 214, 130),
                             (150, 255, 190), (150, 210, 255))):
        spread = 40 + i * 34
        d.polygon([(cx, cy), (w, cy - 90 - spread), (w, cy + 90 - spread)],
                  fill=col + (54,))
    d.polygon([(cx - 60, cy - 92), (cx + 60, cy - 92), (cx, cy + 96)],
              outline=(255, 255, 255, 220), width=4)
    img.alpha_composite(ov)


def motif_topo(img):
    """等高线：层叠闭合曲线（测绘/地理）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(151)
    for i in range(7):
        pts = []
        for x in range(0, w + 20, 20):
            y = h * (0.22 + i * 0.10) + math.sin(x / 130 + i) * 34 + \
                math.sin(x / 47 + i * 2) * 14
            pts.append((x, y))
        d.line(pts, fill=(255, 255, 255, 40 + i * 14), width=3)
    img.alpha_composite(ov)


def motif_meshnodes(img):
    """网阵：斜向网格 + 交点节点（平台/网络/互联）。"""
    ov, d = overlay(img)
    w, h = img.size
    step = 56
    for i in range(-h // step, w // step + h // step + 1):
        x = i * step
        d.line([(x, 0), (x + h, h)], fill=(255, 255, 255, 38), width=2)
        d.line([(x, h), (x + h, 0)], fill=(255, 255, 255, 38), width=2)
    random.seed(211)
    for _ in range(46):
        px, py = random.randint(0, w), random.randint(0, h)
        r = random.choice([3, 4, 5])
        d.ellipse([px - r, py - r, px + r, py + r],
                  fill=(255, 255, 255, random.randint(120, 220)))
    img.alpha_composite(ov)


def motif_radar(img):
    """雷达：同心弧 + 扫描线（探测/数据/监测）。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy = w * 0.5, h * 0.52
    rmax = min(w, h) * 0.62
    for i in range(1, 6):
        r = rmax * i / 5
        d.ellipse([cx - r, cy - r, cx + r, cy + r],
                  outline=(255, 255, 255, 64 - i * 6), width=3)
    d.line([(cx - rmax, cy), (cx + rmax, cy)], fill=(255, 255, 255, 50), width=2)
    d.line([(cx, cy - rmax), (cx, cy + rmax)], fill=(255, 255, 255, 50), width=2)
    ex, ey = cx + rmax * 0.72, cy - rmax * 0.52
    d.line([(cx, cy), (ex, ey)], fill=(255, 255, 255, 120), width=5)
    d.ellipse([ex - 9, ey - 9, ex + 9, ey + 9], fill=(255, 255, 255, 210))
    img.alpha_composite(ov)


def motif_ripple(img):
    """波纹环：多层同心扁环（水务/传播/声学）。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy, base = w * 0.5, h * 0.46, min(w, h) * 0.08
    for i in range(9):
        a = 150 - i * 14
        if a <= 0:
            break
        r = base + i * min(w, h) * 0.075
        d.ellipse([cx - r, cy - r * 0.62, cx + r, cy + r * 0.62],
                  outline=(255, 255, 255, a), width=4)
    img.alpha_composite(ov)


def motif_chevrons(img):
    """山形箭头：锯齿折线堆叠（进阶/增长/阶段）。"""
    ov, d = overlay(img)
    w, h = img.size
    for i in range(5):
        y = h * (0.22 + i * 0.15)
        amp = h * 0.10
        pts = [(w * k / 7, y + (amp if k % 2 else 0)) for k in range(8)]
        d.line(pts, fill=(255, 255, 255, 140 - i * 20), width=6, joint="curve")
    img.alpha_composite(ov)


def motif_orbs(img):
    """光球：大圆柔光斑（美业/教育/柔和调性）。"""
    w, h = img.size
    random.seed(307)
    for _ in range(7):
        r = random.randint(int(w * 0.10), int(w * 0.26))
        cx, cy = random.randint(0, w), random.randint(0, h)
        img.alpha_composite(_radial_glow(img.size, (cx, cy), r,
                                         (255, 255, 255), random.randint(30, 70)))


def motif_stairs(img):
    """阶梯：递进矩形（成长/阶段/里程碑）。"""
    ov, d = overlay(img)
    w, h = img.size
    for i in range(6):
        bh = h * (0.16 + i * 0.13)
        x = w * 0.10 + i * (w * 0.13)
        y = h - bh - h * 0.10
        d.rectangle([x, y, x + w * 0.112, h - h * 0.10],
                    fill=(255, 255, 255, 40 + i * 16))
    img.alpha_composite(ov)


def motif_arclines(img):
    """弧线束：平行细弧（秩序/设计/精密）。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy = w * 0.5, h * 0.52
    for i in range(14):
        r = min(w, h) * (0.28 + i * 0.075)
        d.arc([cx - r, cy - r, cx + r, cy + r], start=200, end=340,
              fill=(255, 255, 255, 120 - i * 7), width=3)
    img.alpha_composite(ov)


def motif_confetti(img):
    """碎点：随机小方块与圆点（活动/庆典/零售）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(419)
    for _ in range(90):
        x, y = random.randint(0, w), random.randint(0, h)
        s = random.randint(4, 16)
        a = random.randint(50, 170)
        if random.random() < 0.5:
            d.rectangle([x, y, x + s, y + s], fill=(255, 255, 255, a))
        else:
            d.ellipse([x, y, x + s, y + s], fill=(255, 255, 255, a))
    img.alpha_composite(ov)


def motif_glasstower(img):
    """玻璃塔楼：错落楼体 + 幕墙竖纹 + 楼层横线 + 天线 + 斜向反光（建筑日景，
    适合作视频播放封面 / 项目封面的建筑主题素材）。"""
    ov, d = overlay(img)
    w, h = img.size
    base = h
    # (x, 高度比例, 宽)：高低错落的主楼群
    for x, hr, bw in ((60, 0.30, 150), (240, 0.52, 120), (390, 0.68, 170),
                      (600, 0.44, 130), (760, 0.58, 150)):
        bh = int(h * hr)
        top = base - bh
        d.rectangle([x, top, x + bw, base], fill=(255, 255, 255, 52))
        for gx in range(x + 10, x + bw - 8, 16):  # 幕墙竖纹
            d.line([(gx, top + 8), (gx, base - 6)], fill=(255, 255, 255, 60), width=2)
        for gy in range(top + 12, base - 8, 26):  # 楼层横线
            d.line([(x + 4, gy), (x + bw - 4, gy)], fill=(255, 255, 255, 34), width=1)
        d.line([(x + bw // 2, top - 26), (x + bw // 2, top)],
               fill=(255, 255, 255, 130), width=3)  # 天线
    # 斜向反光带（玻璃幕墙的反光感）
    band = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    ImageDraw.Draw(band).polygon([(w * 0.15, h), (w * 0.45, 0), (w * 0.58, 0),
                                  (w * 0.28, h)], fill=(255, 255, 255, 26))
    ov = Image.alpha_composite(ov, band.filter(ImageFilter.GaussianBlur(18)))
    d.line([(0, base), (w, base)], fill=(255, 255, 255, 110), width=3)
    img.alpha_composite(ov)


def motif_estate(img):
    """楼盘社区：远景楼群剪影 + 中景住宅楼（带窗格）+ 前景地面（地产日景）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(223)
    x = -10
    while x < w:  # 远景楼群
        bw = random.randint(60, 110)
        bh = random.randint(int(h * 0.10), int(h * 0.22))
        d.rectangle([x, int(h * 0.62) - bh, x + bw, int(h * 0.62)],
                    fill=(255, 255, 255, 28))
        x += bw + random.randint(6, 18)
    for bx, hr, bw in ((90, 0.30, 200), (420, 0.38, 240), (720, 0.28, 190)):
        bh = int(h * hr)
        top = int(h * 0.78) - bh
        d.rectangle([bx, top, bx + bw, int(h * 0.78)], fill=(255, 255, 255, 50))
        for wy in range(top + 12, int(h * 0.78) - 10, 24):  # 窗格
            for wx in range(bx + 12, bx + bw - 10, 26):
                d.rectangle([wx, wy, wx + 14, wy + 12], fill=(255, 255, 255, 90))
    d.rectangle([0, int(h * 0.78), w, h], fill=(255, 255, 255, 20))
    d.line([(0, int(h * 0.78)), (w, int(h * 0.78))],
           fill=(255, 255, 255, 90), width=3)
    img.alpha_composite(ov)


COVER_MOTIFS = {
    "skyline": motif_skyline, "dots": motif_dots, "beams": motif_beams,
    "arcs": motif_arcs, "blocks": motif_blocks, "shield": motif_shield,
    "motion": motif_motion, "stars": motif_stars, "waves": motif_waves,
    "hex": motif_hex, "mountain": motif_mountain, "bokeh": motif_bokeh,
    "gridline": motif_gridline, "goldline": motif_goldline,
    "minimal": motif_minimal, "night": motif_night, "circuit": motif_circuit,
    "rings": motif_rings, "stripes": motif_stripes, "bubbles": motif_bubbles,
    "cityline": motif_cityline, "veins": motif_veins, "prism": motif_prism,
    "glasstower": motif_glasstower, "estate": motif_estate,
    "topo": motif_topo, "meshnodes": motif_meshnodes, "radar": motif_radar,
    "ripple": motif_ripple, "chevrons": motif_chevrons, "orbs": motif_orbs,
    "stairs": motif_stairs, "arclines": motif_arclines,
    "confetti": motif_confetti,
}


def make_cover(item_id, name, colors, motif):
    img = vgrad((COVER_W, COVER_H), *colors).convert("RGBA")
    COVER_MOTIFS[motif](img)
    path = os.path.join(OUT, "covers", item_id + ".jpg")
    img.convert("RGB").save(path, "JPEG", quality=87)
    # 预览缩略图（选择器里加载，避免原图流量）
    thumb = img.convert("RGB").resize((THUMB_W, THUMB_H), Image.LANCZOS)
    tpath = os.path.join(OUT, "thumbs", "covers", item_id + ".jpg")
    thumb.save(tpath, "JPEG", quality=80)
    return {"id": item_id, "name": name, "category": "cover", "style": style_of(item_id),
            "file": "covers/%s.jpg" % item_id, "thumb": "thumbs/covers/%s.jpg" % item_id,
            "width": COVER_W, "height": COVER_H,
            "tags": [name, "封面", "背景", motif, STYLE_NAMES[style_of(item_id)]]}


# ── LOGO 标记 ─────────────────────────────────────────────────────────
def _mark_layer(size):
    return Image.new("RGBA", (size, size), (0, 0, 0, 0))


def mark_building(size):
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    w = int(size * 0.05)
    d.rounded_rectangle([size * 0.26, size * 0.18, size * 0.74, size * 0.82],
                        radius=int(size * 0.04), outline=(255, 255, 255, 255), width=w)
    d.line([(size * 0.20, size * 0.82), (size * 0.80, size * 0.82)],
           fill=(255, 255, 255, 255), width=w)
    for r in range(2):
        for c in range(3):
            x = size * (0.34 + c * 0.14)
            y = size * (0.28 + r * 0.20)
            d.rectangle([x, y, x + size * 0.08, y + size * 0.10],
                        outline=(255, 255, 255, 230), width=int(w * 0.7))
    return lay


def mark_bolt(size):
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    d.polygon([(size * 0.56, size * 0.14), (size * 0.30, size * 0.54),
               (size * 0.46, size * 0.54), (size * 0.40, size * 0.86),
               (size * 0.70, size * 0.44), (size * 0.53, size * 0.44)],
              fill=(255, 255, 255, 255))
    return lay


def mark_gear(size):
    import math
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx = cy = size / 2
    r_out, r_in = size * 0.30, size * 0.21
    for i in range(8):
        a = math.radians(i * 45)
        tx, ty = cx + r_out * 0.92 * math.cos(a), cy + r_out * 0.92 * math.sin(a)
        tooth = Image.new("RGBA", (int(size * 0.12), int(size * 0.12)), (0, 0, 0, 0))
        ImageDraw.Draw(tooth).rounded_rectangle(
            [0, 0, size * 0.12 - 1, size * 0.12 - 1], radius=int(size * 0.02),
            fill=(255, 255, 255, 255))
        tooth = tooth.rotate(-i * 45, expand=True, resample=Image.BICUBIC)
        lay.alpha_composite(tooth, (int(tx - tooth.width / 2), int(ty - tooth.height / 2)))
    d.ellipse([cx - r_out, cy - r_out, cx + r_out, cy + r_out],
              outline=(255, 255, 255, 255), width=int(size * 0.05))
    d.ellipse([cx - r_in, cy - r_in, cx + r_in, cy + r_in],
              outline=(255, 255, 255, 255), width=int(size * 0.05))
    return lay


def mark_shield(size):
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, s = size / 2, size * 0.48, size * 0.30
    pts = [(cx, cy - s), (cx + s * 0.85, cy - s * 0.62), (cx + s * 0.85, cy + s * 0.18),
           (cx, cy + s), (cx - s * 0.85, cy + s * 0.18), (cx - s * 0.85, cy - s * 0.62)]
    d.line(pts + [pts[0]], fill=(255, 255, 255, 255), width=int(size * 0.045), joint="curve")
    d.line([(cx - s * 0.38, cy - s * 0.02), (cx - s * 0.08, cy + s * 0.28),
            (cx + s * 0.44, cy - s * 0.34)],
           fill=(255, 255, 255, 255), width=int(size * 0.05), joint="curve")
    return lay


def mark_hex(size):
    import math
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx = cy = size / 2
    for r, wd in ((size * 0.30, int(size * 0.05)), (size * 0.17, int(size * 0.045))):
        pts = [(cx + r * math.cos(math.radians(60 * i + 30)),
                cy + r * math.sin(math.radians(60 * i + 30))) for i in range(6)]
        d.line(pts + [pts[0]], fill=(255, 255, 255, 255), width=wd, joint="curve")
    return lay


def mark_arrow(size):
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, r = size / 2, size / 2, size * 0.30
    d.ellipse([cx - r, cy - r, cx + r, cy + r],
              outline=(255, 255, 255, 255), width=int(size * 0.05))
    d.polygon([(cx, cy - r * 0.62), (cx + r * 0.42, cy + r * 0.02),
               (cx - r * 0.42, cy + r * 0.02)], fill=(255, 255, 255, 255))
    d.rectangle([cx - r * 0.14, cy + r * 0.02, cx + r * 0.14, cy + r * 0.58],
                fill=(255, 255, 255, 255))
    return lay


def mark_tower(size):
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    d.polygon([(size * 0.40, size * 0.16), (size * 0.60, size * 0.16),
               (size * 0.68, size * 0.82), (size * 0.32, size * 0.82)],
              outline=(255, 255, 255, 255), width=int(size * 0.045))
    for i in range(3):
        y = size * (0.34 + i * 0.16)
        d.line([(size * (0.40 - i * 0.015), y), (size * (0.60 + i * 0.015), y)],
               fill=(255, 255, 255, 230), width=int(size * 0.035))
    d.line([(size * 0.50, size * 0.16), (size * 0.50, size * 0.06)],
           fill=(255, 255, 255, 255), width=int(size * 0.035))
    return lay


def mark_orbit(size):
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy = size / 2, size / 2
    d.ellipse([cx - size * 0.13, cy - size * 0.13, cx + size * 0.13, cy + size * 0.13],
              fill=(255, 255, 255, 255))
    d.ellipse([cx - size * 0.32, cy - size * 0.18, cx + size * 0.32, cy + size * 0.18],
              outline=(255, 255, 255, 230), width=int(size * 0.04))
    d.ellipse([cx + size * 0.26, cy - size * 0.07, cx + size * 0.36, cy + size * 0.03],
              fill=(255, 255, 255, 255))
    return lay


def mark_diamond(size):
    """菱钻：双层菱形 + 棱线。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, s = size / 2, size * 0.48, size * 0.30
    pts = [(cx, cy - s), (cx + s * 0.72, cy), (cx, cy + s), (cx - s * 0.72, cy)]
    d.line(pts + [pts[0]], fill=(255, 255, 255, 255),
           width=int(size * 0.05), joint="curve")
    d.line([(cx, cy - s), (cx, cy + s)], fill=(255, 255, 255, 160),
           width=int(size * 0.03))
    d.line([(cx - s * 0.72, cy), (cx + s * 0.72, cy)],
           fill=(255, 255, 255, 160), width=int(size * 0.03))
    return lay


def mark_ringstar(size):
    """星环：五角星 + 外圈细环。"""
    import math
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, r = size / 2, size * 0.48, size * 0.26
    pts = []
    for i in range(10):
        rr = r if i % 2 == 0 else r * 0.42
        a = math.radians(-90 + i * 36)
        pts.append((cx + rr * math.cos(a), cy + rr * math.sin(a)))
    d.polygon(pts, fill=(255, 255, 255, 255))
    d.ellipse([cx - size * 0.34, cy - size * 0.34, cx + size * 0.34, cy + size * 0.34],
              outline=(255, 255, 255, 200), width=int(size * 0.035))
    return lay


def mark_peak(size):
    """山峰：双峰 + 地平线。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    d.line([(size * 0.20, size * 0.76), (size * 0.42, size * 0.34),
            (size * 0.58, size * 0.62), (size * 0.68, size * 0.48),
            (size * 0.84, size * 0.76)],
           fill=(255, 255, 255, 255), width=int(size * 0.05), joint="curve")
    d.line([(size * 0.16, size * 0.82), (size * 0.86, size * 0.82)],
           fill=(255, 255, 255, 255), width=int(size * 0.045))
    return lay


def mark_flow(size):
    """流动：三条错位弧线。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    for i in range(3):
        y = size * (0.34 + i * 0.16)
        d.arc([size * 0.22, y - size * 0.10, size * 0.78, y + size * 0.10],
              200, 340, fill=(255, 255, 255, 255), width=int(size * 0.05))
    return lay


def mark_link(size):
    """链接：双环相扣。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    r = size * 0.20
    d.ellipse([size * 0.20, size * 0.30, size * 0.20 + 2 * r, size * 0.30 + 2 * r],
              outline=(255, 255, 255, 255), width=int(size * 0.055))
    d.ellipse([size * 0.60 - 2 * r + 2 * r, size * 0.30,
               size * 0.60 + 2 * r, size * 0.30 + 2 * r],
              outline=(255, 255, 255, 255), width=int(size * 0.055))
    return lay


def mark_cube(size):
    """立方：等距立方体线框。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, s = size / 2, size * 0.50, size * 0.26
    top = [(cx, cy - s), (cx + s, cy - s * 0.5), (cx, cy), (cx - s, cy - s * 0.5)]
    d.line(top + [top[0]], fill=(255, 255, 255, 255),
           width=int(size * 0.045), joint="curve")
    bot = [(cx, cy), (cx + s, cy + s * 0.5), (cx, cy + s), (cx - s, cy + s * 0.5)]
    d.line(bot + [bot[0]], fill=(255, 255, 255, 255),
           width=int(size * 0.045), joint="curve")
    d.line([(cx, cy - s), (cx, cy)], fill=(255, 255, 255, 200),
           width=int(size * 0.04))
    d.line([(cx + s, cy - s * 0.5), (cx + s, cy + s * 0.5)],
           fill=(255, 255, 255, 200), width=int(size * 0.04))
    d.line([(cx - s, cy - s * 0.5), (cx - s, cy + s * 0.5)],
           fill=(255, 255, 255, 200), width=int(size * 0.04))
    return lay


def mark_wave(size):
    """波浪：三条错位正弦线。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    for i in range(3):
        y = size * (0.36 + i * 0.15)
        amp = size * 0.07
        pts = [(x, y + amp * math.sin(x / size * 6.0 + i)) for x in range(0, size + 4, 4)]
        d.line(pts, fill=(255, 255, 255, 255), width=int(size * 0.045), joint="curve")
    return lay


def mark_flame(size):
    """火焰：水滴形火苗 + 内焰。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, s = size / 2, size * 0.30
    outer = [(cx, size * 0.16), (cx + s * 0.78, size * 0.52),
             (cx + s * 0.62, size * 0.84), (cx, size * 0.86),
             (cx - s * 0.62, size * 0.84), (cx - s * 0.78, size * 0.52)]
    d.polygon(outer, fill=(255, 255, 255, 255))
    d.polygon([(cx, size * 0.44), (cx + s * 0.34, size * 0.66),
               (cx, size * 0.80), (cx - s * 0.34, size * 0.66)],
              fill=(0, 0, 0, 90))
    return lay


def mark_leaf(size):
    """叶片：对称叶形 + 主脉。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, s = size / 2, size / 2, size * 0.30
    d.polygon([(cx - s * 0.85, cy + s * 0.85), (cx + s * 0.85, cy - s * 0.85),
               (cx + s * 0.85, cy + s * 0.20), (cx - s * 0.85, cy + s * 0.20)],
              fill=(255, 255, 255, 255))
    d.line([(cx - s * 0.85, cy + s * 0.85), (cx + s * 0.85, cy - s * 0.85)],
           fill=(0, 0, 0, 70), width=int(size * 0.035))
    return lay


def mark_drop(size):
    """水滴：圆底尖顶 + 高光。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, s = size / 2, size * 0.54, size * 0.28
    d.polygon([(cx, cy - s), (cx + s * 0.86, cy + s * 0.30),
               (cx, cy + s), (cx - s * 0.86, cy + s * 0.30)],
              fill=(255, 255, 255, 255))
    d.ellipse([cx - s * 0.62, cy + s * 0.02, cx + s * 0.62, cy + s * 1.0],
              fill=(255, 255, 255, 255))
    d.ellipse([cx - s * 0.34, cy - s * 0.02, cx - s * 0.06, cy + s * 0.30],
              fill=(0, 0, 0, 60))
    return lay


def mark_crown(size):
    """冠冕：三尖王冠 + 底座。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    w = int(size * 0.05)
    pts = [(size * 0.20, size * 0.72), (size * 0.24, size * 0.30),
           (size * 0.38, size * 0.52), (size * 0.50, size * 0.22),
           (size * 0.62, size * 0.52), (size * 0.76, size * 0.30),
           (size * 0.80, size * 0.72)]
    d.line(pts, fill=(255, 255, 255, 255), width=w, joint="curve")
    d.line([(size * 0.20, size * 0.72), (size * 0.80, size * 0.72)],
           fill=(255, 255, 255, 255), width=w)
    d.line([(size * 0.18, size * 0.84), (size * 0.82, size * 0.84)],
           fill=(255, 255, 255, 220), width=int(w * 0.8))
    return lay


def mark_spiral(size):
    """螺旋：等距阿基米德螺线（半径收在标记区内，不顶边）。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy = size / 2, size / 2
    pts = []
    for i in range(240):
        t = i / 240 * math.pi * 6
        r = size * 0.04 + t * size * 0.017
        pts.append((cx + r * math.cos(t), cy + r * math.sin(t)))
    d.line(pts, fill=(255, 255, 255, 255), width=int(size * 0.045), joint="curve")
    return lay


def mark_anchor(size):
    """船锚：航运/港航/海事。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, s = size / 2, size / 2, size * 0.30
    lw = max(2, int(size * 0.05))
    d.line([(cx, cy - s * 0.80), (cx, cy + s * 0.78)], fill=(255, 255, 255, 255), width=lw)
    d.line([(cx - s * 0.55, cy - s * 0.42), (cx + s * 0.55, cy - s * 0.42)],
           fill=(255, 255, 255, 255), width=lw)
    d.arc([cx - s * 0.85, cy + s * 0.02, cx + s * 0.85, cy + s * 1.32],
          start=0, end=180, fill=(255, 255, 255, 255), width=lw)
    d.ellipse([cx - s * 0.17, cy - s * 1.12, cx + s * 0.17, cy - s * 0.70],
              outline=(255, 255, 255, 255), width=lw)
    return lay


def mark_bridge(size):
    """拱桥：基建/工程/联通。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, s = size / 2, size / 2, size * 0.32
    lw = max(2, int(size * 0.055))
    d.line([(cx - s * 1.12, cy + s * 0.32), (cx + s * 1.12, cy + s * 0.32)],
           fill=(255, 255, 255, 255), width=lw)
    d.arc([cx - s * 0.85, cy - s * 0.72, cx + s * 0.85, cy + s * 0.96],
          start=180, end=360, fill=(255, 255, 255, 255), width=lw)
    for sx in (-0.85, 0.85):
        d.line([(cx + s * sx, cy + s * 0.32), (cx + s * sx, cy + s * 0.96)],
               fill=(255, 255, 255, 255), width=lw)
    return lay


def mark_compass(size):
    """罗盘：导航/咨询/方向。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, r = size / 2, size / 2, size * 0.34
    d.ellipse([cx - r, cy - r, cx + r, cy + r],
              outline=(255, 255, 255, 255), width=max(2, int(size * 0.045)))
    p = size * 0.17
    d.polygon([(cx, cy - p), (cx + p * 0.5, cy), (cx, cy + p), (cx - p * 0.5, cy)],
              fill=(255, 255, 255, 255))
    return lay


def mark_brackets(size):
    """尖括号：软件/互联网/研发。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, s = size / 2, size / 2, size * 0.30
    lw = max(2, int(size * 0.055))
    d.line([(cx - s * 0.30, cy - s * 0.62), (cx - s * 1.05, cy), (cx - s * 0.30, cy + s * 0.62)],
           fill=(255, 255, 255, 255), width=lw, joint="curve")
    d.line([(cx + s * 0.30, cy - s * 0.62), (cx + s * 1.05, cy), (cx + s * 0.30, cy + s * 0.62)],
           fill=(255, 255, 255, 255), width=lw, joint="curve")
    d.line([(cx - s * 0.18, cy + s * 0.55), (cx + s * 0.18, cy - s * 0.55)],
           fill=(255, 255, 255, 255), width=lw)
    return lay


def mark_circles(size):
    """三圆环：协作/团队/生态。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    cx, cy, r = size / 2, size / 2, size * 0.26
    off = r * 0.82
    lw = max(2, int(size * 0.05))
    for dx, dy in ((-off, off * 0.5), (off, off * 0.5), (0, -off * 0.85)):
        d.ellipse([cx + dx - r, cy + dy - r, cx + dx + r, cy + dy + r],
                  outline=(255, 255, 255, 255), width=lw)
    return lay


def mark_grid(size):
    """九宫格：平台/系统/矩阵。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    s = size * 0.30
    cx, cy = size / 2, size / 2
    cell, gap = s * 0.58, s * 0.10
    origin = cell * 1.5 + gap
    for r in range(3):
        for c in range(3):
            x0 = cx - origin + c * (cell + gap)
            y0 = cy - origin + r * (cell + gap)
            d.rounded_rectangle([x0, y0, x0 + cell, y0 + cell],
                                radius=cell * 0.22, fill=(255, 255, 255, 255))
    return lay


def mark_person(size):
    """白色人形剪影（头 + 肩），用于默认头像。"""
    lay = _mark_layer(size)
    d = ImageDraw.Draw(lay)
    d.ellipse([size * 0.36, size * 0.14, size * 0.64, size * 0.42], fill=(255, 255, 255, 255))
    d.pieslice([size * 0.18, size * 0.46, size * 0.82, size * 1.10], 180, 360,
               fill=(255, 255, 255, 255))
    return lay


def make_avatar(item_id, name, colors):
    S = 256
    tile = rounded_tile(S, *colors, radius_ratio=0.22)
    tile.alpha_composite(mark_person(S), (0, 0))
    tile.save(os.path.join(OUT, "avatars", item_id + ".png"), "PNG")
    return {"id": item_id, "name": name, "category": "avatar",
            "style": style_of(item_id),
            "file": "avatars/%s.png" % item_id, "thumb": "avatars/%s.png" % item_id,
            "width": S, "height": S,
            "tags": [name, "头像", "默认", STYLE_NAMES[style_of(item_id)]]}


LOGO_MARKS = {
    "building": mark_building, "bolt": mark_bolt, "gear": mark_gear,
    "shield": mark_shield, "hex": mark_hex, "arrow": mark_arrow,
    "tower": mark_tower, "orbit": mark_orbit, "diamond": mark_diamond,
    "ringstar": mark_ringstar, "peak": mark_peak, "flow": mark_flow,
    "link": mark_link, "cube": mark_cube, "wave": mark_wave, "flame": mark_flame,
    "leaf": mark_leaf, "drop": mark_drop, "crown": mark_crown, "spiral": mark_spiral,
    "anchor": mark_anchor, "bridge": mark_bridge, "compass": mark_compass,
    "brackets": mark_brackets, "circles": mark_circles, "grid": mark_grid,
}


def make_logo(item_id, name, colors, mark):
    S = 512
    tile = rounded_tile(S, *colors, radius_ratio=0.22)
    tile.alpha_composite(LOGO_MARKS[mark](S), (0, 0))
    path = os.path.join(OUT, "logos", item_id + ".png")
    tile.save(path, "PNG")
    return {"id": item_id, "name": name, "category": "logo",
            "style": style_of(item_id),
            "file": "logos/%s.png" % item_id, "thumb": "logos/%s.png" % item_id,
            "width": S, "height": S,
            "tags": [name, "LOGO", "标识", mark, STYLE_NAMES[style_of(item_id)]]}


# ── 小程序首页背景图（750×1334 竖幅，深色高端商务风） ───────────────────
def _radial_fade(size, cx, cy, radius, power=1.7, peak=255):
    """平滑径向衰减图（L 模式）：(cx,cy) 处 peak，radius 处 0。
    幂函数缓出保证边缘一阶导为 0（无圆形硬边）；低分辨率计算 + LANCZOS 放大，快且平滑。"""
    w, h = size
    sw = 128
    sh = max(8, int(sw * h / w))
    px = []
    for y in range(sh):
        dy = (y + 0.5) / sh * h - cy
        for x in range(sw):
            dx = (x + 0.5) / sw * w - cx
            t = min(1.0, (dx * dx + dy * dy) ** 0.5 / radius)
            px.append(int(peak * (1 - t) ** power))
    small = Image.new("L", (sw, sh))
    small.putdata(px)
    return small.resize((w, h), Image.LANCZOS)


def _radial_glow(size, center, radius, color=(255, 255, 255), max_alpha=90):
    """径向光晕层：center 最亮，向边缘平滑消失（无硬边）。"""
    w, h = size
    a = _radial_fade(size, center[0], center[1], radius)
    a = a.point(lambda v: int(v * max_alpha / 255))
    glow = Image.new("RGBA", (w, h), color + (0,))
    glow.putalpha(a)
    return glow


def _vignette(img, strength=64):
    """四角暗角：中心亮、边缘暗，椭圆衰减 + 幂缓出，无环形接缝。"""
    w, h = img.size
    a = _radial_fade(img.size, w * 0.5, h * 0.5, max(w, h) * 0.80)
    a = a.point(lambda v: int((255 - v) * strength / 255))
    black = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    black.putalpha(a)
    img.alpha_composite(black)


def bg_stars(img):
    """深空：上段星云光晕 + 随机星点。"""
    w, h = img.size
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.14), w * 0.95,
                                     (96, 165, 250), 66))
    ov, d = overlay(img)
    random.seed(101)
    for _ in range(120):
        x, y = random.randint(0, w), random.randint(0, int(h * 0.75))
        r = random.choice([1, 1, 2, 2, 3])
        d.ellipse([x - r, y - r, x + r, y + r],
                  fill=(255, 255, 255, random.randint(70, 210)))
    img.alpha_composite(ov)


def bg_mountain(img):
    """山峦：下段三层山脉剪影。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(103)
    for base, alpha in ((0.66, 70), (0.76, 100), (0.86, 140)):
        pts = [(0, h)]
        x, peak = 0, h * base
        while x < w:
            step = random.randint(120, 220)
            ph = random.randint(60, 200)
            pts += [(x + step / 2, peak - ph), (x + step, peak)]
            x += step
        pts += [(w, h)]
        d.polygon(pts, fill=(255, 255, 255, alpha))
    img.alpha_composite(ov)


def bg_city(img):
    """城市夜景：底部楼宇剪影 + 暖色窗格灯火。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(107)
    x = -30
    while x < w + 30:
        bw = random.randint(90, 170)
        bh = random.randint(int(h * 0.20), int(h * 0.46))
        top = h - bh
        d.rectangle([x, top, x + bw, h], fill=(0, 0, 0, 130))
        for wy in range(top + 18, h - 16, 30):
            for wx in range(x + 14, x + bw - 12, 28):
                if random.random() < 0.38:
                    d.rectangle([wx, wy, wx + 12, wy + 14],
                                fill=(255, 214, 130, random.randint(110, 220)))
        x += bw + random.randint(12, 34)
    img.alpha_composite(ov)


def bg_bokeh(img):
    """光斑：大幅面柔光圆斑。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(109)
    for _ in range(22):
        r = random.randint(40, 150)
        x = random.randint(-60, w + 60)
        y = random.randint(-40, int(h * 0.85))
        a = random.randint(14, 52)
        d.ellipse([x - r, y - r, x + r, y + r], fill=(255, 255, 255, a))
        d.ellipse([x - r, y - r, x + r, y + r],
                  outline=(255, 255, 255, a + 26), width=3)
    img.alpha_composite(ov)


def bg_waves(img):
    """涟漪：同心弧线自下而上展开。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy = w * 0.5, h * 1.02
    for i, r in enumerate(range(140, 1150, 95)):
        d.arc([cx - r, cy - r, cx + r, cy + r], 200, 340,
              fill=(255, 255, 255, 46 + (i % 3) * 26), width=6)
    img.alpha_composite(ov)


def bg_grid(img):
    """格线：下段透视网格 + 地平线。"""
    ov, d = overlay(img)
    w, h = img.size
    horizon = int(h * 0.58)
    for i in range(-10, 11):
        d.line([(w / 2 + i * 40, horizon), (w / 2 + i * 300, h)],
               fill=(255, 255, 255, 40), width=2)
    for j in range(1, 9):
        y = horizon + (h - horizon) * (j / 9) ** 1.6
        d.line([(0, y), (w, y)], fill=(255, 255, 255, 40), width=2)
    d.line([(0, horizon), (w, horizon)], fill=(255, 255, 255, 110), width=2)
    img.alpha_composite(ov)


def bg_diagonal(img):
    """斜纹：细密斜线条纹。"""
    ov, d = overlay(img)
    w, h = img.size
    gap, width = 46, 10
    x = -h
    while x < w + h:
        d.line([(x, 0), (x + h, h)], fill=(255, 255, 255, 30), width=width)
        d.line([(x + gap, 0), (x + gap + h, h)], fill=(255, 255, 255, 14), width=width)
        x += gap * 2
    img.alpha_composite(ov)


def bg_glow(img):
    """光晕：中央强径向光 + 底部辅光。"""
    w, h = img.size
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.32), w * 1.0,
                                     (165, 180, 252), 96))
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.94), w * 0.8,
                                     (99, 102, 241), 52))


def bg_gold(img):
    """金线：金色细框 + 竖向金线（奢华）。"""
    ov, d = overlay(img)
    w, h = img.size
    gold = (212, 175, 55, 210)
    m = 40
    d.rectangle([m, m, w - m, h - m], outline=gold, width=3)
    d.rectangle([m + 14, m + 14, w - m - 14, h - m - 14],
                outline=(212, 175, 55, 80), width=1)
    for x in (w * 0.5,):
        d.line([(x, m + 40), (x, h - m - 40)], fill=(212, 175, 55, 60), width=2)
    d.ellipse([w * 0.5 - 10, h * 0.30 - 10, w * 0.5 + 10, h * 0.30 + 10], fill=gold)
    img.alpha_composite(ov)


def bg_mesh(img):
    """星链：节点 + 邻近连线（网络感）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(127)
    nodes = [(random.randint(30, w - 30), random.randint(60, int(h * 0.8)))
             for _ in range(26)]
    for i, (x1, y1) in enumerate(nodes):
        for x2, y2 in nodes[i + 1:]:
            dist2 = (x1 - x2) ** 2 + (y1 - y2) ** 2
            if dist2 < 260 ** 2:
                d.line([(x1, y1), (x2, y2)], fill=(255, 255, 255, 54), width=2)
    for x, y in nodes:
        r = random.choice([4, 5, 6])
        d.ellipse([x - r, y - r, x + r, y + r],
                  fill=(255, 255, 255, random.randint(120, 220)))
    img.alpha_composite(ov)


def bg_aurora(img):
    """极光：两条斜向柔光带。"""
    w, h = img.size
    for cx, cy, color, alpha in (
        (w * 0.30, h * 0.28, (94, 234, 212), 78),
        (w * 0.72, h * 0.50, (56, 189, 248), 62),
    ):
        band = Image.new("RGBA", (w, h), (0, 0, 0, 0))
        bd = ImageDraw.Draw(band)
        bd.line([(cx - w * 0.5, cy + h * 0.24), (cx + w * 0.5, cy - h * 0.24)],
                fill=color + (alpha,), width=int(h * 0.20))
        band = band.filter(ImageFilter.GaussianBlur(80))
        img.alpha_composite(band)


def bg_sand(img):
    """流沙：层叠沙丘曲线。"""
    ov, d = overlay(img)
    w, h = img.size
    for base, alpha in ((0.62, 46), (0.74, 70), (0.88, 100)):
        pts = [(0, h)]
        x = 0
        while x <= w:
            t = x / w
            y = h * base - math.sin(t * math.pi * 1.6 + base * 9) * h * 0.05
            pts.append((x, y))
            x += 30
        pts += [(w, h)]
        d.polygon(pts, fill=(255, 255, 255, alpha))
    img.alpha_composite(ov)


def bg_canyon(img):
    """峡谷：两侧暖色岩壁夹一道光。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(157)
    for side in (0, 1):
        pts = [(0 if side == 0 else w, 0)]
        for i in range(9):
            y = i * h / 8
            inset = random.randint(60, 230) if side == 0 else w - random.randint(60, 230)
            pts.append((inset, y))
        pts.append((0 if side == 0 else w, h))
        d.polygon(pts, fill=(0, 0, 0, 120))
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.30), w * 0.9,
                                     (255, 214, 170), 70))


def bg_forest(img):
    """森林：深浅两层树干剪影 + 顶部透光。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(163)
    for depth, alpha, wdt in ((0, 150, (26, 46)), (1, 90, (14, 28))):
        x = -40
        while x < w + 40:
            bw = random.randint(*wdt)
            d.rectangle([x, 0, x + bw, h], fill=(0, 0, 0, alpha))
            x += random.randint(90, 210)
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.06), w * 1.0,
                                     (187, 247, 208), 54))


def bg_ink(img):
    """水墨（浅色）：淡墨晕染 + 飞白点，留白充足。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(167)
    for _ in range(7):
        r = random.randint(90, 260)
        x, y = random.randint(0, w), random.randint(int(h * 0.15), int(h * 0.85))
        d.ellipse([x - r, y - r * 0.7, x + r, y + r * 0.7],
                  fill=(39, 39, 42, random.randint(14, 34)))
    for _ in range(60):
        x, y = random.randint(0, w), random.randint(0, h)
        r = random.randint(1, 4)
        d.ellipse([x - r, y - r, x + r, y + r], fill=(39, 39, 42, random.randint(40, 120)))
    img.alpha_composite(ov)


def bg_geo(img):
    """几何：大块半透明多边形拼接。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(173)
    for _ in range(9):
        cx, cy = random.randint(0, w), random.randint(0, h)
        r = random.randint(160, 380)
        n = random.randint(3, 6)
        pts = [(cx + r * math.cos(math.radians(360 / n * i + random.randint(0, 60))),
                cy + r * math.sin(math.radians(360 / n * i + random.randint(0, 60))))
               for i in range(n)]
        d.polygon(pts, fill=(255, 255, 255, random.randint(16, 46)))
    img.alpha_composite(ov)


def bg_lines(img):
    """光束线：横向细线自上而下渐隐。"""
    ov, d = overlay(img)
    w, h = img.size
    for i in range(46):
        y = int(h * 0.10 + i * (h * 0.9 / 46))
        a = int(90 * (1 - i / 46) ** 1.4)
        d.line([(0, y), (w, y)], fill=(255, 255, 255, a), width=2)
    img.alpha_composite(ov)


def bg_nebula(img):
    """星云：多团彩色光晕叠加 + 细星点。"""
    w, h = img.size
    for cx, cy, r, col, a in (
        (w * 0.30, h * 0.26, w * 0.80, (129, 140, 248), 60),
        (w * 0.72, h * 0.48, w * 0.72, (244, 114, 182), 50),
        (w * 0.44, h * 0.76, w * 0.66, (56, 189, 248), 44),
    ):
        img.alpha_composite(_radial_glow(img.size, (cx, cy), r, col, a))
    ov, d = overlay(img)
    random.seed(179)
    for _ in range(150):
        x, y = random.randint(0, w), random.randint(0, h)
        r = random.choice([1, 1, 2])
        d.ellipse([x - r, y - r, x + r, y + r],
                  fill=(255, 255, 255, random.randint(60, 190)))
    img.alpha_composite(ov)


def bg_noir(img):
    """暗夜：近黑底 + 单点柔光（极简克制）。"""
    w, h = img.size
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.34), w * 0.85,
                                     (228, 228, 231), 46))


def bg_ocean(img):
    """深海：自上而下的光束 + 悬浮微粒。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(181)
    for _ in range(9):
        x = random.randint(0, w)
        wid = random.randint(40, 130)
        d.polygon([(x, 0), (x + wid, 0), (x + wid + 180, h), (x + 150, h)],
                  fill=(255, 255, 255, random.randint(14, 40)))
    for _ in range(70):
        x, y = random.randint(0, w), random.randint(0, h)
        r = random.randint(1, 3)
        d.ellipse([x - r, y - r, x + r, y + r],
                  fill=(255, 255, 255, random.randint(50, 140)))
    img.alpha_composite(ov)


def bg_pearl(img):
    """珠光（浅色）：柔和彩虹光晕 + 细腻珠光斑。"""
    w, h = img.size
    for cx, cy, r, col, a in (
        (w * 0.28, h * 0.24, w * 0.85, (244, 114, 182), 30),
        (w * 0.74, h * 0.52, w * 0.85, (56, 189, 248), 26),
        (w * 0.42, h * 0.84, w * 0.85, (167, 139, 250), 24),
    ):
        img.alpha_composite(_radial_glow(img.size, (cx, cy), r, col, a))


def bg_ridge(img):
    """山脊：细线层叠山脊（冷峻理性）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(191)
    for i in range(6):
        base = h * (0.40 + i * 0.09)
        pts = []
        for x in range(0, w + 24, 24):
            y = base - abs(math.sin(x / 190 + i * 1.3)) * h * 0.10 - \
                random.randint(0, 16)
            pts.append((x, y))
        d.line(pts, fill=(255, 255, 255, 36 + i * 16), width=3)
    img.alpha_composite(ov)


def bg_tech(img):
    """科技：电路板走线 + 节点。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(193)
    for _ in range(18):
        x, y = random.randint(0, w), random.randint(0, h)
        pts = [(x, y)]
        for _ in range(random.randint(2, 5)):
            if random.random() < 0.5:
                x += random.choice([-1, 1]) * random.randint(70, 200)
            else:
                y += random.choice([-1, 1]) * random.randint(70, 200)
            pts.append((x, y))
        d.line(pts, fill=(255, 255, 255, random.randint(40, 110)), width=3, joint="curve")
        for px, py in pts:
            d.ellipse([px - 5, py - 5, px + 5, py + 5], fill=(255, 255, 255, 170))
    img.alpha_composite(ov)


def bg_linen(img):
    """亚麻（浅色）：细密织纹 + 柔和斜光。"""
    ov, d = overlay(img)
    w, h = img.size
    for x in range(0, w, 6):
        d.line([(x, 0), (x, h)], fill=(120, 113, 108, 16), width=1)
    for y in range(0, h, 6):
        d.line([(0, y), (w, y)], fill=(120, 113, 108, 12), width=1)
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (w * 0.30, h * 0.18), w * 1.0,
                                     (255, 251, 235), 60))


def bg_marble(img):
    """大理石：斜向纹脉（建材/家居/高端）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(173)
    for _ in range(26):
        x, y = random.randint(-w // 4, w), random.randint(-h // 4, h)
        pts = [(x, y)]
        for _ in range(6):
            x += random.randint(-int(w * 0.22), int(w * 0.22))
            y += random.randint(int(h * 0.04), int(h * 0.16))
            pts.append((x, y))
        d.line(pts, fill=(255, 255, 255, random.randint(18, 46)),
               width=random.randint(1, 6), joint="curve")
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.22), w * 0.95,
                                     (255, 255, 255), 30))


def bg_silk(img):
    """丝绸：柔和 S 形波带（服装/珠宝/美业）。"""
    ov, d = overlay(img)
    w, h = img.size
    for i in range(9):
        base = h * (0.10 + i * 0.10)
        amp = h * 0.055
        pts = [(w * k / 24, base + math.sin(k / 24 * math.pi * 2 + i * 0.5) * amp)
               for k in range(25)]
        d.line(pts, fill=(255, 255, 255, 30 - i),
               width=max(1, int(h * 0.010)), joint="curve")
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.28), w * 0.9,
                                     (255, 255, 255), 40))


def bg_hexcell(img):
    """蜂巢：六边形网格（制造/工业/材料）。"""
    ov, d = overlay(img)
    w, h = img.size
    r = w * 0.115
    dx, dy = r * 1.5, r * math.sqrt(3)
    row, y = 0, -dy
    while y < h + dy:
        x = -dx + (0.75 * r if row % 2 else 0)
        while x < w + dx:
            pts = [(x + r * math.cos(math.pi / 3 * k), y + r * math.sin(math.pi / 3 * k))
                   for k in range(6)]
            d.line(pts + [pts[0]], fill=(255, 255, 255, 34), width=2)
            x += dx
        y += dy
        row += 1
    img.alpha_composite(ov)


def bg_dotmatrix(img):
    """点阵：规则圆点阵列（数据/科技/互联网）。"""
    ov, d = overlay(img)
    w, h = img.size
    step, row = 42, 0
    for y in range(0, h + step, step):
        ox = step / 2 if row % 2 else 0
        a = 46 if row % 2 else 30
        for x in range(0, w + step, step):
            d.ellipse([x + ox - 2.5, y - 2.5, x + ox + 2.5, y + 2.5],
                      fill=(255, 255, 255, a))
        row += 1
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.24), w * 0.95,
                                     (255, 255, 255), 34))


def bg_sunburst(img):
    """光芒：放射光束（能源/光伏/照明）。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy, rmax = w * 0.5, h * 0.24, w * 1.5
    for i in range(22):
        ang = i * (2 * math.pi / 22)
        d.line([(cx, cy), (cx + rmax * math.cos(ang), cy + rmax * math.sin(ang))],
               fill=(255, 255, 255, 26), width=int(w * 0.030))
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (cx, cy), w * 0.85,
                                     (255, 236, 170), 80))


def bg_topo(img):
    """等高线：层叠曲线（测绘/工程/地质）。"""
    ov, d = overlay(img)
    w, h = img.size
    for i in range(16):
        base = h * (0.06 + i * 0.062)
        amp = h * (0.03 + i * 0.006)
        pts = []
        for k in range(31):
            x = w * k / 30
            y = (base + math.sin(k / 30 * math.pi * 2 + i * 0.42) * amp
                 + math.sin(k / 30 * math.pi * 4 + i * 0.7) * amp * 0.35)
            pts.append((x, y))
        d.line(pts, fill=(255, 255, 255, 62 - i * 2), width=2, joint="curve")
    img.alpha_composite(ov)


def bg_bloom(img):
    """柔光绽放：多团柔光，带轻微色偏（美业/婚庆/教育）。"""
    w, h = img.size
    random.seed(179)
    tints = [(196, 181, 253), (249, 168, 212), (165, 180, 252),
             (253, 186, 116), (147, 197, 253)]
    for i in range(6):
        r = random.randint(int(w * 0.26), int(w * 0.44))
        cx = random.randint(int(w * 0.12), int(w * 0.88))
        cy = random.randint(int(h * 0.06), int(h * 0.88))
        img.alpha_composite(_radial_glow(img.size, (cx, cy), r, tints[i % 5],
                                         random.randint(90, 130)))
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.30), w * 0.75,
                                     (255, 255, 255), 36))


def bg_skyline_day(img):
    """晨曦天际线（浅色）：底部建筑剪影 + 低垂暖光（地产/建筑）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(181)
    x = -20
    while x < w + 20:
        bw = random.randint(60, 130)
        bh = random.randint(int(h * 0.10), int(h * 0.26))
        top = h - bh
        d.rectangle([x, top, x + bw, h], fill=(71, 85, 105, 105))
        for wy in range(top + 18, h - 16, 30):
            for wx in range(x + 12, x + bw - 12, 26):
                if random.random() < 0.35:
                    d.rectangle([wx, wy, wx + 10, wy + 13],
                                fill=(255, 255, 255, random.randint(70, 150)))
        x += bw + random.randint(8, 26)
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.16), w * 1.0,
                                     (255, 214, 170), 72))


def bg_ripple(img):
    """水波环：同心扁环（水务/环保/声学）。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy = w * 0.5, h * 0.36
    for i in range(12):
        a = 66 - i * 4
        if a <= 0:
            break
        r = w * (0.10 + i * 0.105)
        d.ellipse([cx - r, cy - r * 0.72, cx + r, cy + r * 0.72],
                  outline=(255, 255, 255, a), width=3)
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (cx, cy), w * 0.8,
                                     (190, 242, 255), 40))


def bg_arcstripe(img):
    """弧形条纹：宽弧带（金融/咨询/稳重）。"""
    ov, d = overlay(img)
    w, h = img.size
    cx, cy = w * 0.5, h * 0.18
    for i in range(7):
        r = w * (0.55 + i * 0.20)
        d.arc([cx - r, cy - r, cx + r, cy + r], start=20, end=160,
              fill=(255, 255, 255, 40 - i * 4), width=int(h * 0.075))
    img.alpha_composite(ov)


def bg_sparkdust(img):
    """星尘：细密亮点 + 中心柔光（科技/互联网）。"""
    ov, d = overlay(img)
    w, h = img.size
    random.seed(191)
    for _ in range(220):
        x, y = random.randint(0, w), random.randint(0, h)
        s = random.choice([1, 1, 2, 2, 3])
        d.ellipse([x - s, y - s, x + s, y + s],
                  fill=(255, 255, 255, random.randint(40, 190)))
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.22), w * 0.9,
                                     (199, 210, 254), 46))


def bg_weave(img):
    """织纹（浅色）：经纬交错细线（纺织/服装/手作）。"""
    ov, d = overlay(img)
    w, h = img.size
    step = 26
    for x in range(0, w + step, step):
        d.line([(x, 0), (x, h)], fill=(120, 110, 100, 26), width=3)
    for y in range(0, h + step, step):
        d.line([(0, y), (w, y)], fill=(120, 110, 100, 20), width=3)
    img.alpha_composite(ov)
    img.alpha_composite(_radial_glow(img.size, (w * 0.5, h * 0.20), w * 0.95,
                                     (255, 255, 255), 42))


BG_MOTIFS = {
    "stars": bg_stars, "mountain": bg_mountain, "city": bg_city, "bokeh": bg_bokeh,
    "waves": bg_waves, "grid": bg_grid, "diagonal": bg_diagonal, "glow": bg_glow,
    "gold": bg_gold, "mesh": bg_mesh, "aurora": bg_aurora, "sand": bg_sand,
    "canyon": bg_canyon, "forest": bg_forest, "ink": bg_ink, "geo": bg_geo,
    "lines": bg_lines, "nebula": bg_nebula, "noir": bg_noir, "ocean": bg_ocean,
    "pearl": bg_pearl, "ridge": bg_ridge, "tech": bg_tech, "linen": bg_linen,
    "marble": bg_marble, "silk": bg_silk, "hexcell": bg_hexcell,
    "dotmatrix": bg_dotmatrix, "sunburst": bg_sunburst, "topo": bg_topo,
    "bloom": bg_bloom, "skyline-day": bg_skyline_day, "ripple": bg_ripple,
    "arcstripe": bg_arcstripe, "sparkdust": bg_sparkdust, "weave": bg_weave,
}


def _lum(c):
    """感知亮度 0~255（ITU-R BT.601）。"""
    return 0.299 * c[0] + 0.587 * c[1] + 0.114 * c[2]


def make_background(item_id, name, colors, motif, tags):
    """背景图：竖幅渐变 + motif + 暗角；JPEG 原图 + 缩略图（选择器预览）。
    浅色图（平均亮度 >180）跳过暗角——浅蓝底压暗四角会显脏，原型图的浅蓝
    背景要的是通透感。"""
    img = vgrad((BG_W, BG_H), *colors).convert("RGBA")
    BG_MOTIFS[motif](img)
    if (_lum(colors[0]) + _lum(colors[1])) / 2 < 180:
        _vignette(img)
    path = os.path.join(OUT, "backgrounds", item_id + ".jpg")
    img.convert("RGB").save(path, "JPEG", quality=87)
    thumb = img.convert("RGB").resize((BG_THUMB_W, BG_THUMB_H), Image.LANCZOS)
    tpath = os.path.join(OUT, "thumbs", "backgrounds", item_id + ".jpg")
    thumb.save(tpath, "JPEG", quality=80)
    return {"id": item_id, "name": name, "category": "background",
            "style": style_of(item_id),
            "file": "backgrounds/%s.jpg" % item_id,
            "thumb": "thumbs/backgrounds/%s.jpg" % item_id,
            "width": BG_W, "height": BG_H,
            "tags": tags + ["背景", "全屏", motif, STYLE_NAMES[style_of(item_id)]]}


# ── 海报绘制 ──────────────────────────────────────────────────────────
# 海报构图固定：上部标题区（主标题 + 副标题 + 印章）+ 下部信息区（压深，
# 供小程序 canvas 用浅色文字绘制员工信息）。标题烧进图里、员工信息实时画，
# 这样一个模板能给所有员工复用，不必为每人出一张图。
#
# 字体：找不到候选字体时返回 None → 退化为无标题版（只出渐变 + 装饰），
# 不阻塞素材库生成（跨机器跑脚本时字体路径差异是常态）。

_FONT_CACHE = {}


def _load_font(candidates, size):
    """按候选列表加载字体（带缓存）；全部缺失返回 None。"""
    key = (tuple(candidates), size)
    if key in _FONT_CACHE:
        return _FONT_CACHE[key]
    font = None
    for path, index in candidates:
        if not os.path.exists(path):
            continue
        try:
            font = ImageFont.truetype(path, size, index=index)
            break
        except Exception as exc:
            # 不能静默：字体一旦取不到，海报会退化成没有标题的纯渐变图，
            # 不报错的话很难发现（踩过一次：ImageFont 忘了 import）。
            print("  [warn] 字体加载失败 %s#%d: %s" % (path, index, exc))
            continue
    if font is None:
        print("  [warn] 没有可用字体，标题将不绘制：%s" % (candidates,))
    _FONT_CACHE[key] = font
    return font


def serif_font(size):
    return _load_font(_SERIF_CANDIDATES, size)


def sans_font(size):
    return _load_font(_SANS_CANDIDATES, size)


def fit_font(candidates, text, size, max_width, min_size=56):
    """找一个不超 max_width 的字号；返回 (font, size)，font 为 None 表示无字体可用。

    四字标题（"合作共赢"/"诚聘英才"）在 96px 下宽 384px，会撞上右侧的齿轮/太阳
    装饰。按可用宽度自动缩号，比固定字号再让标题去躲装饰更稳（换配色也不会失效）。
    """
    font = _load_font(candidates, size)
    probe = ImageDraw.Draw(Image.new("RGB", (1, 1)))
    while font and size > min_size:
        try:
            w = probe.textlength(text, font=font)
        except Exception:
            break
        if w <= max_width:
            break
        size -= 4
        font = _load_font(candidates, size)
    return font, size


def _poster_moon(d, cx, cy, r, color=(255, 248, 224)):
    """满月：多层低 alpha 圆叠出光晕，再压两处淡斑。"""
    for i in range(38, 0, -1):
        rr = r * (1 + i * 0.027)
        d.ellipse([cx - rr, cy - rr, cx + rr, cy + rr],
                  fill=color + (min(58, int(3 + i * 0.55)),))
    d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=color + (246,))
    d.ellipse([cx - r * 0.38, cy - r * 0.32, cx - r * 0.02, cy + r * 0.16],
              fill=(224, 212, 176, 78))
    d.ellipse([cx + r * 0.16, cy + r * 0.28, cx + r * 0.44, cy + r * 0.52],
              fill=(224, 212, 176, 58))


def _poster_snow(d, seed=2, count=26, rmin=12, rmax=46):
    """雪花：六向主枝 + 两级分叉（分枝才有雪的辨识度）。"""
    random.seed(seed)
    for _ in range(count):
        cx = random.randint(15, POSTER_W - 15)
        cy = random.randint(10, POSTER_H - 15)
        r = random.randint(rmin, rmax)
        col = (255, 255, 255, random.randint(60, 185))
        for k in range(6):
            ang = math.pi / 3 * k + random.uniform(-0.08, 0.08)
            d.line([(cx, cy), (cx + r * math.cos(ang), cy + r * math.sin(ang))],
                   fill=col, width=2)
            for t in (0.42, 0.70):
                bx, by = cx + r * t * math.cos(ang), cy + r * t * math.sin(ang)
                for s in (-1, 1):
                    aa = ang + s * math.pi / 4.2
                    d.line([(bx, by),
                            (bx + r * 0.2 * math.cos(aa), by + r * 0.2 * math.sin(aa))],
                           fill=col, width=1)


def _poster_raindrops(d, seed=7, count=80):
    random.seed(seed)
    for _ in range(count):
        x = random.randint(10, POSTER_W - 10)
        y = random.randint(10, POSTER_H - 40)
        ln = random.randint(20, 54)
        d.line([(x, y), (x - 6, y + ln)],
               fill=(208, 234, 255, random.randint(55, 140)), width=2)


def _poster_thunder(d, seed=71):
    """惊蛰：一道折线闪电 + 稀落雨丝。闪电是主视觉，雨丝只作气氛。"""
    random.seed(seed)
    _poster_raindrops(d, seed=seed + 1, count=34)
    # 闪电折线（自左上劈向右下），外发光 + 实线两遍
    pts = [(196, 40), (150, 250), (232, 252), (128, 470),
           (176, 470), (300, 244), (216, 222), (262, 40)]
    d.line(pts, fill=(226, 216, 255, 52), width=26, joint="curve")
    d.line(pts, fill=(246, 242, 214, 210), width=9, joint="curve")
    d.line(pts, fill=(255, 255, 255, 240), width=3, joint="curve")


def _poster_willow(d, seed=3, n=7, leaf=(224, 252, 208)):
    """垂柳：枝条为二次贝塞尔采样，嫩叶沿枝排布（春的意象）。"""
    random.seed(seed)
    for i in range(n):
        x0 = random.randint(20, POSTER_W - 20)
        length = random.randint(320, 700)
        sway = random.uniform(45, 95) * (1 if i % 2 else -1)
        p0 = (x0, -20)
        c1 = (x0 + sway, length * 0.35)
        c2 = (x0 + sway * 0.5 - 30, length * 0.72)
        pts = []
        for k in range(44):
            t = k / 43
            u = 1 - t
            pts.append((u * u * p0[0] + 2 * u * t * c1[0] + t * t * c2[0],
                        u * u * p0[1] + 2 * u * t * c1[1] + t * t * c2[1]))
        d.line(pts, fill=leaf + (160,), width=3, joint="curve")
        for t in (0.28, 0.44, 0.58, 0.71, 0.83, 0.92):
            idx = min(43, int(t * 43))
            bx, by = pts[idx]
            for s in (-1, 1):
                lx, ly = bx + s * 9, by + 4
                d.ellipse([lx - 5, ly - 3, lx + 5, ly + 7], fill=leaf + (182,))


def _poster_petals(d, seed=5, n=34, color=(255, 214, 226)):
    """花瓣：椭圆旋转难做，用小圆点+短弧模拟桃花瓣。"""
    random.seed(seed)
    for _ in range(n):
        cx = random.randint(20, POSTER_W - 20)
        cy = random.randint(20, POSTER_H - 20)
        r = random.randint(5, 12)
        d.ellipse([cx - r, cy - r * 0.6, cx + r, cy + r * 0.6],
                  fill=color + (random.randint(70, 165),))


def _poster_fog(d, seed=9, bands=7):
    """雾：横向低对比色带（清明/晨雾）。"""
    random.seed(seed)
    for i in range(bands):
        y = 120 + i * 95 + random.randint(-20, 20)
        h = random.randint(26, 54)
        d.rectangle([0, y, POSTER_W, y + h], fill=(255, 255, 255, random.randint(10, 26)))


def _poster_sun(d, cx, cy, r, color=(255, 238, 176)):
    for i in range(34, 0, -1):
        rr = r * (1 + i * 0.035)
        d.ellipse([cx - rr, cy - rr, cx + rr, cy + rr],
                  fill=color + (min(50, int(2 + i * 0.5)),))
    d.ellipse([cx - r * 0.55, cy - r * 0.55, cx + r * 0.55, cy + r * 0.55],
              fill=color + (190,))


def _poster_wheat(d, seed=11, n=9, color=(246, 220, 140)):
    """麦穗：一根主茎 + 两侧对生麦粒。"""
    random.seed(seed)
    for i in range(n):
        x0 = random.randint(30, POSTER_W - 30)
        y0 = random.randint(120, POSTER_H - 260)
        h = random.randint(150, 260)
        d.line([(x0, y0), (x0 + random.randint(-18, 18), y0 - h)],
               fill=color + (150,), width=3)
        for k in range(9):
            t = k / 9
            yy = y0 - h * t
            rr = 7 + t * 6
            for s in (-1, 1):
                d.ellipse([x0 + s * rr - 4, yy - 7, x0 + s * rr + 4, yy + 7],
                          fill=color + (185,))


def _poster_leaves(d, seed=13, n=22, color=(232, 168, 84)):
    """落叶：斜置的椭圆叶 + 中脉。"""
    random.seed(seed)
    for _ in range(n):
        cx = random.randint(20, POSTER_W - 20)
        cy = random.randint(20, POSTER_H - 20)
        r = random.randint(11, 22)
        d.ellipse([cx - r, cy - r * 0.52, cx + r, cy + r * 0.52],
                  fill=color + (random.randint(85, 175),))
        d.line([(cx - r * 0.8, cy), (cx + r * 0.8, cy)],
               fill=(255, 240, 210, 120), width=1)


def _poster_dew(d, seed=17, n=26):
    """露珠：小圆 + 高光点。"""
    random.seed(seed)
    for _ in range(n):
        cx = random.randint(20, POSTER_W - 20)
        cy = random.randint(20, POSTER_H - 20)
        r = random.randint(5, 14)
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=(235, 248, 255, 120))
        d.ellipse([cx - r * 0.35, cy - r * 0.4, cx - r * 0.05, cy - r * 0.05],
                  fill=(255, 255, 255, 190))


def _poster_frost(d, seed=19, n=16):
    """霜花：六角晶体（比雪花更规整、更"结冰"）。"""
    random.seed(seed)
    for _ in range(n):
        cx = random.randint(30, POSTER_W - 30)
        cy = random.randint(30, POSTER_H - 30)
        r = random.randint(14, 34)
        col = (226, 244, 255, random.randint(70, 150))
        for k in range(6):
            ang = math.pi / 3 * k
            d.line([(cx, cy), (cx + r * math.cos(ang), cy + r * math.sin(ang))],
                   fill=col, width=2)
            for t in (0.55,):
                bx, by = cx + r * t * math.cos(ang), cy + r * t * math.sin(ang)
                for s in (-1, 1):
                    aa = ang + s * math.pi / 4
                    d.line([(bx, by),
                            (bx + r * 0.22 * math.cos(aa), by + r * 0.22 * math.sin(aa))],
                           fill=col, width=1)


def _poster_lantern(d, seed=21, n=5):
    """灯笼：吊绳 + 椭圆灯身 + 上下金箍 + 流苏。"""
    random.seed(seed)
    for i in range(n):
        cx = random.randint(70, POSTER_W - 70)
        cy = random.randint(60, 340)
        rw = random.randint(46, 74)
        rh = int(rw * 0.78)
        d.line([(cx, 0), (cx, cy - rh)],
               fill=(240, 200, 140, 130), width=2)
        d.ellipse([cx - rw, cy - rh, cx + rw, cy + rh], fill=(214, 54, 44, 175))
        d.ellipse([cx - rw * 0.72, cy - rh * 0.78, cx + rw * 0.72, cy + rh * 0.78],
                  outline=(255, 214, 150, 120), width=3)
        d.rectangle([cx - rw * 0.34, cy - rh - 9, cx + rw * 0.34, cy - rh + 7],
                    fill=(250, 216, 140, 210))
        d.rectangle([cx - rw * 0.34, cy + rh - 7, cx + rw * 0.34, cy + rh + 9],
                    fill=(250, 216, 140, 210))
        for s in (-1, 0, 1):
            d.line([(cx + s * 7, cy + rh + 9), (cx + s * 9, cy + rh + 44)],
                   fill=(250, 216, 140, 165), width=2)


def _poster_bamboo(d, seed=23, n=5, color=(150, 208, 140)):
    random.seed(seed)
    for i in range(n):
        x0 = random.randint(20, POSTER_W - 20)
        w = random.randint(14, 24)
        top = random.randint(-40, 120)
        d.rectangle([x0 - w, top, x0 + w, POSTER_H], fill=color + (95,))
        y = top + 40
        while y < POSTER_H:
            d.line([(x0 - w, y), (x0 + w, y)], fill=(40, 80, 50, 110), width=3)
            for s in (-1, 1):
                lx, ly = x0 + s * (w + 16), y + 18
                d.ellipse([lx - 22, ly - 7, lx + 22, ly + 7], fill=color + (135,))
            y += random.randint(90, 150)


def _poster_confetti(d, seed=29, n=54):
    """节庆彩带：短矩形 + 弧线飘带。"""
    random.seed(seed)
    palette = [(255, 214, 120), (255, 120, 120), (150, 220, 255),
               (200, 255, 190), (255, 180, 240), (255, 255, 255)]
    for _ in range(n):
        cx = random.randint(10, POSTER_W - 10)
        cy = random.randint(10, POSTER_H - 10)
        w = random.randint(7, 15)
        h = random.randint(12, 26)
        col = random.choice(palette)
        d.rectangle([cx - w, cy - h, cx + w, cy + h],
                    fill=col + (random.randint(90, 190),))


def _poster_gear(d, cx, cy, r, color=(255, 255, 255)):
    """齿轮：外齿环 + 中心孔（机电行业的符号）。"""
    teeth = 12
    for k in range(teeth):
        ang = math.pi * 2 / teeth * k
        tx, ty = cx + r * math.cos(ang), cy + r * math.sin(ang)
        d.ellipse([tx - r * 0.17, ty - r * 0.17, tx + r * 0.17, ty + r * 0.17],
                  fill=color + (110,))
    d.ellipse([cx - r * 0.82, cy - r * 0.82, cx + r * 0.82, cy + r * 0.82],
              outline=color + (95,), width=max(1, int(r * 0.14)))
    d.ellipse([cx - r * 0.3, cy - r * 0.3, cx + r * 0.3, cy + r * 0.3],
              outline=color + (80,), width=max(1, int(r * 0.1)))


def _poster_arcs(d, seed=31, n=10):
    random.seed(seed)
    for _ in range(n):
        r = random.randint(150, 640)
        ox = random.randint(-120, 120)
        d.arc([ox - r * 0.3, -r * 0.25, ox + r, r * 1.5], 0, 360,
              fill=(255, 255, 255, random.randint(40, 95)), width=2)


def _poster_rays(d, cx, cy, r, n=30, color=(255, 236, 170)):
    for k in range(n):
        ang = math.pi * 2 / n * k
        d.line([(cx, cy), (cx + r * math.cos(ang), cy + r * math.sin(ang))],
               fill=color + (80 if k % 2 == 0 else 45,),
               width=3 if k % 2 == 0 else 2)


def _poster_grid(d, seed=33):
    random.seed(seed)
    for x in range(0, POSTER_W + 1, 44):
        d.line([(x, 0), (x, POSTER_H)], fill=(255, 255, 255, 26), width=1)
    for y in range(0, POSTER_H + 1, 44):
        d.line([(0, y), (POSTER_W, y)], fill=(255, 255, 255, 26), width=1)
    for _ in range(16):
        cx = random.randint(30, POSTER_W - 30)
        cy = random.randint(30, POSTER_H - 30)
        r = random.randint(3, 7)
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=(255, 255, 255, 110))


def _poster_pine(d, seed=37, n=6):
    """松枝：斜向主枝 + 针叶簇（圣诞/项目交付的稳���意象）。"""
    random.seed(seed)
    for i in range(n):
        x0 = random.randint(-40, POSTER_W)
        y0 = random.randint(40, POSTER_H - 60)
        ang = random.uniform(-0.5, 0.9)
        ln = random.randint(150, 300)
        ex, ey = x0 + ln * math.cos(ang), y0 + ln * math.sin(ang)
        d.line([(x0, y0), (ex, ey)], fill=(96, 132, 96, 150), width=4)
        for k in range(11):
            t = k / 10
            px, py = x0 + ln * t * math.cos(ang), y0 + ln * t * math.sin(ang)
            for s in (-1, 1):
                a2 = ang + s * 0.9
                d.line([(px, py),
                        (px + 26 * math.cos(a2), py + 26 * math.sin(a2))],
                       fill=(120, 160, 120, 150), width=2)


def _poster_wave(d, seed=41, n=6):
    random.seed(seed)
    for i in range(n):
        base = 150 + i * 125
        amp = random.randint(22, 52)
        pts = [(x, base + amp * math.sin(x / 100 + i * 1.1)) for x in range(0, POSTER_W + 8, 8)]
        d.line(pts, fill=(255, 255, 255, random.randint(40, 85)), width=2, joint="curve")


def _poster_buildings(d, seed=43):
    """楼宇天际线（校企合作 / 品牌品质）。"""
    random.seed(seed)
    x = 20
    while x < POSTER_W - 40:
        bw = random.choice([46, 62, 78, 96])
        bh = random.randint(120, 330)
        top = POSTER_H - bh
        d.rectangle([x, top, x + bw, POSTER_H], fill=(255, 255, 255, 48))
        for wy in range(top + 16, POSTER_H - 14, 26):
            for wx in range(x + 10, x + bw - 10, 20):
                if random.random() < 0.5:
                    d.rectangle([wx, wy, wx + 9, wy + 12], fill=(255, 240, 200, 120))
        x += bw + random.randint(12, 24)


POSTER_MOTIFS = {
    "willow": _poster_willow, "rain": _poster_raindrops, "thunder": _poster_thunder,
    "blossom": _poster_petals, "mist": _poster_fog, "sun": _poster_sun,
    "wheat": _poster_wheat, "leaf": _poster_leaves, "dew": _poster_dew,
    "frost": _poster_frost, "snow": _poster_snow, "moon": _poster_moon,
    "lantern": _poster_lantern, "bamboo": _poster_bamboo, "confetti": _poster_confetti,
    "gear": _poster_gear, "arcs": _poster_arcs, "rays": _poster_rays,
    "grid": _poster_grid, "pine": _poster_pine, "wave": _poster_wave,
    "building": _poster_buildings,
}
# 主体型 motif（月/日/齿轮/光芒）需要落位，统一在这给定：偏右上，避开左边
# 的标题块与下方的员工信息区。圆心/半径按「标题右缘 382」往右排，互不重叠。
POSTER_MOTIFS["moon"] = lambda d: _poster_moon(d, 488, 206, 100)
POSTER_MOTIFS["sun"] = lambda d: _poster_sun(d, 514, 194, 76)
POSTER_MOTIFS["gear"] = lambda d: _poster_gear(d, 506, 240, 94)
POSTER_MOTIFS["rays"] = lambda d: _poster_rays(d, 320, 176, 520)


def make_poster(item_id, name, title, subtitle, colors, motif, style, tags):
    """海报：竖幅渐变 + motif + 烧入标题 + 下部信息区压深。

    下部压深是硬要求：小程序 canvas 在这一段用浅色文字绘制员工信息，
    不压深则深色海报上白字发灰、浅色海报上白字看不见。
    """
    img = vgrad((POSTER_W, POSTER_H), *colors).convert("RGBA")
    ov, d = overlay(img)
    motif_fn = POSTER_MOTIFS.get(motif)
    if motif_fn:
        motif_fn(d)
    img.alpha_composite(ov)

    # 下部信息区压深（自信息区上沿起渐变到近黑）
    info_top = POSTER_INFO_TOP * 2  # 画布坐标 → 素材坐标（2x）
    shade = Image.new("RGBA", (POSTER_W, POSTER_H), (0, 0, 0, 0))
    sd = ImageDraw.Draw(shade)
    for y in range(info_top, POSTER_H):
        t = (y - info_top) / float(POSTER_H - info_top)
        # 起点就压到 alpha 55：信息区上沿正好是姓名/职位，浅色海报（春分、
        # 小满这类）在那里只压 30 的话白字会发灰。
        sd.line([(0, y), (POSTER_W, y)], fill=(4, 6, 14, int(55 + 175 * t ** 0.7)))
    img = Image.alpha_composite(img, shade)

    d = ImageDraw.Draw(img)

    # 顶部再压一层短渐变，保证主标题在浅色海报上也读得出
    top_shade = Image.new("RGBA", (POSTER_W, POSTER_H), (0, 0, 0, 0))
    tsd = ImageDraw.Draw(top_shade)
    for y in range(0, 380):
        tsd.line([(0, y), (POSTER_W, y)], fill=(6, 10, 22, int(66 * (1 - y / 380.0))))
    img = Image.alpha_composite(img, top_shade)
    d = ImageDraw.Draw(img)

    # 主标题（衬线大字）+ 副标题（无衬线、字距）
    # 标题最大宽度 330：右缘留在 382 之前，与右侧的齿轮/太阳装饰（x≥386）不重叠
    serif, tsize = fit_font(_SERIF_CANDIDATES, title, 96, 330)
    if serif:
        ty = 108 + (96 - tsize) * 0.25  # 字号变小时贴着上沿走，不至于掉下去
        d.text((54, ty + 4), title, font=serif, fill=(8, 10, 20, 90))  # 投影
        d.text((52, ty), title, font=serif, fill=(255, 255, 255, 255))
    sans = sans_font(24)
    if sans:
        cx = 56
        for ch in subtitle:
            d.text((cx, 252), ch, font=sans, fill=(255, 255, 255, 205))
            cx += d.textlength(ch, font=sans) + 5
    d.line([(56, 300), (150, 300)], fill=(255, 255, 255, 150), width=2)

    # 印章：只盖节气与节日，商务场景不盖（"合作共赢"上盖"节气"章是错）。
    # 靠副标题/白名单判定，不靠标题里有没有"雪""雨"这些字——那条路子一加新
    # 海报就会误判（"瑞雪丰年"是祝福语，不是节气）。
    if "二十四节气" in subtitle:
        poster_seal(d, 540, 172, "節氣")
    elif item_id in POSTER_FESTIVALS:
        poster_seal(d, 540, 172, "節慶")

    path = os.path.join(OUT, "posters", item_id + ".jpg")
    img.convert("RGB").save(path, "JPEG", quality=86)
    thumb = img.convert("RGB").resize((POSTER_THUMB_W, POSTER_THUMB_H), Image.LANCZOS)
    tpath = os.path.join(OUT, "thumbs", "posters", item_id + ".jpg")
    thumb.save(tpath, "JPEG", quality=80)
    return {"id": item_id, "name": name, "category": "poster",
            "style": style,
            "file": "posters/%s.jpg" % item_id,
            "thumb": "thumbs/posters/%s.jpg" % item_id,
            "width": POSTER_W, "height": POSTER_H,
            "tags": tags + ["海报", "分享", "背景", STYLE_NAMES[style]]}


def poster_seal(d, cx, cy, text):
    """朱红印章（纸底 + 红框 + 居中文字）。字体缺失时返回 False（不画空框）。

    垫一层半透明宣纸底是必需的：朱红印泥直接压在深绿（立春）、深蓝（立冬）
    这类底色上会糊成一团，红框都看不清；垫白纸后才像真的盖了个章。
    """
    size = 58
    x0, y0 = cx - size / 2, cy - size / 2
    d.rounded_rectangle([x0, y0, x0 + size, y0 + size], radius=7,
                        fill=(250, 246, 236, 228))
    d.rounded_rectangle([x0, y0, x0 + size, y0 + size], radius=7,
                        outline=(178, 34, 34, 235), width=4)
    fo = serif_font(26)
    if not fo:
        return False
    d.text((cx, cy + 1), text, font=fo, fill=(178, 34, 34, 240), anchor="mm")
    return True


def fetch_lucide_license():
    os.makedirs(os.path.dirname(SRC), exist_ok=True)
    dst = os.path.join(HERE, "library-src", "LICENSE-Lucide.txt")
    if os.path.exists(dst):
        return
    url = "https://unpkg.com/lucide-static@%s/LICENSE" % LUCIDE_VERSION
    http_get(url, dst)
    print("  fetched Lucide LICENSE")


CREDITS = """# 常用开源素材库 · 许可与来源

本目录由 `gen_library.py` 生成，内嵌进后端二进制（见 `assets.go`）。

- **图标（icons/）**：取自 [Lucide](https://lucide.dev) v%s（ISC License），
  经 rsvg-convert 光栅化并合成到渐变圆角方块。ISC 许可全文见
  `../library-src/LICENSE-Lucide.txt`。
- **封面（covers/）、背景（backgrounds/）、LOGO（logos/）、头像（avatars/）
  与海报（posters/）**：由生成脚本程序绘制（渐变 + 几何图形/人形剪影），
  无第三方素材，可自由使用。海报上的中文标题使用系统宋体渲染。

重新生成：`python3 gen_library.py`
（依赖 Pillow 与 rsvg-convert；Lucide SVG 已留存 library-src/icons/，离线可再生成）。
"""


def main():
    for sub in ("icons", "covers", "logos", "avatars", "backgrounds", "posters",
                "thumbs/covers", "thumbs/backgrounds", "thumbs/posters"):
        os.makedirs(os.path.join(OUT, sub), exist_ok=True)
    fetch_lucide_license()

    items = []
    print("icons: %d 个 × %d 配色档" % (len(ICONS), len(ICON_VARIANTS)))
    for lucide, name, group, tags in ICONS:
        for style, suffix, name_suffix, glyph_color in ICON_VARIANTS:
            items.append(make_icon(
                "icon-" + lucide + suffix, name + name_suffix, lucide, group, tags,
                ICON_PALETTES[style][group], style, glyph_color or "#FFFFFF"))
    print("covers: %d" % len(COVERS))
    for cid, name, colors, motif in COVERS:
        items.append(make_cover(cid, name, colors, motif))
    print("logos: %d" % len(LOGOS))
    for lid, name, colors, mark in LOGOS:
        items.append(make_logo(lid, name, colors, mark))
    print("avatars: %d" % len(AVATARS))
    for aid, name, colors in AVATARS:
        items.append(make_avatar(aid, name, colors))
    print("backgrounds: %d" % len(BACKGROUNDS))
    for bid, name, colors, motif, tags in BACKGROUNDS:
        items.append(make_background(bid, name, colors, motif, tags))
    print("posters: %d" % len(POSTERS))
    for pid, name, title, sub, colors, motif, style, tags in POSTERS:
        items.append(make_poster(pid, name, title, sub, colors, motif, style, tags))

    # 素材 id 必须唯一（id 同时是落盘文件名与采用路径）：
    # 扩充素材时若不小心写重，后面会静默覆盖前一项，故在此 fail-fast。
    seen = {}
    for it in items:
        if it["id"] in seen:
            raise RuntimeError("素材 id 重复: %s（%s / %s）" % (it["id"], seen[it["id"]], it["name"]))
        seen[it["id"]] = it["name"]

    # 调性分布自检：某个调性一个素材都没有，多半是归类表里写错了 id
    for sk, sn in STYLES:
        n = sum(1 for it in items if it.get("style") == sk)
        print("  style %-8s %-6s %d" % (sk, sn, n))
        if n == 0:
            raise RuntimeError("调性 %s 没有任何素材，检查 _STYLE_GROUPS 的 id" % sk)

    manifest = {
        "version": 9,
        "styles": [{"key": k, "name": n} for k, n in STYLES],
        "categories": [
            {"key": "icon", "name": "图标"},
            {"key": "cover", "name": "封面"},
            {"key": "logo", "name": "LOGO"},
            {"key": "avatar", "name": "头像"},
            {"key": "background", "name": "背景"},
            {"key": "poster", "name": "海报"},
        ],
        "items": items,
    }
    with open(os.path.join(OUT, "manifest.json"), "w", encoding="utf-8") as f:
        json.dump(manifest, f, ensure_ascii=False, indent=1)
    with open(os.path.join(OUT, "CREDITS.md"), "w", encoding="utf-8") as f:
        f.write(CREDITS % LUCIDE_VERSION)

    print("done: %d items -> %s" % (len(items), OUT))


if __name__ == "__main__":
    main()

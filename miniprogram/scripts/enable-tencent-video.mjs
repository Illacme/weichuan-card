#!/usr/bin/env node
// 腾讯视频档开关：小程序侧的两处静态声明。
//
// 为什么需要一个脚本而不是直接改：腾讯视频插件（provider wxa75efa648b60994b）必须
// 在 app.json 静态声明，且**声明了却没在 MP 后台授权会让整个小程序编译失败**——
// 连预览都传不上去。这不是「某个客户视频播不出来」，是所有客户的整个小程序发不了版。
// 所以默认不带插件，要用的部署方跑一次本脚本显式打开；不用的部署方升级后行为不变。
//
// 用法（在仓库任意位置都行，路径按脚本自身位置解析）：
//   node miniprogram/scripts/enable-tencent-video.mjs on    # 打开
//   node miniprogram/scripts/enable-tencent-video.mjs off   # 还原
//   node miniprogram/scripts/enable-tencent-video.mjs       # 只打印当前状态
//
// 打开后还要做两件事（脚本改不了，必须人做）：
//   1. 小程序后台「设置 → 第三方服务 → 插件管理」添加腾讯视频插件并等授权通过；
//   2. 后端设环境变量 MP_TENCENT_VIDEO=true 并重启，否则后台该档仍不可选/保存被拒。
//
// 插件版本号以 MP 后台「插件详情」里显示的为准，可用 PLUGIN_VERSION=2.0.5 覆盖。
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const MP_DIR = join(dirname(fileURLToPath(import.meta.url)), '..')
const APP_JSON = join(MP_DIR, 'app.json')
const COMP_JSON = join(MP_DIR, 'components/company-video/index.json')

const PROVIDER = 'wxa75efa648b60994b'
const PLUGIN_VERSION = process.env.PLUGIN_VERSION || '1.3.6'
const COMPONENT_KEY = 'txv-video'
const COMPONENT_PATH = 'plugin://tencentvideo/video'

function readJson(p) {
  return JSON.parse(readFileSync(p, 'utf8'))
}
// 2 空格缩进 + 尾换行，与仓库既有 json 一致（避免整文件被重排成 diff 噪音）
function writeJson(p, obj) {
  writeFileSync(p, JSON.stringify(obj, null, 2) + '\n')
}

function status() {
  const app = readJson(APP_JSON)
  const comp = readJson(COMP_JSON)
  const declared = !!app.plugins?.tencentvideo
  const registered = comp.usingComponents?.[COMPONENT_KEY] === COMPONENT_PATH
  return { app, comp, declared, registered }
}

const { app, comp, declared, registered } = status()
const cmd = (process.argv[2] || 'status').toLowerCase()

if (cmd === 'status') {
  console.log(`app.json 声明插件: ${declared ? '是' : '否'}`)
  console.log(`company-video 注册 ${COMPONENT_KEY}: ${registered ? '是' : '否'}`)
  process.exit(0)
}

if (cmd === 'on') {
  if (declared && registered) {
    console.log('已开启，无需改动')
    process.exit(0)
  }
  app.plugins = { ...(app.plugins || {}), tencentvideo: { version: PLUGIN_VERSION, provider: PROVIDER } }
  comp.usingComponents = { ...(comp.usingComponents || {}), [COMPONENT_KEY]: COMPONENT_PATH }
  writeJson(APP_JSON, app)
  writeJson(COMP_JSON, comp)
  console.log(`已开启：app.json 声明 tencentvideo@${PLUGIN_VERSION}，组件注册 ${COMPONENT_KEY}`)
  console.log('接下来：① MP 后台添加并授权该插件；② 后端设 MP_TENCENT_VIDEO=true 并重启')
  process.exit(0)
}

if (cmd === 'off') {
  if (!declared && !registered) {
    console.log('已关闭，无需改动')
    process.exit(0)
  }
  if (app.plugins) {
    delete app.plugins.tencentvideo
    if (Object.keys(app.plugins).length === 0) delete app.plugins
  }
  if (comp.usingComponents) {
    delete comp.usingComponents[COMPONENT_KEY]
    if (Object.keys(comp.usingComponents).length === 0) delete comp.usingComponents
  }
  writeJson(APP_JSON, app)
  writeJson(COMP_JSON, comp)
  console.log('已关闭：插件声明与组件注册均已移除')
  process.exit(0)
}

console.error(`未知命令: ${cmd}（只支持 on / off / status）`)
process.exit(1)

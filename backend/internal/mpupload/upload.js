// 小程序代码上传脚本，由 Go 端（internal/mpupload）以 node 执行。
//
// 为什么上传要在 Node 里做：微信官方只提供两条上传途径——开发者工具（GUI）与
// miniprogram-ci（Node 模块，从开发者工具抽离的编译上传能力）。自营小程序没有
// 纯 HTTP 上传接口（wxa/commit 只服务第三方平台代开发），所以后端要么调 Node，
// 要么自己复刻编译+打包+签名的整套流程。选前者。
//
// 协议：Go 从 stdin 喂一份 JSON，本脚本把结果以固定前缀打到 stdout 最后一行。
// 用前缀而不是「只打印 JSON」，是因为 miniprogram-ci 自己会往 stdout 打编译进度，
// 不能假设 stdout 里只有我们的内容。
//
// 输入 JSON：
//   { appid, projectPath, privateKeyPath, version, desc, robot }
// 输出（stdout 最后一行，前缀 __WEICHUAN_MP_UPLOAD__）：
//   { ok: true } 或 { ok: false, error: "..." }
//
// 注意：privateKeyPath 是 Go 写好的临时文件（0600，用完即删），密钥本身不进 argv、
// 不进 stdout、不进任何日志——argv 会被 ps 看到。

const ci = require('miniprogram-ci')

const SENTINEL = '__WEICHUAN_MP_UPLOAD__'

function emit(obj) {
  process.stdout.write(SENTINEL + JSON.stringify(obj) + '\n')
}

async function main() {
  let raw = ''
  process.stdin.setEncoding('utf8')
  for await (const chunk of process.stdin) raw += chunk

  let cfg
  try {
    cfg = JSON.parse(raw)
  } catch (e) {
    emit({ ok: false, error: 'Go 端传来的配置不是合法 JSON：' + e.message })
    process.exit(1)
  }
  for (const k of ['appid', 'projectPath', 'privateKeyPath', 'version']) {
    if (!cfg[k]) {
      emit({ ok: false, error: '缺少参数 ' + k })
      process.exit(1)
    }
  }

  const project = new ci.Project({
    appid: cfg.appid,
    type: 'miniProgram',
    projectPath: cfg.projectPath,
    privateKeyPath: cfg.privateKeyPath,
    // node_modules 不进包：小程序源码目录里可能有为装 typescript 编译器而存在的
    // node_modules，把它传上去只会让包变大、并可能带上与本项目无关的依赖。
    ignores: ['node_modules/**/*'],
  })

  const opts = {
    project,
    version: cfg.version,
    desc: cfg.desc || '',
  }
  // robot 是「哪台 CI 机器人传的」（1-30），只影响体验版二维码的归属展示。
  // 不传时 miniprogram-ci 用自己的缺省，这里不替它决定。
  if (cfg.robot) opts.robot = cfg.robot

  await ci.upload(opts)
  emit({ ok: true })
}

main().catch((e) => {
  // 只取 message：ci 的错误对象里可能挂着整个编译上下文，全序列化会把
  // 项目路径之类的信息倒进日志。真正的排障信息微信都放在 message 里。
  const msg = (e && e.message) || String(e)
  emit({ ok: false, error: msg })
  process.exit(1)
})

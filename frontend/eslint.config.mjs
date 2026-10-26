// ESLint 9 flat config（前端 lint 门禁）。
//
// 选型原则与后端 .golangci.yml 一致：**只留「报出来一定是问题」的规则，不留风格类**。
// 前端已经有 vue-tsc 做类型门禁（类型错误一律拦住），所以这里不重复做类型相关检查，
// 只补三件 vue-tsc 管不到、但真会出事的事：
//   1. 未使用的变量 / 导入（删功能时最常留下的垃圾，且会掩盖「这个分支没走到」）；
//   2. Vue 模板里的硬性错误（v-for 缺 key、指令写错、重复属性）——
//      这些在运行时表现为「列表乱序 / 整个块不渲染」，而不是报错；
//   3. 会把人绕进去的 JS 陷阱（async 里直接 new Promise、常量当条件）。
//
// 刻意不启用：任何排版/命名/复杂度规则（缩进、引号、分号、函数长度…）。
// 一开就是几百条，结局只有两种：花几天改代码，或加一堆 eslint-disable 让它变摆设。
import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import pluginVue from 'eslint-plugin-vue'
import globals from 'globals'

export default tseslint.config(
  // components.d.ts 由 unplugin-vue-components 自动生成（每次构建覆写），
  // 检查生成物只会产出「无人会去改」的告警。
  { ignores: ['dist/**', 'dist-check/**', 'node_modules/**', 'coverage/**', 'src/components.d.ts'] },
  js.configs.recommended,
  // 只取 base（parser + plugin，不带规则集）：recommended 里的 no-explicit-any
  // 会命中测试桩里大量的 any，逼着一处处加 disable，反而淹没有效信号。
  // 注意 base 是**单个配置对象**而不是数组（v8 起如此），不能展开。
  tseslint.configs.base,
  // flat/essential 是 vue 的最小集（解析错误 + 少数硬错误），
  // 不用 recommended：那一档含大量模板风格规则（属性顺序、换行…）。
  ...pluginVue.configs['flat/essential'],
  {
    files: ['**/*.vue'],
    // vue 文件必须由 vue-eslint-parser 解析，但 **`<script setup lang="ts">` 里的 TS 语法**
    // 要交给 TS parser 才行——只装 vue 插件的话，类型注解会被当成语法错误
    // （满屏 "Parsing error: Unexpected token Xxx" 就是漏了这一行）。
    languageOptions: {
      parserOptions: { parser: tseslint.parser },
    },
  },
  {
    files: ['**/*.{ts,vue}'],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: 'module',
      globals: { ...globals.browser, ...globals.node },
    },
    rules: {
      // TS 项目里 no-undef 会把类型名当未定义变量报出来（types 不是值），
      // 交给 vue-tsc 管，这里关掉。
      'no-undef': 'off',
      'no-unused-vars': 'off',
      '@typescript-eslint/no-unused-vars': [
        'error',
        // 下划线开头的参数是「占位但不使用」的显式约定（如 (_url, _init) ）
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      // 会静默吞掉异常或让等待永不落地的写法
      'no-async-promise-executor': 'error',
      'no-constant-condition': 'warn',
      'no-self-compare': 'error',
      'no-unreachable': 'error',
      // Vue 模板里真正会出错的两条
      'vue/require-v-for-key': 'error',
      'vue/no-dupe-keys': 'error',
      // 命名类规则不进门禁：Stats / Settings / Login 这类单文件组件名与路由一一对应，
      // 改成 WeichuanStats 只是多敲几个字，读代码的人反而要先去掉前缀才认得出是哪一页。
      'vue/multi-word-component-names': 'off',
    },
  },
)

import { defineComponent, h, nextTick } from 'vue'

// 列表页单测共用的 Element Plus 桩。
//
// 为什么自己写桩而不引入真组件：真组件要挂全局样式、依赖 ResizeObserver/Popper，
// 在 jsdom 里会引入大量与被测逻辑无关的噪声，而且 el-dialog 默认 teleport 到 body，
// 断言得绕出组件树去找 DOM。这里每个桩只保留「被测逻辑真正依赖的那一个契约」。
//
// 三条踩过的坑，改动这里之前先看：
//  1. el-button 桩**不要**声明 emits:['click']。声明了以后 @click 只认 $emit，
//     trigger('click') 打到空气上；不声明则落进 $attrs 绑到原生 button，点击才真的触发。
//  2. el-dialog 桩带 data-title。一个页面两个弹窗时 find('.el-dialog') 永远只拿第一个，
//     断言会静默落在另一个弹窗上。
//  3. el-table 桩按 el-table 的**真实模型**：列只是配置（el-table-column 不渲染任何东西），
//     行与单元格全由 el-table 按 data 渲染。若按「每列各渲染自己的单元格」写桩，
//     纯 prop 列（无插槽）会被 Vue 静态提升，父组件重渲染时整个 vnode 被跳过——
//     数据变了、带插槽的列更新了，唯独 prop 列停在首次渲染的旧值
//     （实测表现：驳回原因列刷新后一直是空的）。

export const ElCard = defineComponent({
  name: 'ElCard',
  template: `<div class="el-card"><slot /></div>`,
})

export const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'type', 'placeholder', 'clearable', 'rows'],
  emits: ['update:modelValue'],
  // 驳回原因是 textarea：必须真渲染成 <textarea>，否则 setValue 走的是 input 语义，
  // 测试里的「填原因」和用户实际操作不是一回事
  template: `<textarea v-if="type === 'textarea'" class="el-input" :placeholder="placeholder"
      :value="modelValue" @input="$emit('update:modelValue', $event.target.value)"></textarea>
    <input v-else class="el-input" :type="type || 'text'" :placeholder="placeholder"
      :value="modelValue" @input="$emit('update:modelValue', $event.target.value)" />`,
})

export const ElButton = defineComponent({
  name: 'ElButton',
  // disabled 必须在这里声明：模板里用了 :disabled，不声明会渲染期报
  // 「Property "disabled" was accessed during render but is not defined」
  props: ['type', 'size', 'loading', 'link', 'icon', 'disabled'],
  template: `<button class="el-button" :disabled="disabled" :data-loading="String(!!loading)" v-bind="$attrs"><slot /></button>`,
})

export const ElTag = defineComponent({
  name: 'ElTag',
  props: ['type', 'effect', 'size'],
  template: `<span class="el-tag" :data-type="type"><slot /></span>`,
})

export const ElPagination = defineComponent({
  name: 'ElPagination',
  props: ['currentPage', 'pageSize', 'total', 'pageSizes', 'layout', 'background'],
  template: `<div class="el-pagination" :data-total="total" />`,
})

export const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width'],
  template: `<div v-if="modelValue" class="el-dialog" :data-title="title"><h3>{{ title }}</h3><slot /><slot name="footer" /></div>`,
})

export const ElDescriptions = defineComponent({
  name: 'ElDescriptions',
  props: ['column', 'border'],
  template: `<div class="el-descriptions"><slot /></div>`,
})

export const ElDescriptionsItem = defineComponent({
  name: 'ElDescriptionsItem',
  props: ['label'],
  template: `<div class="el-descriptions-item" :data-label="label"><slot /></div>`,
})

export const ElTooltip = defineComponent({
  name: 'ElTooltip',
  props: ['content', 'placement', 'effect'],
  template: `<span class="el-tooltip"><slot /><slot name="content" /></span>`,
})

export const ElImage = defineComponent({
  name: 'ElImage',
  props: ['src', 'fit'],
  template: `<img class="el-image" :src="src" />`,
})

export const ElIcon = defineComponent({
  name: 'ElIcon',
  template: `<i class="el-icon"><slot /></i>`,
})

export const ElTableColumn = defineComponent({
  name: 'ElTableColumn',
  __isTableColumn: true, // 供 el-table 桩识别列配置节点（组件选项里的未知字段 Vue 会忽略）
  props: ['prop', 'label', 'width', 'align', 'fixed', 'minWidth', 'showOverflowTooltip'],
  setup() {
    return () => null
  },
})

export const ElTable = defineComponent({
  name: 'ElTable',
  props: ['data', 'border', 'stripe'],
  setup(props, { slots, expose }) {
    expose({})
    return () => {
      const cols = (slots.default ? slots.default() : [])
        .filter((v: any) => v && v.type && (v.type as any).__isTableColumn)
        .map((v: any) => ({
          prop: v.props?.prop,
          // 编译产物里插槽挂在 children.default 上（对象形式）
          slot:
            v.children && typeof v.children === 'object' && !Array.isArray(v.children)
              ? (v.children as any).default
              : null,
        }))
      return h(
        'div',
        { class: 'el-table' },
        (props.data || []).map((row: any, i: number) =>
          h(
            'div',
            { class: 'el-table-row', 'data-row-id': row?.id, key: i },
            cols.map((c: any, j: number) =>
              h(
                'div',
                { class: 'el-table-column', 'data-prop': c.prop ?? null, key: j },
                c.slot ? c.slot({ row }) : c.prop && row ? String(row[c.prop] ?? '') : ''
              )
            )
          )
        )
      )
    }
  },
})

// 头像列：只验证「有头像时走缩略图组件」，悬停预览与全屏查看属 ImageThumb 自己的职责
export const ImageThumbStub = defineComponent({
  name: 'ImageThumb',
  props: ['src', 'width', 'height'],
  template: `<img class="img-thumb" :src="src" />`,
})

export const elStubs = {
  'el-card': ElCard,
  'el-input': ElInput,
  'el-button': ElButton,
  'el-tag': ElTag,
  'el-pagination': ElPagination,
  'el-dialog': ElDialog,
  'el-descriptions': ElDescriptions,
  'el-descriptions-item': ElDescriptionsItem,
  'el-tooltip': ElTooltip,
  'el-image': ElImage,
  'el-icon': ElIcon,
  'el-table': ElTable,
  'el-table-column': ElTableColumn,
  ImageThumb: ImageThumbStub,
}

export const mountOpts = {
  global: {
    stubs: elStubs,
    directives: { loading: {} },
  },
}

export async function flush() {
  for (let i = 0; i < 6; i++) await nextTick()
}

// api 层已把响应体收敛成 Paged（{list,total,page,size}），useServerTable 直接消费这个形状。
// 每次返回**新数组**：真实请求每次都是新对象；若桩回同一个引用，表格的 :data 不变，
// 行就不会重算（见上面第 3 条）。
export function paged(list: any[]) {
  return { list: [...list], total: list.length, page: 1, size: 20 }
}

/** 找到某一行（按 data-row-id） */
export function rowOf(w: any, id: number) {
  return w.findAll(`.el-table-row[data-row-id="${id}"]`)[0]
}

/** 在指定行里找按钮 */
export function btnIn(row: any, text: string) {
  return row.findAll('button.el-button').find((b: any) => b.text().includes(text))
}

/** 按标题定位弹窗（一个页面往往不止一个 el-dialog） */
export function dlgOf(w: any, title: string) {
  return w.find(`.el-dialog[data-title="${title}"]`)
}

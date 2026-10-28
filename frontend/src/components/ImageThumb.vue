<template>
  <!-- 列表缩略图统一组件：指针悬停浮层看大图 + 点击打开全屏查看器。
       各列表页（员工头像 / 分类与服务图标 / 项目封面 / 申请头像）共用，
       替代裸 el-image，避免每个页面各写一套预览逻辑。 -->
  <!-- 根元素包一层：el-tooltip 要求「恰好一个有效子元素」（ElOnlyChild），
       查看器必须放在 tooltip 外面，否则 tooltip 直接失效、点击也打不开。 -->
  <span class="itp-root">
    <el-tooltip
      placement="right"
      effect="light"
      :show-after="250"
      :hide-delay="120"
      popper-class="img-thumb-popper"
    >
      <template #content>
        <img :src="src" class="img-thumb-pop-img" alt="预览大图" />
      </template>
      <el-image
        class="img-thumb"
        :src="thumbSrc"
        :fit="fit"
        :style="thumbStyle"
        @click="viewerOpen = true"
        @error="onErr"
      />
    </el-tooltip>
    <!-- 全屏查看统一走 ImageViewer（带分辨率/文件大小信息条），不用 el-image
         自带的 preview-src-list——后者无法给出原图信息。 -->
    <ImageViewer v-if="viewerOpen" :url-list="[src]" @close="viewerOpen = false" />
  </span>
</template>

<script setup lang="ts">
import { computed, ref, watch, type PropType } from 'vue'
import ImageViewer from './ImageViewer.vue'
import { thumbnailURL } from '@/utils/thumb'

const props = defineProps({
  src: { type: String, required: true },
  // 缩略图尺寸（px）；悬停大图与全屏查看器都用原图 URL，不受此影响
  width: { type: Number, default: 36 },
  height: { type: Number, default: 36 },
  // 圆角：默认 4px 方块；头像等场景传 '50%' 变圆形
  radius: { type: String, default: '4px' },
  fit: {
    type: String as PropType<'fill' | 'contain' | 'cover' | 'none' | 'scale-down'>,
    default: 'cover',
  },
})

const viewerOpen = ref(false)
// 缩略图加载失败（存量上传 / 外链 / SaveBytes 生成的二维码素材无缩略图）时回退原图。
const failed = ref(false)
watch(
  () => props.src,
  () => {
    failed.value = false
  },
)

// 列表网格用缩略图降低带宽与首屏耗时；悬停浮层与全屏查看器始终用原图（props.src）。
const thumbSrc = computed(() => (failed.value ? props.src : thumbnailURL(props.src)))

function onErr() {
  failed.value = true
}

const thumbStyle = computed(() => ({
  width: props.width + 'px',
  height: props.height + 'px',
  borderRadius: props.radius,
}))
</script>

<!-- 悬停浮层内的大图：teleport 到 body，但 img 元素带本组件 scope id，scoped 可命中 -->
<style scoped>
.itp-root {
  display: inline-flex;
  vertical-align: middle;
}
.img-thumb {
  cursor: zoom-in;
  flex-shrink: 0;
  vertical-align: middle;
}
.img-thumb-pop-img {
  display: block;
  max-width: 320px;
  max-height: 480px;
  object-fit: contain;
}
</style>

<template>
  <div>
    <el-card>
      <div class="page-bar">
        <h3>名片统计</h3>
        <div class="bar-right">
          <el-input
            v-model="keyword"
            placeholder="搜索姓名/职位"
            clearable
            :prefix-icon="Search"
            style="width: 200px"
          />
          <!-- 日期范围：value-format 直接给 YYYY-MM-DD 字符串，与接口参数同形；
               清空（×）= 全部时间，与后端 from/to 缺省语义一致 -->
          <el-date-picker
            v-model="range"
            type="daterange"
            unlink-panels
            range-separator="至"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            :shortcuts="rangeShortcuts"
            value-format="YYYY-MM-DD"
            clearable
            style="width: 260px"
          />
          <el-button :icon="Download" @click="onExport">导出</el-button>
          <el-button :icon="Tickets" @click="openDetail()">数据明细</el-button>
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        </div>
      </div>

      <!-- 汇总三卡：访问 / 拨号 / 保存（口径见《首页组件与接口字段映射》§4：只记点击，不校验真实结果） -->
      <div class="stat-cards">
        <div class="stat-card">
          <div class="stat-label">总访问</div>
          <div class="stat-value">{{ summary.visit }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">拨号点击</div>
          <div class="stat-value">{{ summary.call }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">保存点击</div>
          <div class="stat-value">{{ summary.save }}</div>
        </div>
      </div>

      <p class="range-tip">统计范围：{{ rangeLabel }}（日期按浏览器本地时区与后端 TZ 一致计算）</p>

      <!-- 按天趋势：只含所选范围内有事件的日期；点某一天可下钻当天明细（常驻首屏，运营每天看健康度） -->
      <StatTrend :points="trend" @pick="onTrendPick" />

      <!-- 折叠区1：内容浏览三卡 + 裂变动作两卡 + TOP 双榜，默认收起（方案A 同页分区） -->
      <section class="fold fold-intent" :class="{ open: intentOpen }">
        <div class="fold-head" @click="intentOpen = !intentOpen">
          <span class="fold-title">内容浏览与裂变<span class="fold-sub">访客在看什么 · 名片被传出去多少</span></span>
          <el-icon class="fold-arrow"><ArrowDown /></el-icon>
        </div>
        <div v-show="intentOpen" class="fold-body">
          <!-- 内容浏览三卡：案例/服务/视频。这三类是唯一「指向具体内容对象」的事件
               （targetId 强制存在且同租户），所以只有它们进 TOP 榜，
               也才配叫「访客对什么内容感兴趣」。口径见 stat.go 文件头。 -->
          <div class="intent-block">
            <div class="intent-title">内容浏览<span class="intent-sub">访客对什么内容感兴趣</span></div>
            <div class="stat-cards intent-cards">
              <div class="stat-card">
                <div class="stat-label">案例浏览</div>
                <div class="stat-value">{{ summary.projectView }}</div>
              </div>
              <div class="stat-card">
                <div class="stat-label">服务浏览</div>
                <div class="stat-value">{{ summary.serviceView }}</div>
              </div>
              <div class="stat-card">
                <div class="stat-label">视频播放</div>
                <div class="stat-value">{{ summary.videoPlay }}</div>
              </div>
            </div>
          </div>
          <!-- 裂变动作两卡：转发 / 海报保存。
               为什么不并入上面的「内容浏览」：这两类不带 targetId、不指向任何内容对象，
               且执行者可能是员工本人（「我的 → 生成海报」入口只有本人才进得去），
               混进「访客对什么内容感兴趣」既答不了那个问题，又会把意向总量抬高。 -->
          <div class="intent-block">
            <div class="intent-title">裂变动作<span class="intent-sub">把名片传出去的动作 · 主体可能是员工本人</span></div>
            <div class="stat-cards intent-cards">
              <div class="stat-card">
                <div class="stat-label">转发</div>
                <div class="stat-value">{{ summary.share }}</div>
              </div>
              <div class="stat-card">
                <div class="stat-label">海报保存</div>
                <div class="stat-value">{{ summary.posterSave }}</div>
              </div>
            </div>
          </div>
          <!-- 内容浏览 TOP 榜：按 target_id 聚合（口径见 stat.go statTopTargets）。
               点某行直接下钻该对象的明细——「哪个案例热」到这里就有答案。
               对象被删除后行仍在（计数不减），避免「汇总 3 条、榜单加起来 2 条」。 -->
          <div class="top-boards">
            <div class="top-board">
              <div class="top-title">TOP 案例浏览<span class="top-sub">点行下钻明细</span></div>
              <el-table
                :data="topProjects"
                class="top-table"
                border
                @row-click="(row: any) => onTopRowClick('project_view', row)"
              >
                <el-table-column type="index" label="排名" width="60" align="center" />
                <el-table-column prop="name" label="案例" min-width="140" show-overflow-tooltip />
                <el-table-column prop="count" label="次数" width="80" align="right" />
              </el-table>
              <p v-if="!loading && !topProjects.length" class="top-empty">范围内暂无案例浏览</p>
            </div>
            <div class="top-board">
              <div class="top-title">TOP 服务浏览<span class="top-sub">点行下钻明细</span></div>
              <el-table
                :data="topServices"
                class="top-table"
                border
                @row-click="(row: any) => onTopRowClick('service_view', row)"
              >
                <el-table-column type="index" label="排名" width="60" align="center" />
                <el-table-column prop="name" label="服务" min-width="140" show-overflow-tooltip />
                <el-table-column prop="count" label="次数" width="80" align="right" />
              </el-table>
              <p v-if="!loading && !topServices.length" class="top-empty">范围内暂无服务浏览</p>
            </div>
          </div>
        </div>
      </section>

      <!-- 折叠区：流量来源与裂变，默认收起（方案A 同页分区） -->
      <section class="fold fold-source" :class="{ open: sourceOpen }">
        <div class="fold-head" @click="sourceOpen = !sourceOpen">
          <span class="fold-title">流量来源与裂变<span class="fold-sub">访客从哪来 · 谁的名片被转发带来新访客</span></span>
          <el-icon class="fold-arrow"><ArrowDown /></el-icon>
        </div>
        <div v-show="sourceOpen" class="fold-body">
          <!-- 流量来源分布：对 visit 事件按 source 分组（scan/share_card/search/poster/unknown/other）。
               条形长度按占访问总量比例——一眼看出主渠道；未知/其他作为兜底项也展示。 -->
          <div class="src-block">
            <div class="intent-title">流量来源分布<span class="intent-sub">按进入名片的渠道拆分（visit 事件）</span></div>
            <div class="src-rows">
              <div class="src-row" v-for="row in sourceDist" :key="row.source">
                <span class="src-label">{{ sourceLabel(row.source) }}</span>
                <div class="src-bar-wrap">
                  <div class="src-bar" :style="{ width: sourcePct(row.count) + '%' }"></div>
                </div>
                <span class="src-count">{{ row.count }}</span>
              </div>
            </div>
            <p v-if="!loading && !sourceDist.length" class="top-empty">范围内暂无访问数据</p>
          </div>
          <!-- 裂变榜：source=share_card 的 visit 按员工聚合——谁的名片被转发带来新访客最多。
               归因落在「被分享的名片主人」上（分享者本人无需单独记录），正是运营要的排行。 -->
          <div class="fission-block">
            <div class="intent-title">裂变榜 · 谁的名片被转发带来新访客<span class="intent-sub">share_card 来源的访问按员工聚合</span></div>
            <el-table :data="fissionTop" class="top-table" border>
              <el-table-column type="index" label="排名" width="60" align="center" />
              <el-table-column prop="name" label="员工" min-width="120" show-overflow-tooltip />
              <el-table-column prop="count" label="转发带来访问" width="130" align="right" />
            </el-table>
            <p v-if="!loading && !fissionTop.length" class="top-empty">范围内暂无转发带来的访问</p>
          </div>
        </div>
      </section>

      <!-- 折叠区2：按员工（表格 + 口径说明 + 分页），默认收起（方案A 同页分区） -->
      <section class="fold fold-staff" :class="{ open: staffOpen }">
        <div class="fold-head" @click="staffOpen = !staffOpen">
          <span class="fold-title">按员工<span class="fold-sub">谁带来的访问与意向多</span></span>
          <el-icon class="fold-arrow"><ArrowDown /></el-icon>
        </div>
        <div v-show="staffOpen" class="fold-body">
          <el-table :data="paged" v-loading="loading" border stripe>
            <el-table-column prop="staffId" label="编号" width="80" align="center" />
            <el-table-column prop="name" label="姓名" width="120" />
            <el-table-column prop="title" label="职位" width="120" />
            <el-table-column prop="visit" label="访问" width="110" align="right" sortable />
            <el-table-column prop="call" label="拨号" width="110" align="right" sortable />
            <el-table-column prop="save" label="保存" width="110" align="right" sortable />
            <!-- 内容浏览：三类浏览事件的合计，悬浮看拆分。做成一列而不是三列——
                 三列会把表格推到需要横向滚动，而「谁带来的浏览多」用一个数就够读；
                 细分走行「明细」下钻（那里能按事件类型逐类筛） -->
            <el-table-column prop="browse" label="内容浏览" width="110" align="right" sortable>
              <template #default="{ row }">
                <el-tooltip placement="top" :content="browseTip(row)">
                  <span class="intent-cell">{{ row.browse }}</span>
                </el-tooltip>
              </template>
            </el-table-column>
            <!-- 裂变：转发 + 海报保存的合计。与「内容浏览」分列而不是合并：
                 两者的主体和行为性质都不同（可能是员工自己把名片传出去），
                 合成一个数会让「谁的内容被看得多」和「谁在使劲传播」混成一谈 -->
            <el-table-column prop="fission" label="裂变" width="90" align="right" sortable>
              <template #default="{ row }">
                <el-tooltip placement="top" :content="fissionTip(row)">
                  <span class="intent-cell">{{ row.fission }}</span>
                </el-tooltip>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="80" align="center">
              <template #default="{ row }">
                <el-button link type="primary" @click="openDetail(row.staffId)">明细</el-button>
              </template>
            </el-table-column>
          </el-table>
          <p class="tip">
            口径：只记前端点击、不校验真实结果（需求 §6）。「内容浏览」是案例浏览/服务浏览/视频播放三类事件的合计——这三类都指向具体内容对象（案例/服务），才叫「访客对什么内容感兴趣」。「裂变」是转发 + 海报保存的合计，主体可能是员工本人（「我的 → 生成海报」只有本人进得去），故单独成列、不与内容浏览相加。悬浮可看拆分，行「明细」可逐类下钻。想看某员工的名片实际效果，在小程序首页输入其编号即可预览。
          </p>

          <el-pagination
            class="table-pager"
            v-model:current-page="currentPage"
            v-model:page-size="size"
            :total="total"
            :page-sizes="[10, 20, 50]"
            layout="total, sizes, prev, pager, next"
            background
          />
        </div>
      </section>
    </el-card>

    <!-- 明细抽屉：员工行「明细」/「数据明细」按钮/趋势图点某天/TOP 榜行点击四个入口
         （后两者分别预选 eventType + targetId，形成「哪个案例热 → 看它的事件」闭环） -->
    <StatDetail
      v-model:open="detailOpen"
      :range="rangeParam"
      :staffs="staffOptions"
      :staff-id="detailStaffId"
      :event-type="detailEventType"
      :target-id="detailTargetId"
      :target-name="detailTargetName"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { Download, Refresh, Search, Tickets, ArrowDown } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { adminStats, exportStatsCsv, type StatRange, type StatSummary, type StatTopItem, type StatSourceItem, type StatFissionItem, SOURCE_LABELS } from '@/api/stats'
import StatTrend from '@/components/StatTrend.vue'
import StatDetail from '@/components/StatDetail.vue'
import { useTable } from '@/composables/useTable'

const rows = ref<any[]>([])
const { keyword, currentPage, size, total, paged } = useTable(
  rows,
  (r, kw) =>
    (r.name || '').toLowerCase().includes(kw) || (r.title || '').toLowerCase().includes(kw)
)
const loading = ref(false)
// 方案A 同页分区折叠：内容浏览与裂变（三卡+两卡+TOP榜）与按员工表默认收起，
// 点分区标题展开。常驻首屏只留工具条+转化三卡+趋势图，保住转化 vs 内容浏览的
// 同屏对比与下钻闭环，只给首屏减负。
const intentOpen = ref(false)
const staffOpen = ref(false)
const sourceOpen = ref(false)
// 八项汇总：visit/call/save 转化 + projectView/serviceView/videoPlay/share/posterSave 意向。
// 初始值给全八项——缺项的卡片会渲染出「undefined」（后端未跑迁移 0021 时）。
const summary = ref<StatSummary>({
  visit: 0,
  call: 0,
  save: 0,
  projectView: 0,
  serviceView: 0,
  videoPlay: 0,
  share: 0,
  posterSave: 0,
})
const trend = ref<any[]>([])
const topProjects = ref<StatTopItem[]>([])
const topServices = ref<StatTopItem[]>([])
// 流量来源分布 / 裂变榜（迁移 0022 起）：缺字段时给空数组，UI 显示空态而非报错。
const sourceDist = ref<StatSourceItem[]>([])
const fissionTop = ref<StatFissionItem[]>([])
// 来源分布条形占比：以访问总量为分母（未知/其他也计入分母，避免比例和超过 100%）。
const sourceTotal = computed(() =>
  sourceDist.value.reduce((sum, r) => sum + (r.count || 0), 0)
)
function sourceLabel(s: string): string {
  return SOURCE_LABELS[s] || s
}
function sourcePct(c: number): number {
  const t = sourceTotal.value
  return t > 0 ? Math.round((c / t) * 100) : 0
}

// null = 全部时间（不发 from/to）。日期快捷选项不放「全部」——
// 清空输入框（×）即是全部，与后端缺省语义一致。
const range = ref<[string, string] | null>(null)
const rangeShortcuts = [
  { text: '今天', value: () => [dayOffset(0), dayOffset(0)] },
  { text: '昨天', value: () => [dayOffset(-1), dayOffset(-1)] },
  { text: '近 7 天', value: () => [dayOffset(-6), dayOffset(0)] },
  { text: '近 30 天', value: () => [dayOffset(-29), dayOffset(0)] },
]

// 本地时区取日期：与「后端按 TZ（默认 Asia/Shanghai）落库」配合，
// 同处东八区时两边「今天」是同一天；跨时区使用以各自本地日期为准。
function dayOffset(days: number): string {
  const d = new Date()
  d.setDate(d.getDate() + days)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

const rangeLabel = computed(() =>
  range.value ? `${range.value[0]} 至 ${range.value[1]}` : '全部时间（自上线起累计）'
)

// 选择器的 [from, to] 元组 → 接口的 { from, to } 对象（直接传元组会被
// axios 序列化成 from[]=... 的数组参数，后端收不到日期）。
// 取 computed 而非函数：作为 prop 传给明细抽屉时引用稳定，
// 它的多源 watch 才不会在父组件每次重渲染时被误触发。
const rangeParam = computed<StatRange>(() =>
  range.value ? { from: range.value[0], to: range.value[1] } : {}
)

// 范围变化即重查（不含首次：首次由 onMounted 触发，避免双请求）
watch(range, () => {
  load()
})

// 明细抽屉：员工行「明细」/「数据明细」按钮/趋势图点某天/TOP 榜行点击四个入口
const detailOpen = ref(false)
const detailStaffId = ref(0)
// TOP 榜下钻预选：eventType + targetId（+ 对象名，用于抽屉里的筛选标签展示）
const detailEventType = ref('')
const detailTargetId = ref(0)
const detailTargetName = ref('')
// 员工下拉选项取全量 rows（不受关键字/分页影响）——筛明细时要能选到任何人
const staffOptions = computed(() => rows.value.map((r) => ({ staffId: r.staffId, name: r.name })))

function openDetail(staffId = 0, eventType = '', targetId = 0, targetName = '') {
  detailStaffId.value = staffId
  detailEventType.value = eventType
  detailTargetId.value = targetId
  detailTargetName.value = targetName
  detailOpen.value = true
}

// 点趋势图某天：范围收窄到当天 + 打开明细抽屉（一次点击完成「看某天有什么」）
function onTrendPick(date: string) {
  range.value = [date, date] // watch(range) 触发汇总重查；抽屉经 range 变化同步重查
  openDetail()
}

// 点 TOP 榜某行：打开明细并预选该事件类型 + 对象（不筛员工——
// 榜单本身是租户级聚合，带上员工就看不到「这个案例吸引了多少人」）
function onTopRowClick(eventType: 'project_view' | 'service_view', row: StatTopItem) {
  openDetail(0, eventType, row.targetId, row.name)
}

// 「内容浏览」列的悬浮文案：只给三类浏览事件。这里不放转发/海报保存——
// 那两项在单独的「裂变」列里，混进来就等于把两列拆了个寂寞。
function browseTip(row: any): string {
  const parts = [
    `案例浏览 ${row.projectView || 0}`,
    `服务浏览 ${row.serviceView || 0}`,
    `视频播放 ${row.videoPlay || 0}`,
  ]
  return parts.join(' · ')
}

// 「裂变」列的悬浮文案：转发 / 海报保存两项。
function fissionTip(row: any): string {
  const parts = [`转发 ${row.share || 0}`, `海报保存 ${row.posterSave || 0}`]
  return parts.join(' · ')
}

async function load() {
  loading.value = true
  try {
    const r = await adminStats(rangeParam.value)
    summary.value = r.summary
    // 浏览合计 / 裂变合计在前端算：后端每行已给五个分项（CSV 要用分项），
    // 表格只要两个数——多算两列比多传五列更省，也不改接口契约。
    // 两者**不相加**：内容浏览答「访客在看什么」，裂变答「名片被传出去多少」，
    // 后者主体可能是员工本人（见页脚口径说明）。
    rows.value = r.byStaff.map((x: any) => ({
      ...x,
      browse: (x.projectView || 0) + (x.serviceView || 0) + (x.videoPlay || 0),
      fission: (x.share || 0) + (x.posterSave || 0),
    }))
    trend.value = r.trend
    topProjects.value = r.topProjects
    topServices.value = r.topServices
    // 老副本（未跑迁移 0022）缺这两个字段，兜底空数组——UI 显示空态而非崩溃
    sourceDist.value = r.sourceDist || []
    fissionTop.value = r.fissionTop || []
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

async function onExport() {
  try {
    await exportStatsCsv(rangeParam.value)
  } catch (e) {
    // 业务失败（如登录失效）在 api 层已解析出 msg；网络异常走通用文案
    ElMessage.error((e as Error)?.message || '导出失败')
  }
}

// 埋点口径见《首页组件与接口字段映射》§4：只记前端点击、不校验真实结果；
// 本页无写操作，故不需要「查看名片」之类的跳转（PC 端无法直接打开小程序，
// 运营可在小程序首页输入编号预览）。

onMounted(load)
</script>

<style scoped>
.page-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.page-bar h3 {
  margin: 0;
}
.bar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.stat-cards {
  display: flex;
  gap: 16px;
  margin-bottom: 16px;
}
.stat-card {
  flex: 1;
  padding: 16px 20px;
  border: 1px solid #ebeef5;
  border-radius: 6px;
  background: #fafcff;
}
/* 内容浏览/裂变区：卡片横排，窄屏自动换行成 2~3 个一排
   （不跟三卡一起纵向堆叠——满屏高的卡片会把趋势图挤出首屏） */
.intent-block {
  margin-bottom: 4px;
}
.intent-title {
  margin-bottom: 8px;
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.intent-sub {
  margin-left: 8px;
  font-size: 12px;
  font-weight: 400;
  color: #909399;
}
.intent-cards {
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 8px;
}
.intent-cards .stat-card {
  flex: 1 1 140px;
  padding: 12px 16px;
}
.intent-cards .stat-value {
  font-size: 22px;
}
/* TOP 榜：案例/服务左右双栏，窄屏堆叠 */
.top-boards {
  display: flex;
  gap: 16px;
  margin-bottom: 16px;
}
.top-board {
  flex: 1;
  min-width: 0;
}
.top-title {
  margin-bottom: 8px;
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.top-sub {
  margin-left: 8px;
  font-size: 12px;
  font-weight: 400;
  color: #909399;
}
.top-empty {
  margin: 0;
  padding: 18px 0;
  text-align: center;
  color: #909399;
  font-size: 12px;
  background: #fafcff;
  border: 1px dashed #ebeef5;
  border-radius: 6px;
}
/* 流量来源分布：每行 = 标签 + 占比条形 + 计数；条形长度按占访问总量比例 */
.src-block {
  margin-bottom: 16px;
}
.src-rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.src-row {
  display: flex;
  align-items: center;
  gap: 12px;
}
.src-label {
  flex: 0 0 56px;
  font-size: 13px;
  color: #303133;
}
.src-bar-wrap {
  flex: 1;
  height: 12px;
  background: #f0f2f5;
  border-radius: 6px;
  overflow: hidden;
}
.src-bar {
  height: 100%;
  min-width: 2px;
  background: linear-gradient(90deg, #409eff, #66b1ff);
  border-radius: 6px;
  transition: width 0.3s ease;
}
.src-count {
  flex: 0 0 48px;
  text-align: right;
  font-size: 13px;
  font-weight: 600;
  color: #303133;
}
.fission-block {
  margin-top: 4px;
}
.stat-label {
  color: #909399;
  font-size: 13px;
}
.stat-value {
  margin-top: 6px;
  font-size: 28px;
  font-weight: 600;
  color: #303133;
}
.range-tip {
  margin: 0 0 4px;
  color: #909399;
  font-size: 12px;
}
.tip {
  margin: 12px 0 0;
  color: #909399;
  font-size: 12px;
}
/* 分区折叠（方案A 同页分区）：常驻区之外的「内容浏览与裂变」「按员工」默认收起，点标题展开。
   折叠区本身在移动端即承担手风琴角色，配合下方媒体查询的堆叠即可，无需额外机制。 */
.fold {
  margin-bottom: 16px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  background: #fff;
  overflow: hidden;
}
.fold-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  cursor: pointer;
  user-select: none;
  background: #fafcff;
}
.fold-head:hover {
  background: #f2f6fc;
}
.fold-title {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.fold-sub {
  margin-left: 8px;
  font-size: 12px;
  font-weight: 400;
  color: #909399;
}
.fold-arrow {
  color: #909399;
  transition: transform 0.2s ease;
}
.fold.open .fold-arrow {
  transform: rotate(180deg);
}
.fold-body {
  padding: 16px;
}
/* 手机端：三张汇总卡由横排改纵向堆叠，避免窄屏被压扁 */
@media (max-width: 767px) {
  .stat-cards {
    flex-direction: column;
  }
  .stat-card {
    padding: 14px 16px;
  }
  .stat-value {
    font-size: 24px;
  }
  /* 内容浏览/裂变卡片不堆叠（满宽卡片太高），靠换行挤成 2~3 个一排 */
  .intent-cards {
    flex-direction: row;
    gap: 8px;
  }
  .intent-cards .stat-card {
    flex: 1 1 100px;
    padding: 10px 12px;
  }
  .intent-cards .stat-value {
    font-size: 18px;
  }
  /* TOP 榜改上下堆叠：并排两栏在窄屏下每行只剩 3~4 个字 */
  .top-boards {
    flex-direction: column;
    gap: 12px;
  }
}
</style>

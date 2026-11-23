<template>
  <div class="storage-profiles-page">
    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="存储与分发：配置只存一份，谁用由绑定决定"
      description="默认本地磁盘、不开 CDN，直接可用；不给任何企业单独指定时，所有人默认用同一套。配置与绑定只有平台超管能改。"
    />

    <el-tabs v-model="tab" class="storage-tabs">
      <!-- 总览：第一次进来先看这里，默认页签 -->
      <el-tab-pane label="总览" name="overview">
        <StorageOverview
          :platform-profile="platformPrimaryProfile"
          :platform-delivery="platformDelivery"
          :global-binding="globalBinding"
          :company-binding-count="companyBindings.length"
          @goto="tab = $event"
        />
      </el-tab-pane>

      <!-- 文件存储 -->
      <el-tab-pane label="文件存储" name="storage">
        <div class="tab-guide"><strong>{{ TAB_GUIDES.storage.title }}</strong><span>{{ TAB_GUIDES.storage.desc }}</span></div>
        <div class="tab-head tab-head-actions">
          <el-button link type="primary" @click="showTechCols = !showTechCols">{{ showTechCols ? '收起技术列' : '更多列' }}</el-button>
          <el-button type="primary" @click="openStorageDialog(null)">新建文件存储</el-button>
        </div>
        <el-table :data="orderedStorages" v-loading="loading" size="small" border>
          <el-table-column label="名称" min-width="240">
            <template #default="{ row }">
              <div class="name-cell">
                <div class="name-line">
                  <el-tag v-if="roleOf(row.purpose)" size="small" :type="roleOf(row.purpose)!.type">{{ roleOf(row.purpose)!.badge }}</el-tag>
                  <span class="name-text">{{ row.name }}</span>
                  <el-tooltip v-if="row.builtin" placement="top" :content="row.purpose === 'platform_primary' ? '平台自己那份文件的主落点：p/ 平台对象（素材采用 / 小程序码）与未单独指定存储的企业用它。连接信息可以在这里改，但改完必须重启；不可删除、不能绑定给企业，也不能参与迁移' : row.purpose === 'platform_mirror' ? '系统自带的备份盘 · 目录继承自「平台主存储（启动配置）→ 本地镜像目录」（mirror_dir），这里改不了、改那里后需重启才生效；自动运行，请勿删除' : '系统自带的备用盘 · 目录继承自「平台主存储（启动配置）→ 本地上传目录」（upload_dir），这里改不了、改那里后需重启才生效；云端出问题时才用，请勿删除'">
                    <el-tag size="small" type="info" effect="plain">内置</el-tag>
                  </el-tooltip>
                  <el-tag v-if="usageLabel(row.id, 'storage')" size="small" type="success" effect="plain" class="usage-tag">{{ usageLabel(row.id, 'storage') }}</el-tag>
                </div>
                <div class="name-duty">{{ dutyOf(row) }}</div>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="位置" min-width="220">
            <template #default="{ row }">{{ locationOf(row) }}</template>
          </el-table-column>
          <el-table-column v-if="showTechCols" prop="endpoint" label="服务地址" min-width="160" />
          <el-table-column v-if="showTechCols" prop="bucket" label="存储桶" min-width="120" />
          <el-table-column v-if="showTechCols" prop="baseUrl" label="上传返回基址" min-width="180" />
          <el-table-column label="连通性" width="110">
            <template #default="{ row }">
              <el-tooltip placement="top" :content="probeHint(row)">
                <el-tag :type="statusTag(row.status)" size="small">{{ statusText(row.status) }}</el-tag>
              </el-tooltip>
            </template>
          </el-table-column>
          <el-table-column v-if="showTechCols" prop="accessKeyMasked" label="AK 掩码" min-width="120" />
          <el-table-column v-if="showTechCols" prop="version" label="版本" width="70" />
          <el-table-column label="操作" width="220" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openStorageDialog(row)">编辑</el-button>
              <el-button link type="primary" @click="checkStorage(row)">自检</el-button>
              <a v-if="row.builtin" :href="builtinSettingHref(row)" class="builtin-setting-link">跟随系统设置 →</a>
              <span v-if="row.builtin" class="builtin-no-delete">系统自带 · 不可删</span>
              <el-button v-else link type="danger" @click="removeStorage(row)">删除</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div class="table-empty">读不到文件存储列表。系统自带的三条内置存储一定会出现在这里，所以空表意味着数据没加载出来——先刷新页面；仍为空就检查后端服务是否正常。</div>
          </template>
        </el-table>
      </el-tab-pane>

      <!-- CDN 分发 -->
      <el-tab-pane label="CDN 分发" name="delivery">
        <div class="tab-guide"><strong>{{ TAB_GUIDES.delivery.title }}</strong><span>{{ TAB_GUIDES.delivery.desc }}</span></div>
        <div class="tab-head tab-head-actions">
          <el-button type="primary" @click="openDeliveryDialog(null)">新建 CDN 分发</el-button>
        </div>
        <el-table :data="deliveries" v-loading="loading" size="small" border>
          <el-table-column prop="name" label="名称" min-width="150">
            <template #default="{ row }">
              {{ row.name }}
              <el-tooltip v-if="row.builtin" placement="top" :content="deliveryPurposeHint(row)">
                <el-tag size="small" type="info" effect="plain" class="builtin-tag">内置</el-tag>
              </el-tooltip>
              <el-tag v-if="usageLabel(row.id, 'delivery')" size="small" type="success" effect="plain" class="usage-tag">{{ usageLabel(row.id, 'delivery') }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="厂商" width="130">
            <template #default="{ row }">{{ providerLabel(row.provider) }}</template>
          </el-table-column>
          <el-table-column label="公开域名" min-width="220">
            <template #default="{ row }">
              <span v-if="row.purpose === 'platform_primary'" class="muted">
                见「文件存储 → 平台主存储」的访问基址
              </span>
              <span v-else>{{ row.domain }}</span>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tooltip placement="top" :content="probeHint(row)">
                <el-tag :type="statusTag(row.status)" size="small">{{ statusText(row.status) }}</el-tag>
              </el-tooltip>
            </template>
          </el-table-column>
          <el-table-column prop="version" label="版本" width="70" />
          <el-table-column label="操作" width="220" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openDeliveryDialog(row)">编辑</el-button>
              <el-button link type="primary" @click="checkDelivery(row)">自检</el-button>
              <el-button link type="danger" :disabled="row.builtin" @click="removeDelivery(row)">删除</el-button>
              <span v-if="row.builtin" class="builtin-no-delete">系统自带 · 不可删</span>
            </template>
          </el-table-column>
          <template #empty>
            <div class="table-empty">读不到 CDN 分发列表。系统自带的「平台自身 CDN」一定会出现在这里，所以空表意味着数据没加载出来——先刷新页面；仍为空就检查后端服务是否正常。</div>
          </template>
        </el-table>
      </el-tab-pane>

      <!-- 迁移任务 -->
      <el-tab-pane label="迁移任务" name="migration">
        <div class="tab-guide"><strong>{{ TAB_GUIDES.migration.title }}</strong><span>{{ TAB_GUIDES.migration.desc }}</span></div>
        <el-alert
          class="binding-alert"
          type="info"
          :closable="false"
          show-icon
          title="只复制，不删除"
          description="历史对象会完整复制到目标文件存储（含缩略图），源桶对象原样保留，方便回滚与对账。单对象失败会进失败清单，不影响其余对象；仅在零失败时才允许自动切换绑定。视频按流式搬运，不会整段进内存。"
        />
        <div class="migration-card">
          <div class="migration-fields">
            <label>迁移范围
              <el-select v-model="newJob.tenantId">
                <el-option :value="0" label="全平台（整桶）" />
                <el-option v-for="c in companies" :key="c.id" :value="c.id" :label="`企业：${c.name}`" />
              </el-select>
            </label>
            <label>源文件存储
              <el-select v-model="newJob.sourceProfileId">
                <el-option v-for="p in writableStorages" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </label>
            <label>目标文件存储
              <el-select v-model="newJob.targetProfileId">
                <el-option v-for="p in writableStorages" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </label>
          </div>
          <p class="migration-hint">勾「试运行」后点「创建并启动」，只会统计对象数量、不复制；确认数量无误后取消勾选，再建一个正式任务。源桶原样保留，不会被删除。</p>
          <div class="migration-actions">
            <el-checkbox v-model="newJob.dryRun">试运行（只统计不复制）</el-checkbox>
            <el-checkbox v-model="newJob.autoSwitch" :disabled="newJob.dryRun">成功后自动切换绑定</el-checkbox>
            <el-button type="primary" :loading="saving" @click="createJob">创建并启动</el-button>
          </div>
        </div>
        <el-table :data="jobs" v-loading="loading" size="small" border>
          <el-table-column prop="id" label="ID" width="70" />
          <el-table-column label="范围" min-width="130">
            <template #default="{ row }">{{ row.tenantId ? `企业 #${row.tenantId}` : '全平台' }}</template>
          </el-table-column>
          <el-table-column label="源 → 目标" min-width="190">
            <template #default="{ row }">{{ profileName(row.sourceProfileId) }} → {{ profileName(row.targetProfileId) }}</template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="migrationStatus(row.status).type" size="small">{{ migrationStatus(row.status).text }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="进度" min-width="140">
            <template #default="{ row }">
              <el-progress :percentage="percent(row)" :status="row.failed ? 'exception' : undefined" />
              <span class="muted">{{ row.done }}/{{ row.total }}，失败 {{ row.failed }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="error" label="错误" min-width="180" show-overflow-tooltip />
          <el-table-column label="操作" width="210">
            <template #default="{ row }">
              <el-button link type="primary" :disabled="row.status === 'running' || (row.status === 'done' && !row.failed)" @click="startJob(row)">{{ row.status === 'done' && row.failed ? '重试失败' : '启动 / 续跑' }}</el-button>
              <el-button link type="warning" :disabled="row.status !== 'running'" @click="stopJob(row)">取消</el-button>
              <el-button link type="primary" @click="openErrors(row)">失败清单</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div class="table-empty">还没有迁移任务。先把老文件复制过去、核对失败清单为空，再到「绑定关系」切换。</div>
          </template>
        </el-table>
      </el-tab-pane>

      <!-- 绑定关系 -->
      <el-tab-pane label="绑定关系" name="binding">
        <div class="tab-guide"><strong>{{ TAB_GUIDES.binding.title }}</strong><span>{{ TAB_GUIDES.binding.desc }}</span></div>
        <el-alert
          class="binding-alert"
          type="warning"
          :closable="false"
          show-icon
          title="切换全局默认是即时生效的高危操作"
          description="保存前会核对新文件存储是否包含当前全部对象，缺一个就拒绝切换；先搬历史对象请用「迁移任务」。切错可一键回滚，但回滚只换绑定、不搬对象。仅当目标本来就是新桶时，才勾「强制切换」跳过核对。"
        />
        <div class="binding-card">
          <h4>全局默认（所有企业的共同设置）</h4>
          <p v-if="!boundGlobal" class="muted">
            当前未配置绑定：文件存储沿用「文件存储 → 平台主存储」，CDN 刷新沿用「CDN 分发 → 平台自身 CDN」（两处改动都需重启）。
          </p>
          <div class="binding-form">
            <label>文件存储</label>
            <el-select v-model="globalStorageId" placeholder="选择文件存储" style="width: 260px">
              <el-option v-for="p in writableStorages" :key="p.id" :label="p.name" :value="p.id" />
            </el-select>
            <label>CDN 分发</label>
            <el-select v-model="globalDeliveryId" placeholder="不启用 CDN" clearable style="width: 260px">
              <el-option v-for="p in writableDeliveries" :key="p.id" :label="p.name" :value="p.id" />
            </el-select>
            <el-button type="primary" :loading="saving" @click="saveGlobal">保存全局默认</el-button>
            <el-button
              v-if="globalBinding && globalBinding.previousStorageProfileId"
              :loading="saving"
              @click="rollbackGlobal"
            >回滚到上一套（{{ profileName(globalBinding.previousStorageProfileId) }}）</el-button>
          </div>
          <div class="force-row">
            <el-checkbox v-model="forceGlobal">强制切换（目标可能缺对象，跳过核对）</el-checkbox>
            <span class="force-note">只对下一次「保存全局默认」生效，保存后自动取消勾选</span>
          </div>
        </div>
        <div class="company-head">
          <h4 class="company-title">企业覆盖（没有绑定行则继承全局）</h4>
          <div class="company-head-right">
            <el-checkbox v-model="forceCompany">强制切换（目标可能缺对象，跳过核对）</el-checkbox>
            <span class="force-note">只对下一次企业「保存」生效，保存后自动取消勾选</span>
          </div>
        </div>
        <el-table :data="companyBindings" v-loading="loading" size="small" border>
          <el-table-column prop="tenantId" label="企业 ID" width="90" />
          <el-table-column label="企业名称" min-width="160">
            <template #default="{ row }">{{ companyNames[row.tenantId] || `#${row.tenantId}` }}</template>
          </el-table-column>
          <el-table-column label="文件存储" min-width="170">
            <template #default="{ row }">
              <el-select v-model="row.storageProfileId" size="small" style="width:100%">
                <el-option v-for="p in writableStorages" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="CDN 分发" min-width="170">
            <template #default="{ row }">
              <el-select v-model="row.deliveryProfileId" size="small" clearable style="width:100%">
                <el-option v-for="p in writableDeliveries" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="生效来源" width="90">
            <template #default="{ row }">{{ sourceLabel(row.source) }}</template>
          </el-table-column>
          <el-table-column label="切换前的文件存储" min-width="140">
            <template #default="{ row }">
              <span v-if="row.previousStorageProfileId">{{ profileName(row.previousStorageProfileId) }}</span>
              <span v-else class="muted">无</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="220">
            <template #default="{ row }">
              <el-button link type="primary" @click="saveCompany(row)">保存</el-button>
              <el-button
                v-if="row.previousStorageProfileId"
                link
                type="warning"
                @click="rollbackCompany(row)"
              >回滚</el-button>
              <el-button link type="danger" @click="resetCompany(row)">恢复继承</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div class="table-empty">还没有企业单独指定存储，全部继承全局默认。要为企业指定，去「企业管理」列表里点该企业的「文件存储」按钮。</div>
          </template>
        </el-table>
      </el-tab-pane>

      <!-- CDN 缓存刷新是运维动作：放在五步配置流程之后，不打断建档案→迁移→切绑定 -->
      <el-tab-pane label="CDN 缓存刷新" name="refresh">
        <div class="tab-guide"><strong>{{ TAB_GUIDES.refresh.title }}</strong><span>{{ TAB_GUIDES.refresh.desc }}</span></div>
        <CdnRefresh compact @goto-settings="tab = 'delivery'" />
      </el-tab-pane>

      <!-- 平台主存储的部署期配置：连接信息已在「文件存储」标签的那一行里，
           这里只留目录 / 镜像总闸 / CDN 等启动期参数，同样改完重启。 -->
      <el-tab-pane label="平台自身配置" name="platform">
        <div class="tab-guide"><strong>{{ TAB_GUIDES.platform.title }}</strong><span>{{ TAB_GUIDES.platform.desc }}</span></div>
        <StorageSettingsPanel
          :platform-profile="platformPrimaryProfile"
          :platform-delivery="platformDelivery"
          @goto-profiles="tab = 'storage'"
          @goto-deliveries="tab = 'delivery'"
        />
      </el-tab-pane>
    </el-tabs>

    <!-- 文件存储表单 -->
    <el-dialog v-model="storageDialog" :title="storageForm.id ? '编辑文件存储' : '新建文件存储'" width="640px">
      <el-alert v-if="storageForm.builtin && storageForm.purpose === 'platform_primary'" type="warning" :closable="false" show-icon class="dialog-alert"
        title="这是平台自己那份存储：连接信息在这里改，保存后必须重启服务"
        description="p/ 平台对象（素材采用 / 小程序码 / 访客头像）与尚未单独指定存储的企业用它。改完先点「自检」确认凭据与桶可达，再重启后端；不重启的话界面显示新值、实际上传仍走旧实例。它不可删除、不能绑定给企业、也不能参与迁移。本地盘模式下的目录继承自「启动配置 → 本地上传目录」（upload_dir），这里改不了。" />
      <el-alert v-else-if="storageForm.builtin" type="info" :closable="false" show-icon class="dialog-alert"
        title="这是系统自带的文件存储，通常不用改动"
        description="「本地应急盘」是应急备用盘：平时不用，只有云端存储出问题时才顶上，保证还能上传和显示图片。「本地备份盘」是备份保险柜：图片成功传到云端后，系统自动在这里留一份副本，只进不出。两者都由系统自动运行，不可删除、不能改驱动。目录也不在这里改：租户退路的目录继承自「平台主存储（启动配置）→ 本地上传目录」（upload_dir），平台镜像的目录继承自「平台主存储（启动配置）→ 本地镜像目录」（mirror_dir）——两处改动都要到那里改并重启服务才生效。你一般只需要改下面的展示名，其他保持默认即可。" />
      <!-- 跟着当前驱动变的小抄：默认收起，已经知道去哪拿凭据的人不用看 -->
      <div v-if="currentDriverGuide" class="vendor-guide">
        <button type="button" class="vendor-guide-toggle" :aria-expanded="guideOpen ? 'true' : 'false'" @click="guideOpen = !guideOpen">
          <strong>{{ currentDriverGuide.label }}</strong>
          <span class="vendor-guide-q">{{ guideOpen ? '收起配置说明' : '不知道这几项去哪拿？' }}</span>
          <span class="vendor-guide-arrow">{{ guideOpen ? '↑' : '↓' }}</span>
        </button>
        <div v-if="guideOpen" class="vendor-guide-body">
          <p>{{ currentDriverGuide.summary }}</p>
          <p>适合：{{ currentDriverGuide.when }}</p>
          <p v-if="currentDriverGuide.credential">密钥怎么拿：{{ currentDriverGuide.credential }}</p>
          <div v-if="currentDriverGuide.links.length" class="guide-links">
            <a v-for="link in currentDriverGuide.links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">{{ link.label }} ↗</a>
          </div>
          <table v-if="storageForm.driver === 's3'" class="variant-table">
            <thead><tr><th>服务商</th><th>服务地址示例</th><th>怎么填</th></tr></thead>
            <tbody>
              <tr v-for="v in S3_VARIANTS" :key="v.label"><td>{{ v.label }}</td><td><code>{{ v.endpoint }}</code></td><td>{{ v.note }}</td></tr>
            </tbody>
          </table>
        </div>
      </div>

      <el-form label-width="130px" size="small">
        <el-form-item>
          <template #label>名称 <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'name')" /></template>
          <el-input v-model="storageForm.name" placeholder="例如：主站 OSS" />
        </el-form-item>
        <el-form-item>
          <template #label>存储方式 <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'driver')" /></template>
          <el-select v-model="storageForm.driver" style="width: 100%" :disabled="storageForm.builtin && storageForm.purpose !== 'platform_primary'">
            <el-option v-for="d in drivers" :key="d.value" :label="d.label" :value="d.value" />
          </el-select>
          <span v-if="storageForm.builtin && storageForm.purpose === 'platform_primary'" class="field-note">
            平台主存储可选本地或任意云驱动，改完必须重启服务才生效。
          </span>
          <span v-else-if="storageForm.builtin" class="field-note">
            驱动锁定为「本地磁盘」，不可删除、也不能改成云存储；这是系统预留的备用盘，你只需改展示名，其余保持默认。
          </span>
        </el-form-item>
        <template v-if="storageForm.driver !== 'local'">
          <el-form-item>
            <template #label>服务地址 <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'endpoint')" /></template>
            <el-input v-model="storageForm.endpoint" placeholder="host[:port]，不要带 http://" />
          </el-form-item>
          <el-form-item>
            <template #label>存储桶名 <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'bucket')" /></template>
            <el-input v-model="storageForm.bucket" />
          </el-form-item>
          <el-form-item>
            <template #label>地域 <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'region')" /></template>
            <el-input v-model="storageForm.region" placeholder="S3 兼容桶可留空" />
          </el-form-item>
          <el-form-item>
            <template #label>访问基址 <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'baseUrl')" /></template>
            <el-input v-model="storageForm.baseUrl" placeholder="https://your-domain/uploads" />
          </el-form-item>
          <el-form-item>
            <template #label>主机头（高级） <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'hostHeader')" /></template>
            <el-input v-model="storageForm.hostHeader" placeholder="留空=路径风格；<bucket>.域名=虚拟主机风格" />
          </el-form-item>
          <el-form-item>
            <template #label>使用 HTTPS <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'useSsl')" /></template>
            <el-switch v-model="storageForm.useSsl" />
          </el-form-item>
          <el-form-item label="公开读桶">
            <el-switch v-model="storageForm.publicRead" disabled />
            <span class="field-note">当前只支持公开读桶：私有桶需要限时签名链路，后端会拒绝 publicRead=false</span>
          </el-form-item>
          <el-form-item>
            <template #label>AccessKey <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'accessKey')" /></template>
            <el-input v-model="storageForm.accessKey" :placeholder="storageForm.id ? '留空表示不修改' : '必填'" />
            <span v-if="storageForm.accessKeyMasked" class="field-note">当前：{{ storageForm.accessKeyMasked }}</span>
          </el-form-item>
          <el-form-item>
            <template #label>SecretKey <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'secretKey')" /></template>
            <el-input v-model="storageForm.secretKey" type="password" show-password :placeholder="storageForm.id ? '留空表示不修改' : '必填'" />
          </el-form-item>
        </template>
        <template v-else>
          <el-form-item>
            <template #label>本地根目录 <FieldHelp v-bind="help(STORAGE_FIELD_GUIDES, 'localDir')" /></template>
            <el-input v-model="storageForm.localDir" :disabled="storageForm.builtin" placeholder="留空使用平台 uploads 目录" />
            <span v-if="storageForm.builtin && storageForm.purpose === 'platform_mirror'" class="field-note">
              这是备份保险柜：备份目录继承自「平台主存储 → 本地镜像目录」（mirror_dir），改那里后需重启才生效；图片传到云端后系统自动在这里留一份副本。
              它不参与任何租户绑定，也不能用于迁移——也就是说它只做备份、不会被当成正式的存取盘。要换位置请改系统设置后重启。
            </span>
            <span v-else-if="storageForm.builtin" class="field-note">
              系统自带文件存储的目录继承自「平台主存储（启动配置）→ 本地上传目录」（upload_dir），网站的图片读取用的也是这个位置。
              所以这里不用改、也不建议改；真要换位置请到那里改后重启服务，单独改这里会让图片找不到。
            </span>
            <span v-else class="field-note">迁移到另一套本地文件存储时，两边要填不同目录，否则源对象会被同名覆盖。</span>
          </el-form-item>
        </template>
      </el-form>
      <p class="dialog-footnote">凭据以明文入库、回显只给掩码，SecretKey 保存后不再回传；请确保数据库备份与访问控制到位。</p>
      <template #footer>
        <el-button @click="storageDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitStorage">保存</el-button>
      </template>
    </el-dialog>

    <!-- CDN 分发表单 -->
    <el-dialog v-model="deliveryDialog" :title="deliveryForm.id ? '编辑 CDN 分发' : '新建 CDN 分发'" width="600px">
      <div v-if="currentCdnGuide" class="vendor-guide">
        <button type="button" class="vendor-guide-toggle" :aria-expanded="guideOpen ? 'true' : 'false'" @click="guideOpen = !guideOpen">
          <strong>{{ currentCdnGuide.label }}</strong>
          <span class="vendor-guide-q">{{ guideOpen ? '收起配置说明' : '不知道这几项去哪拿？' }}</span>
          <span class="vendor-guide-arrow">{{ guideOpen ? '↑' : '↓' }}</span>
        </button>
        <div v-if="guideOpen" class="vendor-guide-body">
          <p>{{ currentCdnGuide.summary }}</p>
          <p>适合：{{ currentCdnGuide.when }}</p>
          <p v-if="currentCdnGuide.credential">密钥怎么拿：{{ currentCdnGuide.credential }}</p>
          <div v-if="currentCdnGuide.links.length" class="guide-links">
            <a v-for="link in currentCdnGuide.links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">{{ link.label }} ↗</a>
          </div>
        </div>
      </div>
      <el-form label-width="130px" size="small">
        <el-form-item>
          <template #label>名称 <FieldHelp v-bind="help(DELIVERY_FIELD_GUIDES, 'name')" /></template>
          <el-input v-model="deliveryForm.name" />
        </el-form-item>
        <el-form-item>
          <template #label>CDN 厂商 <FieldHelp v-bind="help(DELIVERY_FIELD_GUIDES, 'provider')" /></template>
          <el-select v-model="deliveryForm.provider" style="width: 100%">
            <el-option v-for="p in providers" :key="p.value" :label="p.label" :value="p.value" />
          </el-select>
        </el-form-item>
        <template v-if="deliveryForm.provider !== 'none'">
          <!-- 平台自身 CDN 不承载域名：平台对象的出参域名唯一真值源是文件存储的
               访问基址，这里再存一份必然出现「改了基址、这里还是旧域名」。 -->
          <el-alert
            v-if="deliveryForm.purpose === 'platform_primary'"
            type="info"
            :closable="false"
            class="delivery-domain-note"
            title="平台域名不在这里配"
            description="平台对象（p/ 前缀）的访问域名以「文件存储 → 平台主存储」里的访问基址为准，这条 CDN 分发只提供 CDN 厂商与刷新凭据。改完保存后需重启服务。"
          />
          <template v-else>
            <el-form-item>
              <template #label>公开域名 <FieldHelp v-bind="help(DELIVERY_FIELD_GUIDES, 'domain')" /></template>
              <el-input v-model="deliveryForm.domain" placeholder="https://cdn.example.com" />
            </el-form-item>
          </template>
          <el-form-item>
            <template #label>刷新接口地址 <FieldHelp v-bind="help(DELIVERY_FIELD_GUIDES, 'endpoint')" /></template>
            <el-input v-model="deliveryForm.endpoint" placeholder="留空用厂商默认" />
          </el-form-item>
          <el-form-item>
            <template #label>资源标识 <FieldHelp v-bind="help(DELIVERY_FIELD_GUIDES, 'resourceId')" /></template>
            <el-input v-model="deliveryForm.resourceId" placeholder="Cloudflare Zone ID / CloudFront Distribution ID" />
          </el-form-item>
          <el-form-item>
            <template #label>AccessKey / Token <FieldHelp v-bind="help(DELIVERY_FIELD_GUIDES, 'accessKey')" /></template>
            <el-input v-model="deliveryForm.accessKey" />
          </el-form-item>
          <el-form-item>
            <template #label>SecretKey <FieldHelp v-bind="help(DELIVERY_FIELD_GUIDES, 'secretKey')" /></template>
            <el-input v-model="deliveryForm.secretKey" type="password" show-password />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="deliveryDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitDelivery">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="errorDialog" title="迁移失败对象" width="720px">
      <el-table :data="errorRows" size="small" border max-height="400">
        <el-table-column prop="objectKey" label="对象键" min-width="240" />
        <el-table-column prop="error" label="失败原因" min-width="260" />
      </el-table>
      <template #footer>
        <el-button @click="errorDialog = false">关闭</el-button>
        <el-button type="primary" @click="exportErrors">导出 CSV</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  listStorageProfiles, saveStorageProfile, deleteStorageProfile, checkStorageProfile,
  listDeliveryProfiles, saveDeliveryProfile, deleteDeliveryProfile, checkDeliveryProfile,
  listStorageBindings, saveGlobalBinding, saveCompanyBinding, resetCompanyBinding,
  rollbackGlobalBinding, rollbackCompanyBinding,
  listMigrationJobs, createMigrationJob, runMigrationJob, cancelMigrationJob, listMigrationErrors,
  type StorageProfile, type DeliveryProfile, type StorageBinding, type MigrationJob, type MigrationError,
} from '@/api/storageProfiles'
import { listCompanies, type CompanyItem } from '@/api/tenants'
import StorageSettingsPanel from '@/components/StorageSettingsPanel.vue'
import CdnRefresh from './CdnRefresh.vue'
import StorageOverview from '@/components/StorageOverview.vue'
import FieldHelp from '@/components/FieldHelp.vue'
import {
  TAB_GUIDES, S3_VARIANTS,
  STORAGE_FIELD_GUIDES, DELIVERY_FIELD_GUIDES, driverGuide, cdnGuide,
  type FieldGuide,
} from '@/utils/storageGuides'
// 厂商列必须显示中文名：同一模块里「CDN 缓存刷新」已经显示「阿里云 CDN」，
// 列表却显示 aliyun 原始 code，会让人以为是两个不同字段。
import { cdnProviderLabel } from '@/utils/cdn'

// 支持 ?tab=xxx 直达指定页签，?focus=<key> 由「平台主存储」面板消费。
// 第一次进来落在「总览」：先看懂架构和现状，再去改配置。
const VALID_TABS = ['overview', 'storage', 'delivery', 'refresh', 'migration', 'binding', 'platform']
const initialTab = typeof window !== 'undefined'
  ? (new URLSearchParams(window.location.search).get('tab') || 'overview')
  : 'overview'
const tab = ref(VALID_TABS.includes(initialTab) ? initialTab : 'overview')
const loading = ref(false)
const saving = ref(false)
// 弹窗里的厂商小抄默认收起：知道去哪拿凭据的人不用先读一屏说明
const guideOpen = ref(false)
// AK 掩码与版本号是排障用的技术列，默认收起，别让管理员第一眼先看到它们
const showTechCols = ref(false)
const storages = ref<StorageProfile[]>([])
// 两套系统自带的文件存储绝不能出现在绑定 / 迁移下拉里（后端也会拒）：
// 镜像只收备份副本，平台主存储装的是 p/ 平台对象，绑给企业会把两边字节写混。
const writableStorages = computed(() => storages.value.filter(
  (p) => p.purpose !== 'platform_mirror' && p.purpose !== 'platform_primary',
))

// 业务存储在前，三条内置机制压到最后：管理员日常维护的是自己建的那几套；
// 内置三条是「平台的运行机制」，不是他要在列表里挑的方案。
const BUILTIN_ORDER: Record<string, number> = {
  platform_primary: 0,
  tenant_fallback: 1,
  platform_mirror: 2,
}
const orderedStorages = computed(() => [...storages.value].sort((a, b) => {
  const au = a.purpose ? 1 : 0
  const bu = b.purpose ? 1 : 0
  if (au !== bu) return au - bu
  if (au === 1) {
    const d = (BUILTIN_ORDER[a.purpose as string] ?? 9) - (BUILTIN_ORDER[b.purpose as string] ?? 9)
    if (d !== 0) return d
  }
  return a.id - b.id
}))

// 名字本身说不清职责，所以用徽标把「这一行在系统里是什么角色」摆在最前面：
// 主存储 / 应急 / 备份，三个词互不相似，比三个近义地名好认。
function roleOf(purpose?: string) {
  switch (purpose) {
    case 'platform_primary': return { badge: '主存储', type: 'primary' as const }
    case 'tenant_fallback': return { badge: '应急', type: 'warning' as const }
    case 'platform_mirror': return { badge: '备份', type: 'success' as const }
    default: return null
  }
}
const DRIVER_LABELS: Record<string, string> = {
  local: '本地磁盘',
  minio: 'MinIO / S3 兼容',
  oss: '阿里云 OSS',
  s3: 'S3 兼容云',
}
// 每行一句话讲清「它替系统扛什么」，不用悬停也不用点开说明。
function dutyOf(row: { purpose?: string }) {
  switch (row.purpose) {
    case 'platform_primary': return '文件写在这里，访客也从这里读；没单独指定存储的企业都用它'
    case 'tenant_fallback': return '主存储连不上时，上传还能落到这里——它保的是「还能用」'
    case 'platform_mirror': return '上传成功后自动再留一份本地副本，只写不读，用来找回'
    default: return '可以绑定给企业，也可以作为迁移的源或目标'
  }
}
// 本地盘没有 endpoint / bucket，三列全空；合成一列「位置」才说得清文件到底在哪。
function locationOf(row: { driver?: string; localDir?: string; bucket?: string }) {
  if ((row.driver || '').trim() === 'local') {
    const dir = String(row.localDir || '').trim()
    return dir ? `本地磁盘 · ${dir}` : '本地磁盘 · 目录继承系统设置'
  }
  const label = DRIVER_LABELS[(row.driver || '').trim()] || row.driver || '云存储'
  return `${label} · ${(row.bucket || '').trim() || '未填存储桶'}`
}
// 顶部摘要只读「平台主存储」这一行，告诉管理员文件实际存在哪；
// 改连接信息仍去「文件存储」标签，不在启动配置面板里重复一套表单。
const platformPrimaryProfile = computed(
  () => storages.value.find((p) => p.purpose === 'platform_primary') || null,
)
const deliveries = ref<DeliveryProfile[]>([])
// 平台自身 CDN 这条分发不能绑给企业：它的 domain 为空（平台域名真值源是文件存储的
// 访问基址），绑上去企业自己的 CDN 域名反而失效，看起来「配了 CDN」实际全站走平台域名。
// 后端 validateBindingTargets 同样拒绝，这里只是让下拉里看不到。
const writableDeliveries = computed(() => deliveries.value.filter(
  (p) => p.purpose !== 'platform_primary',
))
// 平台自身 CDN：面板与 CDN 刷新页都靠它显示「现在用哪家厂商」。
const platformDelivery = computed(
  () => deliveries.value.find((p) => p.purpose === 'platform_primary') || null,
)
function sourceLabel(source?: string) {
  if (source === 'global') return '全局'
  if (source === 'inherit') return '继承'
  if (source === 'platform') return '平台自带'
  if (source === 'profile') return '单独绑定'
  return source || '—'
}
function deliveryPurposeHint(row: { purpose?: string }) {
  if (row.purpose !== 'platform_primary') return ''
  return '平台自身 CDN：只服务 p/ 平台对象与未绑定 CDN 分发的企业，不可删除、不可绑定给企业；域名见文件存储的访问基址，改完需重启'
}
const jobs = ref<MigrationJob[]>([])
const companies = ref<CompanyItem[]>([])
const companyBindings = ref<StorageBinding[]>([])
const globalBinding = ref<StorageBinding | null>(null)
const globalStorageId = ref<number | undefined>(undefined)
const globalDeliveryId = ref<number | undefined>(undefined)
const boundGlobal = computed(() => !!globalStorageId.value)

// 在档案列表里直接回答「谁在用这套」，不用跳到绑定页逐行对 ID。
// 读的是已保存的绑定（不是表单里的草稿值），避免还没保存就误标「使用中」。
function countUses(rows: StorageBinding[], key: 'storageProfileId' | 'deliveryProfileId') {
  const counts = new Map<number, number>()
  for (const row of rows) {
    const id = row[key]
    if (id) counts.set(id, (counts.get(id) || 0) + 1)
  }
  return counts
}
const storageUseCounts = computed(() => countUses(companyBindings.value, 'storageProfileId'))
const deliveryUseCounts = computed(() => countUses(companyBindings.value, 'deliveryProfileId'))
function usageLabel(id: number | undefined, kind: 'storage' | 'delivery') {
  if (!id) return ''
  const isStorage = kind === 'storage'
  const globalId = isStorage ? globalBinding.value?.storageProfileId : globalBinding.value?.deliveryProfileId
  if (globalId === id) return '全局默认'
  const n = (isStorage ? storageUseCounts.value : deliveryUseCounts.value).get(id) || 0
  return n ? `${n} 家企业` : ''
}
// 强制切换默认关：正常切换必须先迁移完整。只有确认目标本就是新桶时才勾。
const forceGlobal = ref(false)
const forceCompany = ref(false)
const companyNames = ref<Record<number, string>>({})

const drivers = [
  { value: 'minio', label: 'MinIO / S3 兼容' },
  { value: 'oss', label: '阿里云 OSS' },
  { value: 's3', label: 'S3（腾讯云 COS 等）' },
  { value: 'local', label: '本地磁盘' },
]
const providers = [
  { value: 'none', label: '不启用 CDN' },
  { value: 'aliyun', label: '阿里云 CDN' },
  { value: 'tencent', label: '腾讯云 CDN' },
  { value: 'qiniu', label: '七牛云 CDN' },
  { value: 'cloudflare', label: 'Cloudflare' },
  { value: 'cloudfront', label: 'AWS CloudFront' },
]

const emptyStorage = () => ({
  id: 0, name: '', driver: 'minio', endpoint: '', bucket: '', region: '', baseUrl: '',
  hostHeader: '', useSsl: true, localDir: '', publicRead: true, purpose: '' as StorageProfile['purpose'],
  accessKey: '', secretKey: '', accessKeyMasked: '', builtin: false,
})
const emptyDelivery = () => ({
  id: 0, name: '', provider: 'aliyun', domain: '', endpoint: '',
  resourceId: '', accessKey: '', secretKey: '', accessKeyMasked: '',
  purpose: '' as DeliveryProfile['purpose'], builtin: false,
})

const storageDialog = ref(false)
const storageForm = reactive(emptyStorage())
const deliveryDialog = ref(false)
const deliveryForm = reactive(emptyDelivery())

// 弹窗里的小抄：跟着当前选中的驱动 / 厂商换内容，给出官方入口与"值去哪拿"。
const currentDriverGuide = computed(() => driverGuide(storageForm.driver))
const currentCdnGuide = computed(() => cdnGuide(deliveryForm.provider))
// FieldGuide 与 FieldHelp 的 props 同名换算，表单里一行一个 v-bind 即可。
function help(map: Record<string, FieldGuide>, key: string, override?: string) {
  const g = map[key]
  return {
    title: override || g?.label || key,
    usage: g?.usage || '',
    source: g?.source,
    rules: g?.rules,
    note: g?.note,
  }
}

const errorDialog = ref(false)
const errorRows = ref<MigrationError[]>([])
const newJob = reactive({ tenantId: 0, sourceProfileId: 0, targetProfileId: 0, dryRun: false, autoSwitch: false })

function statusTag(s: string) {
  return s === 'ok' ? 'success' : s === 'error' ? 'danger' : 'info'
}
function statusText(s: string) {
  return s === 'ok' ? '已验证' : s === 'error' ? '失败' : '未自检'
}
// 「已验证」只说明上一次自检通过过，不说明现在还行：3 分钟前和 3 天前是两回事。
// 时间戳后端本来就有（lastProbeAt），只是之前没显示，等于把判断依据藏起来了。
function probeHint(row: { status?: string; lastProbeAt?: string | null }) {
  if (row.lastProbeAt) return `上次自检：${formatTime(row.lastProbeAt)}`
  return row.status === 'ok' ? '自检通过，但没有记录到时间' : '还没自检过，点右侧「自检」跑一次'
}
function formatTime(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
// 厂商列不能直接渲染后端原始值：同模块的「CDN 缓存刷新」显示「阿里云 CDN」，
// 列表却显示 aliyun，同一屏两套口径，排查时最容易以为这是两个不同字段。
function providerLabel(p?: string) {
  const v = (p || '').trim()
  if (!v || v === 'none') return '不启用 CDN'
  return cdnProviderLabel(v)
}
// 迁移状态同理：running / done / failed 是给代码看的，管理员要的是中文。
const MIGRATION_STATUS: Record<string, { text: string; type: 'success' | 'warning' | 'danger' | 'info' }> = {
  // 后端建任务时写的是 "created"，不是 "pending"；漏了它新任务会显示英文。
  created: { text: '待启动', type: 'info' },
  pending: { text: '待启动', type: 'info' },
  running: { text: '进行中', type: 'warning' },
  done: { text: '已完成', type: 'success' },
  failed: { text: '失败', type: 'danger' },
  canceled: { text: '已取消', type: 'info' },
}
function migrationStatus(s?: string) {
  const key = (s || '').trim()
  // 后端加了新状态也不许留空标签：原样回显，至少让人知道有这回事。
  return MIGRATION_STATUS[key] || { text: key || '未知', type: 'info' as const }
}
// 系统自带文件存储的目录 / 基址在启动时由 cfg 单向同步，界面改不动。
// 给一条直达链接，避免管理员在这里反复点「编辑」却改不了字段。
// 配置已集中到本模块的「平台主存储」标签；focus 参数让面板加载后滚到对应行。
function builtinSettingHref(row: { purpose?: string }) {
  const key = row.purpose === 'platform_mirror' ? 'mirror_dir' : 'upload_dir'
  return `/platform/storage-profiles?tab=platform&focus=${key}`
}
function profileName(id: number) {
  return storages.value.find((s) => s.id === id)?.name || `#${id}`
}
function percent(row: any) {
  return row.total ? Math.min(100, Math.round((row.done / row.total) * 100)) : 0
}

async function load() {
  loading.value = true
  try {
    const [ss, ds, bs, js, cs] = await Promise.all([
      listStorageProfiles(), listDeliveryProfiles(), listStorageBindings(),
      listMigrationJobs(), listCompanies({ page: 1, size: 200 }),
    ])
    storages.value = ss
    deliveries.value = ds
    jobs.value = js
    companies.value = cs.list || []
    companyNames.value = companies.value.reduce((m, c) => {
      m[c.id] = c.name
      return m
    }, {} as Record<number, string>)
    const g: any = bs.global
    globalBinding.value = g && g.storageProfileId ? (g as StorageBinding) : null
    globalStorageId.value = g?.storageProfileId || undefined
    globalDeliveryId.value = g?.deliveryProfileId || undefined
    // 后端把 tenant_id=0 的全局行也放进了 companies，直接渲染会在「企业覆盖」
    // 表里多出一行「企业 ID 0 / #0」。归属判定与计数都只该看真实企业。
    companyBindings.value = (bs.companies || []).filter((r) => r.tenantId > 0)
  } finally {
    loading.value = false
  }
}

onMounted(load)

// 「进度会自动刷新」这句话必须是真的。之前只有一句提示、没有任何定时器，
// 启动后界面一直停在启动前的数字，管理员只能靠切页签重进才看到进度。
// 只在「迁移任务」页签且确实有 running 任务时轮询：离开页签或任务结束立刻停。
const POLL_MS = 3000
let pollTimer: ReturnType<typeof setInterval> | null = null
const hasRunningJob = computed(() => jobs.value.some((j) => j.status === 'running'))
async function refreshJobs() {
  try {
    // 不置 loading：否则整张表每 3 秒闪一次遮罩，比不刷新更难用。
    jobs.value = await listMigrationJobs()
  } catch {
    // 轮询是锦上添花，网络抖一下不该把整页打成错误态，下个周期再试。
  }
}
function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}
watch([tab, hasRunningJob], () => {
  const need = tab.value === 'migration' && hasRunningJob.value
  if (need && !pollTimer) pollTimer = setInterval(refreshJobs, POLL_MS)
  else if (!need) stopPolling()
})
onUnmounted(stopPolling)

function openStorageDialog(row: any | null) {
  Object.assign(storageForm, emptyStorage(), row || {})
  storageForm.accessKey = ''
  storageForm.secretKey = ''
  guideOpen.value = false
  storageDialog.value = true
}
async function submitStorage() {
  if (!storageForm.name.trim()) return ElMessage.warning('请填写名称')
  saving.value = true
  try {
    await saveStorageProfile(storageForm.id || null, { ...storageForm })
    ElMessage.success('已保存')
    storageDialog.value = false
    await load()
  } finally {
    saving.value = false
  }
}
async function checkStorage(row: any) {
  const r = await checkStorageProfile(row.id)
  ElMessage[r.ok ? 'success' : 'warning'](r.ok ? r.detail : r.error)
  await load()
}
async function removeStorage(row: any) {
  await ElMessageBox.confirm(`删除「${row.name}」？仍被绑定时会被拒绝。`, '确认删除', { type: 'warning' })
  await deleteStorageProfile(row.id)
  ElMessage.success('已删除')
  await load()
}

function openDeliveryDialog(row: any | null) {
  Object.assign(deliveryForm, emptyDelivery(), row || {})
  deliveryForm.accessKey = ''
  deliveryForm.secretKey = ''
  guideOpen.value = false
  deliveryDialog.value = true
}
async function submitDelivery() {
  if (!deliveryForm.name.trim()) return ElMessage.warning('请填写名称')
  saving.value = true
  try {
    await saveDeliveryProfile(deliveryForm.id || null, { ...deliveryForm })
    ElMessage.success('已保存')
    deliveryDialog.value = false
    await load()
  } finally {
    saving.value = false
  }
}
async function checkDelivery(row: any) {
  const r = await checkDeliveryProfile(row.id)
  ElMessage[r.ok ? 'success' : 'warning'](r.ok ? r.detail : r.error)
  await load()
}
async function removeDelivery(row: any) {
  await ElMessageBox.confirm(`删除「${row.name}」？仍被绑定时会被拒绝。`, '确认删除', { type: 'warning' })
  await deleteDeliveryProfile(row.id)
  ElMessage.success('已删除')
  await load()
}

async function saveGlobal() {
  if (!globalStorageId.value) return ElMessage.warning('请选择文件存储')
  saving.value = true
  try {
    // force 与后端一致地作用于下一次保存；错误（含缺失对象 key）由请求层原样弹出，
    // 这里不吞掉，避免「保存失败」变成一句没有信息的空话。
    await saveGlobalBinding(globalStorageId.value, globalDeliveryId.value || 0, forceGlobal.value)
    // 强制切换是「一次性放行」，用完立刻复位：留着勾选会让下一次切换也静默跳过核对。
    forceGlobal.value = false
    ElMessage.success('全局默认已切换，下一个请求即生效')
    await load()
  } finally {
    saving.value = false
  }
}
async function rollbackGlobal() {
  saving.value = true
  try {
    await rollbackGlobalBinding()
    ElMessage.success('已回滚到切换前的文件存储')
    await load()
  } finally {
    saving.value = false
  }
}
async function saveCompany(row: any) {
  if (!row.storageProfileId) return ElMessage.warning('请选择文件存储')
  await saveCompanyBinding(row.tenantId, row.storageProfileId, row.deliveryProfileId || 0, forceCompany.value)
  // 与全局默认一致：一次性放行，不许残留成「以后都不核对」。
  forceCompany.value = false
  ElMessage.success('已保存')
  await load()
}
async function rollbackCompany(row: any) {
  await rollbackCompanyBinding(row.tenantId)
  ElMessage.success('已回滚到切换前的文件存储')
  await load()
}
async function resetCompany(row: any) {
  await resetCompanyBinding(row.tenantId)
  ElMessage.success('已恢复继承全局')
  await load()
}

async function createJob() {
  if (!newJob.sourceProfileId || !newJob.targetProfileId) return ElMessage.warning('请选择源与目标文件存储')
  if (newJob.sourceProfileId === newJob.targetProfileId) return ElMessage.warning('源与目标不能相同')
  saving.value = true
  try {
    const job = await createMigrationJob({ ...newJob })
    // 建完还要再点一次「启动」是多余的一步。除非启动这一步失败，
    // 否则创建即开跑；启动失败也不能把「任务已创建」这件事吞掉。
    if (job?.id) {
      try {
        await runMigrationJob(job.id)
        ElMessage.success(newJob.dryRun
          ? '试运行已启动，跑完看「进度」里的对象数量'
          : '任务已启动，进度每 3 秒自动刷新')
      } catch {
        ElMessage.warning('任务已创建，但启动失败；可在列表里点「启动 / 续跑」重试')
      }
    } else {
      ElMessage.success('任务已创建，点「启动 / 续跑」开始')
    }
    await load()
  } finally {
    saving.value = false
  }
}
async function startJob(row: any) {
  await runMigrationJob(row.id)
  ElMessage.success('已启动，进度每 3 秒自动刷新')
  await load()
}
async function stopJob(row: any) {
  await cancelMigrationJob(row.id)
  ElMessage.success('已取消')
  await load()
}
async function openErrors(row: any) {
  errorRows.value = await listMigrationErrors(row.id)
  errorDialog.value = true
}
function exportErrors() {
  const lines = ['objectKey,error', ...errorRows.value.map((e) => `"${e.objectKey.replace(/"/g, '""')}","${e.error.replace(/"/g, '""')}"`)]
  const blob = new Blob(['\ufeff' + lines.join('\n')], { type: 'text/csv;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = 'storage-migration-errors.csv'
  a.click()
  URL.revokeObjectURL(a.href)
}
</script>

<style scoped>
.storage-profiles-page { padding: 4px 0; }
.storage-tabs { margin-top: 14px; }
.tab-head { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 12px; }
.tab-head-actions { justify-content: flex-end; }
.binding-alert, .dialog-alert { margin-bottom: 14px; }
.binding-card { border: 1px solid #ebeef5; border-radius: 6px; padding: 14px 16px; margin-bottom: 18px; }
.binding-card h4 { margin: 0 0 10px; font-size: 14px; }
.binding-form { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.force-row { margin-top: 10px; }
.force-note { margin-left: 10px; color: #c0c4cc; font-size: 12px; }
.company-head-right { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.company-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.binding-form label, .migration-fields label { font-size: 13px; color: #606266; }
.usage-tag { margin-left: 6px; }
.name-cell { display: flex; flex-direction: column; gap: 3px; }
.name-line { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.name-text { font-weight: 500; }
.name-duty { color: #909399; font-size: 12px; line-height: 1.5; }
.company-title { font-size: 14px; margin: 0 0 10px; }
.migration-card { padding: 14px 16px; margin-bottom: 16px; border: 1px solid #ebeef5; border-radius: 8px; background: #fafcff; }
.migration-fields { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.migration-fields label { display: flex; flex-direction: column; gap: 6px; }
.migration-fields .el-select, .migration-fields select.el-select { width: 100%; }
.migration-hint { margin: 12px 0; color: #909399; font-size: 12px; }
.migration-actions { display: flex; align-items: center; gap: 18px; flex-wrap: wrap; }
.migration-actions .el-button { margin-left: auto; }
.muted { color: #909399; font-size: 12px; }
.field-note { display: block; color: #909399; font-size: 12px; line-height: 1.5; }
.builtin-no-delete { color: #c0c4cc; font-size: 12px; cursor: default; }
.builtin-setting-link { margin-right: 10px; font-size: 12px; color: #409eff; text-decoration: none; white-space: nowrap; }
.builtin-setting-link:hover { text-decoration: underline; }
/* 页签说明压成一行淡灰字：信息还在，但不再每页挂一条蓝色横幅 */
.tab-guide { margin: -4px 0 12px; color: #909399; font-size: 12px; line-height: 1.5; }
.tab-guide strong { margin-right: 8px; color: #606266; font-weight: 600; }
.tab-guide span { color: #909399; }
.vendor-guide { margin-bottom: 14px; border: 1px solid #ebeef5; border-radius: 6px; background: #fafcff; }
.vendor-guide-toggle { display: flex; align-items: center; gap: 8px; width: 100%; padding: 8px 12px; border: 0; background: transparent; font-size: 12px; text-align: left; cursor: pointer; }
.vendor-guide-toggle strong { font-size: 12px; color: #303133; }
.vendor-guide-q { color: #409eff; }
.vendor-guide-arrow { margin-left: auto; color: #909399; }
.vendor-guide-body { padding: 0 12px 10px; }
.vendor-guide-body p { margin: 0 0 6px; color: #606266; font-size: 12px; line-height: 1.7; }
.guide-links { display: flex; flex-wrap: wrap; gap: 8px 14px; margin-top: 4px; }
.guide-links a { color: #409eff; font-size: 12px; text-decoration: none; }
.guide-links a:hover { text-decoration: underline; }
.variant-table { width: 100%; margin-top: 10px; border-collapse: collapse; font-size: 12px; }
.variant-table th, .variant-table td { padding: 6px 8px; border: 1px solid #e4e7ed; text-align: left; color: #606266; }
.variant-table th { background: #fff; }
.dialog-footnote { margin: 2px 0 0; color: #a8abb2; font-size: 12px; line-height: 1.6; }
.table-empty { padding: 28px 16px; color: #909399; font-size: 13px; line-height: 1.8; }
@media (max-width: 900px) {
  .migration-fields { grid-template-columns: 1fr; }
  .migration-actions .el-button { margin-left: 0; }
}
</style>

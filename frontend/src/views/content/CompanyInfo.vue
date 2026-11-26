<template>
  <el-card>
    <div class="page-bar">
      <h3>公司信息</h3>
      <el-button :icon="Refresh" :loading="loading" @click="load">重新加载</el-button>
    </div>

    <el-form :model="form" label-width="90px" style="max-width: 640px">
      <el-form-item label="企业全称">
        <el-input v-model="form.fullName" placeholder="名片首页公司名" />
      </el-form-item>
      <el-form-item label="企业简称">
        <el-input v-model="form.shortName" placeholder="小程序顶栏居中展示，留空则显示企业全称" />
        <div class="field-tip">仅作为小程序中的显示值使用，和后台左边栏顶部标识没有关联</div>
      </el-form-item>
      <!-- 首页公司名 / 简介文字的展示档位（大/中/小/关闭）已迁到
           「小程序设置 → 名片页面样式」页签，与首页背景、板块顺序等版式配置放一起。 -->
      <el-form-item label="地址">
        <el-input v-model="form.address" placeholder="名片首页地址" />
      </el-form-item>
      <el-form-item label="座机">
        <el-input v-model="form.tel" placeholder="名片首页座机" />
      </el-form-item>
      <el-form-item label="LOGO">
        <UploadField v-model="form.logo" :multiple="false" accept="image/*" library-type="logo" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </el-form-item>
    </el-form>
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { reactive, ref, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { getCompanyInfo, saveCompanyInfo } from '@/api/content'
import { useTenantStore } from '@/store/tenant'
import UploadField from '@/components/UploadField.vue'

const tenant = useTenantStore()

const loading = ref(false)
const saving = ref(false)
const form = reactive({
  id: 1,
  fullName: '',
  shortName: '',
  address: '',
  tel: '',
  logo: '',
})

async function load() {
  loading.value = true
  try {
    const d = await getCompanyInfo()
    Object.assign(form, d)
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    // 不提交档位字段：后端为指针部分更新（nil=不写），避免把样式页设置冲掉。
    await saveCompanyInfo({
      fullName: form.fullName,
      shortName: form.shortName,
      address: form.address,
      tel: form.tel,
      logo: form.logo,
    })
    // 顶栏 LOGO 与这一页同一份数据：保存后立刻刷新，别让用户存完还挂着旧图。
    // 后端会归一裸键，所以重新读接口而不是直接把 form.logo 塞给顶栏。
    void tenant.loadCompanyLogo()
    ElMessage.success('已保存')
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

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
.field-tip {
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
}
</style>

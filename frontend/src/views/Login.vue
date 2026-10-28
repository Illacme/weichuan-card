<template>
  <div class="login-wrap">
    <el-card class="login-card">
      <div class="login-head">
        <img class="login-logo" src="/logo.png" alt="微传名片" />
        <h2>{{ settings.loginDisplayBrand }} · 管理后台</h2>
        <p class="login-sub">管理员登录（平台 / 企业）</p>
      </div>
      <el-form ref="formRef" :model="form" :rules="rules" @submit.prevent="onSubmit">
        <el-form-item prop="username">
          <el-input v-model="form.username" placeholder="用户名" :prefix-icon="User" clearable />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            show-password
            placeholder="密码"
            :prefix-icon="Lock"
            @keyup.enter="onSubmit"
          />
        </el-form-item>
        <el-button
          type="primary"
          class="login-btn"
          :loading="loading"
          :disabled="loading"
          @click="onSubmit"
        >
          登录
        </el-button>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { User, Lock } from '@element-plus/icons-vue'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage } from 'element-plus'
import { adminLogin } from '@/api/auth'
import { useAuthStore } from '@/store/auth'
import { useSettingsStore } from '@/store/settings'

const auth = useAuthStore()
const settings = useSettingsStore()
const route = useRoute()
const router = useRouter()

const formRef = ref<FormInstance>()
const loading = ref(false)
const form = reactive({ username: '', password: '' })

const rules: FormRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

// 登录页标题品牌名取自后台「系统设置 → brand_name」（公开接口，无需登录）；
// 若 URL 带 ?company=<白标码> 且命中某 enabled 企业，则显示该企业简称（白标），
// 否则回落全局品牌名 / 内置默认「微传名片」。
onMounted(() => {
  const q = route.query.company
  const company = (typeof q === 'string' && q.trim()) || undefined
  settings.load(company)
})

async function onSubmit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return
  loading.value = true
  try {
    const resp = await adminLogin(form.username, form.password)
    auth.setAuth(form.username, {
      companyId: resp.companyId ?? 0,
      companyName: resp.companyName ?? '',
      companyShortName: resp.companyShortName ?? '',
      isPlatform: resp.isPlatform ?? false,
    })
    ElMessage.success('登录成功')
    const redirect = (route.query.redirect as string) || '/apply/open'
    router.replace(redirect)
  } catch {
    // 拦截器已提示（账号不存在 / 密码错误 / 企业已停用）
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-wrap {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #1f2d3d 0%, #2d3e50 100%);
}
.login-card {
  width: min(360px, 90vw);
}
.login-head {
  text-align: center;
  margin-bottom: 18px;
}
.login-logo {
  width: 48px;
  height: 48px;
  border-radius: 10px;
  background: #fff;
  border: 1px solid #ebeef5;
  display: block;
  object-fit: cover;
  margin: 0 auto 10px;
}
.login-sub {
  color: #909399;
  font-size: 13px;
  margin: 4px 0 0;
}
.login-btn {
  width: 100%;
  margin-top: 4px;
}
</style>

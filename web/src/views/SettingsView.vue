<script setup lang="ts">
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import { LogOut, Upload } from "lucide-vue-next";
import { api } from "../api";
import { useAuthStore, useToastStore } from "../stores";

const auth = useAuthStore();
const toast = useToastStore();
const router = useRouter();

const oldPassword = ref("");
const newPassword = ref("");
const confirmPassword = ref("");
const passwordBusy = ref(false);
const avatarBusy = ref(false);

const initial = computed(() => auth.user?.username.slice(0, 1).toUpperCase() ?? "?");

async function uploadAvatar(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) {
    return;
  }
  avatarBusy.value = true;
  try {
    await api.uploadAvatar(file);
    await auth.restore();
    toast.push("头像已更新");
  } catch (error) {
    toast.error(error);
  } finally {
    avatarBusy.value = false;
    input.value = "";
  }
}

async function changePassword() {
  if (newPassword.value !== confirmPassword.value) {
    toast.push("两次输入的新密码不一致", "error");
    return;
  }
  passwordBusy.value = true;
  try {
    await api.changePassword(oldPassword.value, newPassword.value);
    toast.push("密码已修改，请重新登录");
    await auth.logout();
    router.push("/login");
  } catch (error) {
    toast.error(error);
  } finally {
    passwordBusy.value = false;
  }
}

async function logout() {
  await auth.logout();
  toast.push("已退出登录");
  router.push("/");
}
</script>

<template>
  <section class="page">
    <h1 class="page-title">设置</h1>

    <section class="card">
      <h2>头像</h2>
      <div class="avatar-row">
        <img v-if="auth.user?.avatar_url" :src="auth.user.avatar_url" alt="头像" class="avatar" />
        <span v-else class="avatar fallback">{{ initial }}</span>
        <label class="btn">
          <Upload :size="16" />
          {{ avatarBusy ? "上传中…" : "更换头像" }}
          <input type="file" accept="image/jpeg,image/png,image/webp" hidden @change="uploadAvatar" />
        </label>
        <span class="muted">支持 jpg / png / webp，最大 2MB</span>
      </div>
    </section>

    <section class="card">
      <h2>修改密码</h2>
      <form class="form" @submit.prevent="changePassword">
        <label class="field">
          <span>当前密码</span>
          <input v-model="oldPassword" type="password" required autocomplete="current-password" />
        </label>
        <label class="field">
          <span>新密码</span>
          <input v-model="newPassword" type="password" required minlength="8" maxlength="72" autocomplete="new-password" />
        </label>
        <label class="field">
          <span>确认新密码</span>
          <input v-model="confirmPassword" type="password" required minlength="8" maxlength="72" autocomplete="new-password" />
        </label>
        <button class="btn primary" type="submit" :disabled="passwordBusy">
          {{ passwordBusy ? "提交中…" : "修改密码" }}
        </button>
      </form>
    </section>

    <section class="card">
      <h2>账号</h2>
      <button class="btn" @click="logout"><LogOut :size="16" /> 退出登录</button>
    </section>
  </section>
</template>

<style scoped>
.page {
  max-width: 720px;
  margin: 0 auto;
  padding: 32px 24px 60px;
}

.card {
  padding: 20px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--panel);
  margin-bottom: 16px;
}

.card h2 {
  margin: 0 0 14px;
  font-size: 15px;
}

.avatar-row {
  display: flex;
  align-items: center;
  gap: 14px;
}

.avatar {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  object-fit: cover;
}

.avatar.fallback {
  display: grid;
  place-items: center;
  background: var(--accent);
  font-size: 26px;
  font-weight: 700;
}

.form {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-width: 360px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: var(--muted);
}
</style>
